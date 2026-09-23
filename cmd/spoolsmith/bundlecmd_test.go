package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
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
		port:               install.PortConfiguration{PortName: "RAW9100-192.0.2.10", HostAddress: "192.0.2.10", PortNumber: 9100, Protocol: 1},
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

// TestCloneDegradesToOfflineWhenPrinterUnreachable is the actual operator
// complaint this feature exists for: physically moving a printer routinely
// means it can't be reached exactly when someone wants to copy its settings
// off the old PC. copy now writes a bundle from what Windows already knows
// about the queue instead of failing outright, and says so plainly on stderr
// rather than quietly.
func TestCloneDegradesToOfflineWhenPrinterUnreachable(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	app, _ := bundleTestApplication(t)
	unreachable := func(context.Context, string) (probe.Result, error) {
		return probe.Result{}, errors.New("connection refused")
	}
	app.collect = unreachable
	bundlePath := filepath.Join(t.TempDir(), "office.ssb")

	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"copy", "Test Printer", bundlePath}, strings.NewReader(""), &stdout, &stderr, app)
	if code != 0 {
		t.Fatalf("copy code=%d\n%s\n%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), bundle.UnconfirmedIdentityNotice) {
		t.Fatalf("no unconfirmed-identity notice on stderr:\n%s", stderr.String())
	}

	opened, err := bundle.Open(bundlePath)
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	if err := opened.Verify(); err != nil {
		t.Fatalf("degraded copy wrote a bundle that does not verify: %v", err)
	}
	if opened.Manifest.Profile.Evidence.Provenance != "unconfirmed" {
		t.Fatalf("Provenance = %q, want unconfirmed", opened.Manifest.Profile.Evidence.Provenance)
	}
	if opened.Manifest.Profile.PrinterName != "Test Printer" {
		t.Fatalf("profile did not carry the locally known queue: %+v", opened.Manifest.Profile)
	}

	// bundle inspect must tell an operator reading it later the same thing.
	stdout.Reset()
	stderr.Reset()
	code = run(context.Background(), []string{"bundle", "inspect", bundlePath}, strings.NewReader(""), &stdout, &stderr, app)
	if code != 0 || !strings.Contains(stderr.String(), "unconfirmed") {
		t.Fatalf("bundle inspect code=%d\n%s\n%s", code, stdout.String(), stderr.String())
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

// allQueuesFakeEnvironment adds the listing capability to the clone-capable
// fake, so `copy --all` can be exercised without a real Windows machine.
//
// cliFakeEnvironment.LookupPrinter ignores the name it is asked for and
// always returns the same canned configuration, which is fine for every
// other test here but would let a batch-loop bug (e.g. reusing one queue
// name for every iteration) pass unnoticed. requestedPrinters records what
// was actually asked for so the batch tests can tell the loop really visited
// each distinct copyable queue, not just that some file got written per
// queue in the list.
type allQueuesFakeEnvironment struct {
	*bundleFakeEnvironment
	queues            []install.InstalledQueue
	requestedPrinters []string
}

func (f *allQueuesFakeEnvironment) ListPrinters(context.Context) ([]install.InstalledQueue, error) {
	return f.queues, nil
}

func (f *allQueuesFakeEnvironment) LookupPrinter(ctx context.Context, name string) (install.PrinterConfiguration, error) {
	f.requestedPrinters = append(f.requestedPrinters, name)
	return f.bundleFakeEnvironment.LookupPrinter(ctx, name)
}

// TestCloneAllSkipsUncopyableAndBundlesTheRest is the batch equivalent of
// TestCloneThenApplyAcrossMachines: one queue this fake can actually
// reproduce, one it can't (Microsoft Print to PDF has no reproducible RAW
// TCP/9100 port), and the run should add the first to the printer set while
// explaining and continuing past the second, not aborting the whole batch.
func TestCloneAllSkipsUncopyableAndBundlesTheRest(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	app, env := bundleTestApplication(t)
	all := &allQueuesFakeEnvironment{bundleFakeEnvironment: env, queues: sampleQueues()}
	app.environment = all
	setPath := filepath.Join(t.TempDir(), "printers.zip")

	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"copy", "--all", "--out", setPath}, strings.NewReader(""), &stdout, &stderr, app)
	if code != 0 {
		t.Fatalf("copy --all code=%d\n%s\n%s", code, stdout.String(), stderr.String())
	}
	assertValidJSON(t, stdout.Bytes())

	var result copyAllResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("decode result: %v\n%s", err, stdout.String())
	}
	if result.Requested != 2 || result.Written != 1 || result.Skipped != 1 || result.Failed != 0 || result.SetPath != setPath {
		t.Fatalf("result = %+v", result)
	}
	// Only the copyable queue should ever reach CloneQueue -- the uncopyable
	// one must be skipped before any lookup, not merely fail one afterward.
	if want := []string{"Office"}; !slices.Equal(all.requestedPrinters, want) {
		t.Fatalf("requested printers = %v, want %v", all.requestedPrinters, want)
	}
	set, err := bundle.OpenSet(setPath)
	if err != nil {
		t.Fatal(err)
	}
	defer set.Close()
	if !slices.Equal(set.Members, []string{"Office.ssb"}) || !result.Queues[0].DriverIncluded {
		t.Fatalf("set members = %v; result = %+v", set.Members, result.Queues)
	}
	extracted, err := set.Extract("Office.ssb", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	opened, err := bundle.Open(extracted)
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	if opened.Manifest.Profile.PrinterName != "Test Printer" || opened.Manifest.Driver == nil {
		t.Fatalf("member = %+v (this fake's LookupPrinter always names it Test Printer)", opened.Manifest)
	}
	if !strings.Contains(stderr.String(), "Microsoft Print to PDF") {
		t.Fatalf("stderr does not explain the skipped queue: %s", stderr.String())
	}
}

