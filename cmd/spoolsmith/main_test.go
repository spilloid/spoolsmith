package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/spilloid/spoolsmith/internal/catalog"
	"github.com/spilloid/spoolsmith/internal/evidence"
	"github.com/spilloid/spoolsmith/internal/install"
	"github.com/spilloid/spoolsmith/internal/probe"
)

func TestCommandOutputSeparation(t *testing.T) {
	app := testApplication()
	tests := []struct {
		name       string
		args       []string
		wantStderr string
	}{
		{name: "inspect", args: []string{"inspect", "../../fixtures/hp-laserjet-m404-synthetic.json"}},
		{name: "catalog probe", args: []string{"catalog", "probe", "192.0.2.10"}},
		{name: "catalog families", args: []string{"catalog", "families"}},
		{name: "install", args: []string{"install", "192.0.2.10", "--dry-run"}, wantStderr: "Install plan"},
		{name: "uninstall", args: []string{"uninstall", "Test Printer", "--dry-run"}, wantStderr: "Uninstall plan"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := run(context.Background(), tt.args, strings.NewReader(""), &stdout, &stderr, app)
			if code != int(install.ExitSuccess) {
				t.Fatalf("run() code = %d; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
			}
			assertValidJSON(t, stdout.Bytes())
			if tt.wantStderr == "" && stderr.Len() != 0 {
				t.Fatalf("stderr = %q, want empty", stderr.String())
			}
			if tt.wantStderr != "" && !strings.Contains(stderr.String(), tt.wantStderr) {
				t.Fatalf("stderr = %q, want %q", stderr.String(), tt.wantStderr)
			}
			if strings.Contains(stdout.String(), "Install plan") || strings.Contains(stdout.String(), "Uninstall plan") || strings.Contains(stdout.String(), "Proceed?") {
				t.Fatalf("stdout contains human/interactive text: %q", stdout.String())
			}
		})
	}
}

func TestCatalogFamiliesJSONContainsExactlyCatalogFamilies(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"catalog", "families"}, strings.NewReader(""), &stdout, &stderr, testApplication())
	if code != int(install.ExitSuccess) {
		t.Fatalf("run() code = %d, stderr = %q", code, stderr.String())
	}
	var got []catalog.Family
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("stdout is not family JSON: %v; %q", err, stdout.String())
	}
	if !reflect.DeepEqual(got, catalog.Families()) {
		t.Fatalf("catalog families = %#v, want %#v", got, catalog.Families())
	}
}

func TestCLIInvalidForceFamilyIsUsageErrorAndJSON(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"install", "192.0.2.10", "--force-family", "missing"}, strings.NewReader(""), &stdout, &stderr, testApplication())
	if code != int(install.ExitUsageError) {
		t.Fatalf("run() code = %d, want %d", code, install.ExitUsageError)
	}
	assertValidJSON(t, stdout.Bytes())
	if !strings.Contains(stdout.String(), "unknown forced family") || !strings.Contains(stderr.String(), "unknown forced family") {
		t.Fatalf("missing invalid-family diagnostic: stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestCLIValidForceFamilyMarksForcedPlan(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"install", "192.0.2.10", "--force-family", "test-family", "--dry-run"}, strings.NewReader(""), &stdout, &stderr, testApplication())
	if code != int(install.ExitSuccess) {
		t.Fatalf("run() code = %d; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	var outcome install.Outcome
	if err := json.Unmarshal(stdout.Bytes(), &outcome); err != nil {
		t.Fatalf("stdout is not outcome JSON: %v", err)
	}
	if outcome.Resolution != "forced-override" || outcome.Plan == nil || !outcome.Plan.ForcedOverride {
		t.Fatalf("forced install outcome = %#v", outcome)
	}
	if !strings.Contains(stderr.String(), "⚠ Family manually selected") {
		t.Fatalf("stderr omitted forced-override warning: %q", stderr.String())
	}
}

func TestCLIPartialFailurePrintsRanBeforeFatalAndKeepsJSONStdout(t *testing.T) {
	app := testApplication()
	env := app.environment.(*cliFakeEnvironment)
	env.runErrors = map[int]error{1: errors.New("simulated Add-Printer failure")}
	env.runOutputs = map[int]string{1: "PowerShell diagnostic"}
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"install", "192.0.2.10", "--yes"}, strings.NewReader(""), &stdout, &stderr, app)
	if code != int(install.ExitGeneralError) {
		t.Fatalf("run() code = %d, want %d", code, install.ExitGeneralError)
	}
	assertValidJSON(t, stdout.Bytes())
	for _, fragment := range []string{"Attempted commands before failure:", "PowerShell diagnostic", "Errored: true", "simulated Add-Printer failure"} {
		if !strings.Contains(stderr.String(), fragment) {
			t.Fatalf("stderr %q does not contain %q", stderr.String(), fragment)
		}
	}
	var outcome install.Outcome
	if err := json.Unmarshal(stdout.Bytes(), &outcome); err != nil {
		t.Fatalf("decode outcome: %v", err)
	}
	if outcome.Result == nil || len(outcome.Result.Ran) != 2 {
		t.Fatalf("JSON outcome lost attempted commands: %#v", outcome)
	}
}

func TestWhatIfAliasTakesPrecedenceOverYesAndNeverMutates(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "install", args: []string{"install", "192.0.2.10", "--what-if", "--yes"}},
		{name: "uninstall", args: []string{"uninstall", "Test Printer", "--what-if", "--yes"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app := testApplication()
			env := app.environment.(*cliFakeEnvironment)
			var stdout, stderr bytes.Buffer
			code := run(context.Background(), tt.args, strings.NewReader("y\n"), &stdout, &stderr, app)
			if code != int(install.ExitSuccess) {
				t.Fatalf("run() code = %d; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
			}
			if len(env.ran) != 0 {
				t.Fatalf("--what-if --yes executed mutating commands: %#v", env.ran)
			}
			if strings.Contains(stderr.String(), "Proceed?") {
				t.Fatalf("--what-if prompted for confirmation: %q", stderr.String())
			}
			var outcome install.Outcome
			if err := json.Unmarshal(stdout.Bytes(), &outcome); err != nil || !outcome.DryRun || outcome.Confirmed {
				t.Fatalf("dry-run outcome = %#v, decode error %v", outcome, err)
			}
		})
	}
}

