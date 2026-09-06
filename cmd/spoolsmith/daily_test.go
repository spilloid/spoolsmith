package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spilloid/spoolsmith/internal/evidence"
	"github.com/spilloid/spoolsmith/internal/install"
	"github.com/spilloid/spoolsmith/internal/probe"
)

func TestCaptureThenInstallCLI(t *testing.T) {
	app := testApplication()
	e := evidence.Evidence{IP: "192.0.2.10", Provenance: "captured", HTTPTitle: "Uncataloged printer 123", PJLID: "Uncataloged printer 123"}
	app.collect = func(context.Context, string) (probe.Result, error) { return probe.Result{Evidence: e}, nil }
	app.workflow.Collect = app.collect
	path := filepath.Join(t.TempDir(), "printer.json")
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"profile", "capture", e.IP, path, "--name", "Accounting", "--driver", "Verified OEM driver"}, strings.NewReader(""), &stdout, &stderr, app)
	if code != 0 {
		t.Fatalf("capture code=%d %s %s", code, stdout.String(), stderr.String())
	}
	assertValidJSON(t, stdout.Bytes())
	p, err := install.LoadProfile(path)
	if err != nil || p.DriverName != "Verified OEM driver" {
		t.Fatalf("profile=%#v %v", p, err)
	}
	stdout.Reset()
	stderr.Reset()
	code = run(context.Background(), []string{"install", "--profile", path, "--dry-run", "--yes"}, strings.NewReader(""), &stdout, &stderr, app)
	if code != 0 {
		t.Fatalf("install code=%d %s %s", code, stdout.String(), stderr.String())
	}
	var outcome install.Outcome
	if err := json.Unmarshal(stdout.Bytes(), &outcome); err != nil {
		t.Fatal(err)
	}
	if outcome.Plan == nil || outcome.Plan.PrinterName != "Accounting" || outcome.Status != "dry-run" {
		t.Fatalf("outcome=%#v", outcome)
	}
	if env := app.environment.(*cliFakeEnvironment); len(env.ran) != 0 {
		t.Fatal("dry-run mutated")
	}
	for _, args := range [][]string{{"--profile", path, "192.0.2.11"}, {"--profile", path, "--force-family", "test-family"}, {"--profile", path, "--profile", path}} {
		if _, err := parseInstallArgs(args); err == nil {
			t.Fatalf("accepted mixed/duplicate options %v", args)
		}
	}
}

func TestCaptureInvalidFlagsDoNotProbeOrWrite(t *testing.T) {
	app := testApplication()
	app.collect = func(context.Context, string) (probe.Result, error) { panic("invalid args probed network") }
	path := filepath.Join(t.TempDir(), "printer.json")
	for _, flags := range [][]string{{}, {"--name", "Q"}, {"--name", "Q", "--driver", "D", "--driver", "E"}} {
		args := append([]string{"profile", "capture", "192.0.2.10", path}, flags...)
		var stdout, stderr bytes.Buffer
		if code := run(context.Background(), args, strings.NewReader(""), &stdout, &stderr, app); code != 2 {
			t.Fatalf("code=%d", code)
		}
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("unexpected file: %v", err)
	}
}

func TestDiscoveryCLIReportsUnresolvedCandidate(t *testing.T) {
	app := testApplication()
	app.discover = func(context.Context, string) (probe.Discovery, error) {
		return probe.Discovery{Network: "192.0.2.0/24", Scanned: 254, Candidates: []probe.Result{{Evidence: evidence.Evidence{IP: "192.0.2.10", Provenance: "captured", OpenPorts: []int{9100}}}}}, nil
	}
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), []string{"discover", "192.0.2.0/24"}, strings.NewReader(""), &stdout, &stderr, app); code != 0 {
		t.Fatalf("code=%d", code)
	}
	assertValidJSON(t, stdout.Bytes())
	if !strings.Contains(stdout.String(), `"confidence": 0`) || !strings.Contains(stdout.String(), "192.0.2.10") {
		t.Fatalf("dropped unknown candidate: %s", stdout.String())
	}
}

func TestHumanAddAndConfigureUseConcisePlans(t *testing.T) {
	for _, command := range []string{"add", "configure"} {
		app := testApplication()
		app.outputTerminal = true
		p := install.Profile{Version: 1, Target: "192.0.2.10", PrinterName: "Office", DriverName: "OEM driver", Evidence: evidence.Evidence{IP: "192.0.2.10", Provenance: "captured", HTTPTitle: "Printer Model"}}
		path := filepath.Join(t.TempDir(), "profile.json")
		if err := install.SaveProfile(path, p); err != nil {
			t.Fatal(err)
		}
		app.workflow.Collect = func(context.Context, string) (probe.Result, error) { return probe.Result{Evidence: p.Evidence}, nil }
		var stdout, stderr bytes.Buffer
		code := run(context.Background(), []string{command, "--profile", path, "--dry-run"}, strings.NewReader(""), &stdout, &stderr, app)
		if code != 0 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "RAW TCP 9100") || strings.Contains(stderr.String(), "$ErrorActionPreference") {
			t.Fatalf("%s code=%d stdout=%s stderr=%s", command, code, stdout.String(), stderr.String())
		}
		for _, expected := range []string{"manually selected", "not device authentication", "Driver source"} {
			if !strings.Contains(stderr.String(), expected) {
				t.Fatalf("compact plan hides %q: %s", expected, stderr.String())
			}
		}
	}
}

func TestProfilePackageEditAndClear(t *testing.T) {
	p := install.Profile{Version: 1, Target: "192.0.2.10", PrinterName: "Office", DriverName: "Brother HL-L2315D series", Evidence: evidence.Evidence{Provenance: "captured", HTTPTitle: "Brother HL-L2315D series"}}
	profilePath := filepath.Join(t.TempDir(), "printer.json")
	if err := install.SaveProfile(profilePath, p); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := runProfileEdit([]string{profilePath, "--package", "brother-y14a-c1-hostm-1110", "--archive", "driver.EXE"}, &stdout, &stderr); code != 0 {
		t.Fatalf("%d %s", code, stderr.String())
	}
	loaded, err := install.LoadProfile(profilePath)
	if err != nil || loaded.DriverPackage == nil || loaded.DriverPackage.Archive != "driver.EXE" {
		t.Fatalf("%#v %v", loaded, err)
	}
	options, err := parseInstallArgs([]string{"--profile", profilePath})
	if err != nil || options.Profile.DriverPackage.Archive != filepath.Join(filepath.Dir(profilePath), "driver.EXE") {
		t.Fatalf("%#v %v", options, err)
	}
	for _, flags := range [][]string{{"--clear-package", "--archive", "x"}, {"--archive", "x", "--clear-package"}, {"--clear-package", "--clear-package"}} {
		if code := runProfileEdit(append([]string{profilePath}, flags...), &stdout, &stderr); code != 2 {
			t.Fatalf("accepted conflicting flags: %v", flags)
		}
	}
	if code := runProfileEdit([]string{profilePath, "--clear-package"}, &stdout, &stderr); code != 0 {
		t.Fatalf("%d %s", code, stderr.String())
	}
	loaded, err = install.LoadProfile(profilePath)
	if err != nil || loaded.DriverPackage != nil {
		t.Fatalf("%#v %v", loaded, err)
	}
}