// TestCloneAllFailsWhenNothingIsCopyable checks the batch reports failure,
// not silent success, when every queue is skipped -- and writes no file.
func TestCloneAllFailsWhenNothingIsCopyable(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	app, env := bundleTestApplication(t)
	app.environment = &allQueuesFakeEnvironment{bundleFakeEnvironment: env, queues: []install.InstalledQueue{sampleQueues()[1]}}
	setPath := filepath.Join(t.TempDir(), "printers.zip")

	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"copy", "--all", setPath}, strings.NewReader(""), &stdout, &stderr, app)
	if code == 0 {
		t.Fatalf("expected a non-zero exit when nothing was copied:\n%s", stderr.String())
	}
	assertValidJSON(t, stdout.Bytes())
	if _, err := os.Stat(setPath); !os.IsNotExist(err) {
		t.Fatalf("wrote a set with no printers: %v", err)
	}
}

// TestCopyAllDefaultsToATimestampedSetInTheCurrentFolder: with no name, copy
// --all writes SpoolSmith-printers-<date>-<time>.zip where it was run.
func TestCopyAllDefaultsToATimestampedSetInTheCurrentFolder(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	app, env := bundleTestApplication(t)
	app.environment = &allQueuesFakeEnvironment{bundleFakeEnvironment: env, queues: sampleQueues()}
	t.Chdir(t.TempDir())
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), []string{"copy", "--all"}, strings.NewReader(""), &stdout, &stderr, app); code != 0 {
		t.Fatalf("copy --all code=%d\n%s", code, stderr.String())
	}
	matches, err := filepath.Glob("SpoolSmith-printers-*.zip")
	if err != nil || len(matches) != 1 {
		t.Fatalf("default set file = %v, %v", matches, err)
	}
	if isSet, err := bundle.IsSet(matches[0]); err != nil || !isSet {
		t.Fatalf("default output is not a printer set: %v, %v", isSet, err)
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

// TestInspectRoutesABundleTargetToBundleInspect is the fix for `inspect
// <file>.ssb` being advertised in CLI help and in the GUI's Tools -> Inspect,
// yet only actually working on the GUI side: the generic `inspect` command
// used to hand a .ssb straight to evidence.LoadFixture and fail to decode it
// as JSON. One target type, one behavior, matching what `bundle inspect`
// already does for it.
func TestInspectRoutesABundleTargetToBundleInspect(t *testing.T) {
	app, _ := bundleTestApplication(t)
	bundlePath := filepath.Join(t.TempDir(), "office.ssb")
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), []string{"clone", "Test Printer", bundlePath}, strings.NewReader(""), &stdout, &stderr, app); code != 0 {
		t.Fatalf("clone code=%d %s", code, stderr.String())
	}

	offline := testApplication()
	offline.collect = func(context.Context, string) (probe.Result, error) {
		t.Fatal("inspect probed the network")
		return probe.Result{}, nil
	}
	stdout.Reset()
	stderr.Reset()
	if code := run(context.Background(), []string{"inspect", bundlePath}, strings.NewReader(""), &stdout, &stderr, offline); code != 0 {
		t.Fatalf("inspect code=%d %s", code, stderr.String())
	}
	assertValidJSON(t, stdout.Bytes())
	if !strings.Contains(stderr.String(), "Test Printer") {
		t.Fatalf("inspect on a bundle did not show its manifest: %s", stderr.String())
	}
}

