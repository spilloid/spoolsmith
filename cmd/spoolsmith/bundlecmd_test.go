package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spilloid/spoolsmith/internal/bundle"
	"github.com/spilloid/spoolsmith/internal/evidence"
	"github.com/spilloid/spoolsmith/internal/install"
	"github.com/spilloid/spoolsmith/internal/probe"
)

// bundleFakeEnvironment adds the clone capabilities to the CLI's existing fake.
type bundleFakeEnvironment struct {
	*cliFakeEnvironment
	port       install.PortConfiguration
	exportErr  error
	exportedTo string
}

func (f *bundleFakeEnvironment) LookupPort(context.Context, string) (install.PortConfiguration, error) {
	return f.port, nil
}

func (f *bundleFakeEnvironment) ExportDriver(_ context.Context, _, destDir string) (install.DriverExport, error) {
	if f.exportErr != nil {
		return install.DriverExport{}, f.exportErr
	}
	f.exportedTo = destDir
	if err := os.MkdirAll(filepath.Join(destDir, "amd64"), 0700); err != nil {
		return install.DriverExport{}, err
	}
	for name, content := range map[string]string{
		"brhl2315a.inf":    "[Version]\nCatalogFile=brhl2315a.cat\n",
		"brhl2315a.cat":    "catalog bytes",
		"amd64/driver.dll": "driver bytes",
	} {
		if err := os.WriteFile(filepath.Join(destDir, filepath.FromSlash(name)), []byte(content), 0600); err != nil {
			return install.DriverExport{}, err
		}
	}
	return install.DriverExport{PublishedName: "oem15.inf", OriginalName: "brhl2315a.inf", Provider: "Brother"}, nil
}

func bundleTestApplication(t *testing.T) (application, *bundleFakeEnvironment) {
	t.Helper()
	app := testApplication()
	captured := evidence.Evidence{
		IP:         "192.0.2.10",
		Provenance: "captured",
		HTTPTitle:  "Brother HL-L2315D series",
		PJLID:      "Brother HL-L2315D series",
	}
	collect := func(context.Context, string) (probe.Result, error) {
		return probe.Result{Evidence: captured}, nil
	}
	app.collect = collect
	app.workflow.Collect = collect
	env := &bundleFakeEnvironment{
		cliFakeEnvironment: app.environment.(*cliFakeEnvironment),
		port:               install.PortConfiguration{PortName: "SpoolSmith-192.0.2.10", HostAddress: "192.0.2.10", PortNumber: 9100, Protocol: 1},
	}
	app.environment = env
	return app, env
}

// TestCloneThenApplyAcrossMachines is the workflow this feature exists for:
// one machine that already prints becomes a file, and that file sets up the
// next machine without anyone retyping a driver name.
func TestCloneThenApplyAcrossMachines(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	app, _ := bundleTestApplication(t)
	bundlePath := filepath.Join(t.TempDir(), "office.ssb")

	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"clone", "Test Printer", bundlePath, "--include-driver", "--note", "front desk"}, strings.NewReader(""), &stdout, &stderr, app)
	if code != 0 {
		t.Fatalf("clone code=%d\n%s\n%s", code, stdout.String(), stderr.String())
	}
	assertValidJSON(t, stdout.Bytes())

	opened, err := bundle.Open(bundlePath)
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	if err := opened.Verify(); err != nil {
		t.Fatalf("clone wrote a bundle that does not verify: %v", err)
	}
	// The operator never supplied the driver name; it came off the queue.
	if opened.Manifest.Profile.DriverName != "Verified Windows Driver" {
		t.Fatalf("driver name = %q, want the installed queue's own driver", opened.Manifest.Profile.DriverName)
	}
	if opened.Manifest.Driver == nil || opened.Manifest.Driver.INF != "brhl2315a.inf" {
		t.Fatalf("driver payload = %#v", opened.Manifest.Driver)
	}
	if opened.Manifest.Note != "front desk" {
		t.Fatalf("note = %q", opened.Manifest.Note)
	}

	// Machine 1: review the plan once and take its fingerprint.
	stdout.Reset()
	stderr.Reset()
	code = run(context.Background(), []string{"apply", bundlePath, "--dry-run", "--json"}, strings.NewReader(""), &stdout, &stderr, app)
	if code != 0 {
		t.Fatalf("apply --dry-run code=%d\n%s\n%s", code, stdout.String(), stderr.String())
	}
	var preview install.Outcome
	if err := json.Unmarshal(stdout.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	if preview.Status != "dry-run" || preview.PlanHash == "" {
		t.Fatalf("preview = %#v", preview)
	}
	if preview.Plan.BundleDriver == nil {
		t.Fatal("preview plan does not mention the bundle's driver payload")
	}

	// Machines 2..N: the same file plus the reviewed fingerprint, no prompt.
	for _, machine := range []string{"desk-02", "desk-03"} {
		freshApp, env := bundleTestApplication(t)
		env.driverPresent = false // the driver payload is what makes this work
		stdout.Reset()
		stderr.Reset()
		code = run(context.Background(), []string{"apply", bundlePath, "--plan-hash", preview.PlanHash, "--json"}, strings.NewReader(""), &stdout, &stderr, freshApp)
		if code != 0 {
			t.Fatalf("%s: apply code=%d\n%s\n%s", machine, code, stdout.String(), stderr.String())
		}
		var outcome install.Outcome
		if err := json.Unmarshal(stdout.Bytes(), &outcome); err != nil {
			t.Fatal(err)
		}
		if outcome.Status != "success" || !outcome.Confirmed {
			t.Fatalf("%s: outcome = %#v", machine, outcome)
		}
		if outcome.PlanHash != preview.PlanHash {
			t.Fatalf("%s: plan fingerprint drifted between machines: %s vs %s", machine, outcome.PlanHash, preview.PlanHash)
		}
		if len(env.ran) == 0 || !strings.Contains(env.ran[0], "pnputil.exe /add-driver") {
			t.Fatalf("%s: driver staging was not the first command: %#v", machine, env.ran)
		}
	}
}