func TestUsageAndGeneralErrorsStillWriteJSON(t *testing.T) {
	tests := []struct {
		name string
		args []string
		app  application
		code int
	}{
		{name: "usage", args: []string{"unknown"}, app: testApplication(), code: int(install.ExitUsageError)},
		{name: "general", args: []string{"catalog", "probe", "bad"}, app: application{
			collect: func(context.Context, string) (probe.Result, error) { return probe.Result{}, errors.New("probe failed") },
		}, code: int(install.ExitGeneralError)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if got := run(context.Background(), tt.args, strings.NewReader(""), &stdout, &stderr, tt.app); got != tt.code {
				t.Fatalf("run() code = %d, want %d", got, tt.code)
			}
			assertValidJSON(t, stdout.Bytes())
		})
	}
}

func testApplication() application {
	family := catalog.Family{ID: "test-family", Manufacturer: "Test", Aliases: []string{"Test Model"}}
	driver := catalog.DriverPackage{
		FamilyID:          family.ID,
		Name:              "Test package",
		WindowsDriverName: "Verified Windows Driver",
		Source:            "test",
		Strategy:          "test",
	}
	w := install.Workflow{
		Collect: func(context.Context, string) (probe.Result, error) {
			return probe.Result{Evidence: evidence.Evidence{IP: "192.0.2.10", Provenance: "captured"}}, nil
		},
		Resolve: func(evidence.Evidence) catalog.ResolutionResult {
			familyCopy, driverCopy := family, driver
			return catalog.ResolutionResult{NormalizedModel: "Test Model", Family: &familyCopy, Driver: &driverCopy, Confidence: 1}
		},
		Families:  func() []catalog.Family { return []catalog.Family{family} },
		DriverFor: func(id string) (catalog.DriverPackage, bool) { return driver, id == family.ID },
	}
	env := &cliFakeEnvironment{
		elevated:      true,
		driverPresent: true,
		configuration: install.PrinterConfiguration{PrinterName: "Test Printer", PortName: "SpoolSmith-192.0.2.10", DriverName: "Verified Windows Driver"},
	}
	return application{
		workflow:    w,
		environment: env,
		collect: func(context.Context, string) (probe.Result, error) {
			return probe.Result{Evidence: evidence.Evidence{IP: "192.0.2.10", Provenance: "captured"}}, nil
		},
	}
}

type cliFakeEnvironment struct {
	elevated      bool
	driverPresent bool
	configuration install.PrinterConfiguration
	ran           []string
	runErrors     map[int]error
	runOutputs    map[int]string
}

func (f *cliFakeEnvironment) IsElevated(context.Context) (bool, error) {
	return f.elevated, nil
}

func (f *cliFakeEnvironment) DriverPresent(context.Context, string) (bool, error) {
	return f.driverPresent, nil
}

func (f *cliFakeEnvironment) Run(_ context.Context, command string) (string, error) {
	f.ran = append(f.ran, command)
	index := len(f.ran) - 1
	return f.runOutputs[index], f.runErrors[index]
}

func (f *cliFakeEnvironment) LookupPrinter(context.Context, string) (install.PrinterConfiguration, error) {
	return f.configuration, nil
}

func assertValidJSON(t *testing.T, data []byte) {
	t.Helper()
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatalf("stdout is not valid JSON: %v; %q", err, data)
	}
}