func TestCopyReportsWhyTheDriverWasNotIncluded(t *testing.T) {
	for _, tc := range []struct {
		name  string
		args  []string
		setup func(*bundleFakeEnvironment)
		want  string
	}{
		{"settings only", []string{"--settings-only"}, func(*bundleFakeEnvironment) {}, "settings only, as requested"},
		{"not elevated", nil, func(env *bundleFakeEnvironment) { env.elevated = false }, "administrator rights"},
		{"export fails", nil, func(env *bundleFakeEnvironment) { env.exportErr = errors.New("pnputil failed") }, "pnputil failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app, env := bundleTestApplication(t)
			tc.setup(env)
			path := filepath.Join(t.TempDir(), "thin.ssb")
			var stdout, stderr bytes.Buffer
			args := append([]string{"copy", "Test Printer", "--out", path}, tc.args...)
			if code := run(context.Background(), args, strings.NewReader(""), &stdout, &stderr, app); code != 0 {
				t.Fatalf("copy code=%d %s", code, stderr.String())
			}
			for _, want := range []string{"Driver not included", tc.want, `must already have "Verified Windows Driver" installed`} {
				if !strings.Contains(stderr.String(), want) {
					t.Fatalf("stderr missing %q:\n%s", want, stderr.String())
				}
			}
			opened, err := bundle.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer opened.Close()
			if opened.Manifest.Driver != nil {
				t.Fatal("bundle carries a driver it should not")
			}
		})
	}
}

func TestCopyIncludeDriverIsADeprecatedNoOp(t *testing.T) {
	app, _ := bundleTestApplication(t)
	path := filepath.Join(t.TempDir(), "office.ssb")
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), []string{"copy", "Test Printer", path, "--include-driver"}, strings.NewReader(""), &stdout, &stderr, app); code != 0 {
		t.Fatalf("copy code=%d %s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "--include-driver is no longer needed") || !strings.Contains(stderr.String(), "Driver included") {
		t.Fatalf("stderr = %s", stderr.String())
	}
}

// writeTestSet copies the fake's queue twice (Office, Warehouse) into one
// printer set and returns its path.
func writeTestSet(t *testing.T) string {
	t.Helper()
	app, env := bundleTestApplication(t)
	queues := sampleQueues()
	warehouse := queues[0]
	warehouse.PrinterName = "Warehouse"
	app.environment = &allQueuesFakeEnvironment{bundleFakeEnvironment: env, queues: []install.InstalledQueue{queues[0], warehouse}}
	path := filepath.Join(t.TempDir(), "printers.zip")
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), []string{"copy", "--all", path, "--note", "site move"}, strings.NewReader(""), &stdout, &stderr, app); code != 0 {
		t.Fatalf("copy --all code=%d %s", code, stderr.String())
	}
	return path
}