func TestApplyRejectsAnUnreviewedPlan(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	app, env := bundleTestApplication(t)
	bundlePath := filepath.Join(t.TempDir(), "office.ssb")
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), []string{"clone", "Test Printer", bundlePath}, strings.NewReader(""), &stdout, &stderr, app); code != 0 {
		t.Fatalf("clone code=%d %s", code, stderr.String())
	}
	env.ran = nil

	stdout.Reset()
	stderr.Reset()
	code := run(context.Background(), []string{"apply", bundlePath, "--plan-hash", strings.Repeat("0", 64), "--json"}, strings.NewReader(""), &stdout, &stderr, app)
	if code != int(install.ExitNotConfirmed) {
		t.Fatalf("apply with a wrong fingerprint = code %d, want %d", code, install.ExitNotConfirmed)
	}
	if len(env.ran) != 0 {
		t.Fatalf("an unreviewed plan ran %d commands", len(env.ran))
	}
}

// TestApplyWithoutConfirmationRunsNothing pins the behaviour a scripted
// rollout must not be able to bypass by accident: no fingerprint, no --yes,
// no terminal means nothing is mutated.
func TestApplyWithoutConfirmationRunsNothing(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	app, env := bundleTestApplication(t)
	bundlePath := filepath.Join(t.TempDir(), "office.ssb")
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), []string{"clone", "Test Printer", bundlePath}, strings.NewReader(""), &stdout, &stderr, app); code != 0 {
		t.Fatalf("clone code=%d %s", code, stderr.String())
	}
	env.ran = nil
	stdout.Reset()
	stderr.Reset()
	code := run(context.Background(), []string{"apply", bundlePath, "--json"}, strings.NewReader(""), &stdout, &stderr, app)
	if code != int(install.ExitNotConfirmed) || len(env.ran) != 0 {
		t.Fatalf("apply = code %d after running %d commands", code, len(env.ran))
	}
}

func TestCloneRefusesQueuesItCannotReproduce(t *testing.T) {
	app, env := bundleTestApplication(t)
	env.port = install.PortConfiguration{PortName: "IP_192.0.2.10", HostAddress: "192.0.2.10", PortNumber: 515, Protocol: 2}
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"clone", "Test Printer", filepath.Join(t.TempDir(), "lpr.ssb")}, strings.NewReader(""), &stdout, &stderr, app)
	if code == 0 {
		t.Fatal("clone accepted an LPR queue it cannot reproduce")
	}
	if !strings.Contains(stderr.String(), "not RAW") {
		t.Fatalf("clone did not explain why: %s", stderr.String())
	}
}

func TestBundleInspectReadsWithoutTouchingTheNetwork(t *testing.T) {
	app, _ := bundleTestApplication(t)
	bundlePath := filepath.Join(t.TempDir(), "office.ssb")
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), []string{"clone", "Test Printer", bundlePath, "--include-driver"}, strings.NewReader(""), &stdout, &stderr, app); code != 0 {
		t.Fatalf("clone code=%d %s", code, stderr.String())
	}

	offline := testApplication()
	offline.collect = func(context.Context, string) (probe.Result, error) {
		t.Fatal("bundle inspect probed the network")
		return probe.Result{}, nil
	}
	stdout.Reset()
	stderr.Reset()
	if code := run(context.Background(), []string{"bundle", "inspect", bundlePath}, strings.NewReader(""), &stdout, &stderr, offline); code != 0 {
		t.Fatalf("bundle inspect code=%d %s", code, stderr.String())
	}
	assertValidJSON(t, stdout.Bytes())
	for _, want := range []string{"Test Printer", "Driver payload"} {
		if !strings.Contains(stderr.String(), want) && !strings.Contains(stdout.String(), want) {
			t.Fatalf("bundle inspect output is missing %q:\n%s", want, stderr.String())
		}
	}
}

func TestCloneWithoutDriverSaysWhatIsMissing(t *testing.T) {
	app, _ := bundleTestApplication(t)
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"clone", "Test Printer", filepath.Join(t.TempDir(), "thin.ssb")}, strings.NewReader(""), &stdout, &stderr, app)
	if code != 0 {
		t.Fatalf("clone code=%d %s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "must already have this driver") {
		t.Fatalf("clone did not warn that the bundle carries no driver: %s", stderr.String())
	}
}