// TestApplySetConfirmsEachPrinterOnItsOwn: a set never widens one
// confirmation to cover several printers. Each member shows its own plan and
// takes its own answer; a declined member runs nothing and fails the exit
// code, without stopping the next member.
func TestApplySetConfirmsEachPrinterOnItsOwn(t *testing.T) {
	setPath := writeTestSet(t)
	app, env := bundleTestApplication(t)
	app.inputTerminal = true
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"apply", setPath}, strings.NewReader("n\ny\n"), &stdout, &stderr, app)
	if code != int(install.ExitNotConfirmed) {
		t.Fatalf("apply set code=%d, want %d\n%s", code, install.ExitNotConfirmed, stderr.String())
	}
	if got := strings.Count(stderr.String(), "Proceed? [y/N]"); got != 2 {
		t.Fatalf("asked %d times, want once per printer:\n%s", got, stderr.String())
	}
	for _, want := range []string{"Office.ssb", "Warehouse.ssb", "[1/2]", "[2/2]", "1 applied, 1 not confirmed, 0 failed"} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("stderr missing %q:\n%s", want, stderr.String())
		}
	}
	var result applySetResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("stdout = %s: %v", stdout.String(), err)
	}
	if len(result.Members) != 2 || result.Members[0].Status != "not-confirmed" || result.Members[1].Status != "success" {
		t.Fatalf("result = %+v", result)
	}
	if len(env.ran) == 0 {
		t.Fatal("the confirmed printer ran nothing")
	}
}

func TestApplySetWithYesStillShowsEveryPlan(t *testing.T) {
	setPath := writeTestSet(t)
	app, env := bundleTestApplication(t)
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"apply", setPath, "--yes"}, strings.NewReader(""), &stdout, &stderr, app)
	if code != 0 {
		t.Fatalf("apply set --yes code=%d\n%s", code, stderr.String())
	}
	if got := strings.Count(stderr.String(), "Printer configured:"); got != 2 {
		t.Fatalf("configured %d printers, want 2:\n%s", got, stderr.String())
	}
	if len(env.ran) == 0 {
		t.Fatal("nothing ran")
	}
}

func TestApplySetMemberSelectsOnePrinter(t *testing.T) {
	setPath := writeTestSet(t)
	app, _ := bundleTestApplication(t)
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), []string{"apply", setPath, "--member", "warehouse", "--dry-run", "--json"}, strings.NewReader(""), &stdout, &stderr, app); code != 0 {
		t.Fatalf("apply --member code=%d\n%s", code, stderr.String())
	}
	var result applySetResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Members) != 1 || result.Members[0].Member != "Warehouse.ssb" || result.Members[0].Status != "dry-run" {
		t.Fatalf("result = %+v", result)
	}

	stdout.Reset()
	stderr.Reset()
	if code := run(context.Background(), []string{"apply", setPath, "--member", "Lobby.ssb"}, strings.NewReader(""), &stdout, &stderr, app); code != int(install.ExitUsageError) {
		t.Fatalf("unknown member code=%d", code)
	}
	stdout.Reset()
	stderr.Reset()
	if code := run(context.Background(), []string{"apply", setPath, "--plan-hash", strings.Repeat("0", 64)}, strings.NewReader(""), &stdout, &stderr, app); code != int(install.ExitUsageError) || !strings.Contains(stderr.String(), "--member") {
		t.Fatalf("--plan-hash over a whole set code=%d %s", code, stderr.String())
	}
}

func TestInspectListsASetsPrinters(t *testing.T) {
	setPath := writeTestSet(t)
	offline := testApplication()
	offline.collect = func(context.Context, string) (probe.Result, error) {
		t.Fatal("inspect probed the network")
		return probe.Result{}, nil
	}
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), []string{"inspect", setPath}, strings.NewReader(""), &stdout, &stderr, offline); code != 0 {
		t.Fatalf("inspect set code=%d %s", code, stderr.String())
	}
	var result setInspectResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Note != "site move" || len(result.Members) != 2 {
		t.Fatalf("result = %+v", result)
	}
	for _, m := range result.Members {
		if m.PrinterName != "Test Printer" || m.Target != "192.0.2.10" || m.DriverName != "Verified Windows Driver" || !m.DriverEmbedded {
			t.Fatalf("member = %+v", m)
		}
	}
	if !strings.Contains(stderr.String(), "Printer set") || !strings.Contains(stderr.String(), "Driver: Verified Windows Driver -- included") {
		t.Fatalf("stderr = %s", stderr.String())
	}
}
