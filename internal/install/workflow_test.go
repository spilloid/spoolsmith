package install

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/spilloid/spoolsmith/internal/catalog"
	"github.com/spilloid/spoolsmith/internal/evidence"
	"github.com/spilloid/spoolsmith/internal/probe"
)

func TestForceFamilyValidAndInvalid(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		env := workflowEnvironment(true, true)
		w := testWorkflow(true)
		w.Resolve = func(evidence.Evidence) catalog.ResolutionResult {
			panic("--force-family must skip catalog.Resolve")
		}
		outcome, code := w.RunInstall(context.Background(), env, panicReader{}, io.Discard, false, InstallOptions{
			Target:      "192.0.2.10",
			ForceFamily: "test-family",
			DryRun:      true,
			Yes:         true,
		})
		if code != ExitSuccess || outcome.Plan == nil || !outcome.Plan.ForcedOverride || outcome.Resolution != "forced-override" {
			t.Fatalf("RunInstall() = code %d, outcome %#v", code, outcome)
		}
		if len(env.ran) != 0 {
			t.Fatalf("dry-run executed %d mutating commands", len(env.ran))
		}
	})

	t.Run("invalid", func(t *testing.T) {
		collectCalls := 0
		w := testWorkflow(true)
		w.Collect = func(context.Context, string) (probe.Result, error) {
			collectCalls++
			return probe.Result{}, nil
		}
		outcome, code := w.RunInstall(context.Background(), workflowEnvironment(true, true), strings.NewReader(""), io.Discard, false, InstallOptions{
			Target:      "192.0.2.10",
			ForceFamily: "missing",
		})
		if code != ExitUsageError || !strings.Contains(outcome.Error, "unknown forced family") {
			t.Fatalf("RunInstall() = code %d, outcome %#v", code, outcome)
		}
		if collectCalls != 0 {
			t.Fatalf("invalid override ran detection %d times, want 0", collectCalls)
		}
	})
}

func TestSelectFamilyAbortAndSelection(t *testing.T) {
	families := testWorkflow(true).Families()
	tests := []struct {
		name       string
		input      string
		wantChosen bool
		wantID     string
	}{
		{name: "abort", input: "a\n"},
		{name: "invalid", input: "99\n"},
		{name: "select", input: "1\n", wantChosen: true, wantID: "test-family"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var interactive bytes.Buffer
			family, chosen, err := SelectFamily(strings.NewReader(tt.input), &interactive, []string{"no exact match"}, families)
			if err != nil || chosen != tt.wantChosen || family.ID != tt.wantID {
				t.Fatalf("SelectFamily() = (%#v, %t, %v)", family, chosen, err)
			}
			if !strings.Contains(interactive.String(), "no exact match") || !strings.Contains(interactive.String(), "Select a family by number, or 'a' to abort:") {
				t.Fatalf("picker output = %q", interactive.String())
			}
		})
	}
}

func TestInteractivePickerSelectionContinuesToConfirmation(t *testing.T) {
	w := testWorkflow(false)
	env := workflowEnvironment(true, true)
	var interactive bytes.Buffer
	outcome, code := w.RunInstall(context.Background(), env, strings.NewReader("1\ny\n"), &interactive, true, InstallOptions{Target: "192.0.2.10"})
	if code != ExitSuccess || outcome.Status != "success" || !outcome.Confirmed || outcome.Plan == nil || !outcome.Plan.ForcedOverride {
		t.Fatalf("RunInstall() = code %d, outcome %#v", code, outcome)
	}
	for _, fragment := range []string{"Select a family", "⚠ Family manually selected", "Proceed? [y/N]:"} {
		if !strings.Contains(interactive.String(), fragment) {
			t.Fatalf("interactive output %q does not contain %q", interactive.String(), fragment)
		}
	}
}

func TestInteractivePickerAbortExitsNotConfirmed(t *testing.T) {
	env := workflowEnvironment(true, true)
	outcome, code := testWorkflow(false).RunInstall(context.Background(), env, strings.NewReader("a\n"), io.Discard, true, InstallOptions{Target: "192.0.2.10"})
	if code != ExitNotConfirmed || outcome.Status != "not-confirmed" || len(env.ran) != 0 {
		t.Fatalf("RunInstall() = code %d, outcome %#v, ran %#v", code, outcome, env.ran)
	}
}

func TestInstallDryRunNeverMutatesInAnyPreflightOutcome(t *testing.T) {
	tests := []struct {
		name          string
		elevated      bool
		driverPresent bool
		wantCode      ExitCode
	}{
		{name: "elevated and present", elevated: true, driverPresent: true, wantCode: ExitSuccess},
		{name: "not elevated", elevated: false, driverPresent: true, wantCode: ExitPreflight},
		{name: "driver absent", elevated: true, driverPresent: false, wantCode: ExitPreflight},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := workflowEnvironment(tt.elevated, tt.driverPresent)
			var interactive bytes.Buffer
			outcome, code := testWorkflow(true).RunInstall(context.Background(), env, panicReader{}, &interactive, true, InstallOptions{
				Target: "192.0.2.10",
				DryRun: true,
				Yes:    true,
			})
			if code != tt.wantCode {
				t.Fatalf("RunInstall() code = %d, want %d; outcome %#v", code, tt.wantCode, outcome)
			}
			if len(env.ran) != 0 {
				t.Fatalf("dry-run executed mutating commands: %#v", env.ran)
			}
			if strings.Contains(interactive.String(), "Proceed?") {
				t.Fatalf("dry-run prompted for confirmation: %q", interactive.String())
			}
		})
	}
}

func TestUninstallDryRunNeverMutatesInAnyPreflightOutcome(t *testing.T) {
	tests := []struct {
		name          string
		elevated      bool
		driverPresent bool
		wantCode      ExitCode
	}{
		{name: "elevated and present", elevated: true, driverPresent: true, wantCode: ExitSuccess},
		{name: "not elevated", elevated: false, driverPresent: true, wantCode: ExitPreflight},
		{name: "driver absent is irrelevant to removal", elevated: true, driverPresent: false, wantCode: ExitSuccess},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := workflowEnvironment(tt.elevated, tt.driverPresent)
			var interactive bytes.Buffer
			outcome, code := testWorkflow(true).RunUninstall(context.Background(), env, panicReader{}, &interactive, true, UninstallOptions{
				PrinterName: "Test Printer",
				DryRun:      true,
				Yes:         true,
			})
			if code != tt.wantCode {
				t.Fatalf("RunUninstall() code = %d, want %d; outcome %#v", code, tt.wantCode, outcome)
			}
			if len(env.ran) != 0 {
				t.Fatalf("dry-run executed mutating commands: %#v", env.ran)
			}
			if strings.Contains(interactive.String(), "Proceed?") {
				t.Fatalf("dry-run prompted for confirmation: %q", interactive.String())
			}
		})
	}
}

func TestInstallExitCodeContract(t *testing.T) {
	tests := []struct {
		name       string
		workflow   Workflow
		env        *fakeEnvironment
		input      string
		terminal   bool
		options    InstallOptions
		wantCode   ExitCode
		wantStatus string
	}{
		{name: "unresolved", workflow: testWorkflow(false), env: workflowEnvironment(true, true), options: InstallOptions{Target: "192.0.2.10", NonInteractive: true}, wantCode: ExitUnresolved, wantStatus: "error"},
		{name: "preflight failed", workflow: testWorkflow(true), env: workflowEnvironment(false, true), options: InstallOptions{Target: "192.0.2.10", Yes: true}, wantCode: ExitPreflight, wantStatus: "error"},
		{name: "not confirmed", workflow: testWorkflow(true), env: workflowEnvironment(true, true), input: "n\n", terminal: true, options: InstallOptions{Target: "192.0.2.10"}, wantCode: ExitNotConfirmed, wantStatus: "not-confirmed"},
		{name: "dry run", workflow: testWorkflow(true), env: workflowEnvironment(true, true), options: InstallOptions{Target: "192.0.2.10", DryRun: true}, wantCode: ExitSuccess, wantStatus: "dry-run"},
		{name: "success", workflow: testWorkflow(true), env: workflowEnvironment(true, true), options: InstallOptions{Target: "192.0.2.10", Yes: true}, wantCode: ExitSuccess, wantStatus: "success"},
		{name: "execution error", workflow: testWorkflow(true), env: environmentFailingCommand(0), options: InstallOptions{Target: "192.0.2.10", Yes: true}, wantCode: ExitGeneralError, wantStatus: "error"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			outcome, code := tt.workflow.RunInstall(context.Background(), tt.env, strings.NewReader(tt.input), io.Discard, tt.terminal, tt.options)
			if code != tt.wantCode || outcome.Status != tt.wantStatus {
				t.Fatalf("RunInstall() = code %d, status %q; want %d/%q; outcome %#v", code, outcome.Status, tt.wantCode, tt.wantStatus, outcome)
			}
		})
	}
}

func TestNonInteractiveFlagsNeverReadInstallInput(t *testing.T) {
	tests := []struct {
		name     string
		options  InstallOptions
		resolved bool
		wantCode ExitCode
	}{
		{name: "yes unresolved", options: InstallOptions{Target: "192.0.2.10", Yes: true}, wantCode: ExitUnresolved},
		{name: "json unresolved", options: InstallOptions{Target: "192.0.2.10", JSON: true}, wantCode: ExitUnresolved},
		{name: "non-interactive unresolved", options: InstallOptions{Target: "192.0.2.10", NonInteractive: true}, wantCode: ExitUnresolved},
		{name: "json resolved", options: InstallOptions{Target: "192.0.2.10", JSON: true}, resolved: true, wantCode: ExitNotConfirmed},
		{name: "non-interactive resolved", options: InstallOptions{Target: "192.0.2.10", NonInteractive: true}, resolved: true, wantCode: ExitNotConfirmed},
		{name: "yes resolved", options: InstallOptions{Target: "192.0.2.10", Yes: true}, resolved: true, wantCode: ExitSuccess},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, code := testWorkflow(tt.resolved).RunInstall(context.Background(), workflowEnvironment(true, true), panicReader{}, io.Discard, true, tt.options)
			if code != tt.wantCode {
				t.Fatalf("RunInstall() code = %d, want %d", code, tt.wantCode)
			}
		})
	}
}

func TestNonInteractiveFlagsNeverReadUninstallInput(t *testing.T) {
	tests := []struct {
		name     string
		options  UninstallOptions
		wantCode ExitCode
	}{
		{name: "json", options: UninstallOptions{PrinterName: "Test Printer", JSON: true}, wantCode: ExitNotConfirmed},
		{name: "non-interactive", options: UninstallOptions{PrinterName: "Test Printer", NonInteractive: true}, wantCode: ExitNotConfirmed},
		{name: "yes", options: UninstallOptions{PrinterName: "Test Printer", Yes: true}, wantCode: ExitSuccess},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, code := testWorkflow(true).RunUninstall(context.Background(), workflowEnvironment(true, true), panicReader{}, io.Discard, true, tt.options)
			if code != tt.wantCode {
				t.Fatalf("RunUninstall() code = %d, want %d", code, tt.wantCode)
			}
		})
	}
}

func TestUninstallExitCodeContract(t *testing.T) {
	tests := []struct {
		name       string
		env        *fakeEnvironment
		input      string
		terminal   bool
		options    UninstallOptions
		wantCode   ExitCode
		wantStatus string
	}{
		{name: "preflight failed", env: workflowEnvironment(false, true), options: UninstallOptions{PrinterName: "Test Printer", Yes: true}, wantCode: ExitPreflight, wantStatus: "error"},
		{name: "not confirmed", env: workflowEnvironment(true, true), input: "n\n", terminal: true, options: UninstallOptions{PrinterName: "Test Printer"}, wantCode: ExitNotConfirmed, wantStatus: "not-confirmed"},
		{name: "dry run", env: workflowEnvironment(true, true), options: UninstallOptions{PrinterName: "Test Printer", DryRun: true}, wantCode: ExitSuccess, wantStatus: "dry-run"},
		{name: "success", env: workflowEnvironment(true, true), options: UninstallOptions{PrinterName: "Test Printer", Yes: true}, wantCode: ExitSuccess, wantStatus: "success"},
		{name: "execution error", env: environmentFailingCommand(0), options: UninstallOptions{PrinterName: "Test Printer", Yes: true}, wantCode: ExitGeneralError, wantStatus: "error"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			outcome, code := testWorkflow(true).RunUninstall(context.Background(), tt.env, strings.NewReader(tt.input), io.Discard, tt.terminal, tt.options)
			if code != tt.wantCode || outcome.Status != tt.wantStatus {
				t.Fatalf("RunUninstall() = code %d, status %q; want %d/%q; outcome %#v", code, outcome.Status, tt.wantCode, tt.wantStatus, outcome)
			}
		})
	}
}

func TestPartialFailureResultsReachInteractiveOutput(t *testing.T) {
	env := environmentFailingCommand(1)
	env.runOutputs = map[int]string{1: "Add-Printer diagnostic"}
	var interactive bytes.Buffer
	outcome, code := testWorkflow(true).RunInstall(context.Background(), env, strings.NewReader(""), &interactive, false, InstallOptions{Target: "192.0.2.10", Yes: true})
	if code != ExitGeneralError || outcome.Result == nil || len(outcome.Result.Ran) != 2 {
		t.Fatalf("RunInstall() = code %d, outcome %#v", code, outcome)
	}
	for _, fragment := range []string{
		"Attempted commands before failure:",
		outcome.Result.Ran[0].Command,
		outcome.Result.Ran[1].Command,
		"Add-Printer diagnostic",
		"Errored: true",
	} {
		if !strings.Contains(interactive.String(), fragment) {
			t.Fatalf("interactive output %q does not contain %q", interactive.String(), fragment)
		}
	}
}

type panicReader struct{}

func (panicReader) Read([]byte) (int, error) {
	panic("dry-run must not read confirmation input")
}

func testWorkflow(resolved bool) Workflow {
	family := catalog.Family{ID: "test-family", Manufacturer: "Test", Aliases: []string{"Test Model"}}
	driver := catalog.DriverPackage{
		FamilyID:          family.ID,
		Name:              "Test package label",
		WindowsDriverName: "Verified Windows Driver",
		Source:            "test",
		Strategy:          "test",
	}
	return Workflow{
		Collect: func(context.Context, string) (probe.Result, error) {
			return probe.Result{Evidence: evidence.Evidence{IP: "192.0.2.10", Provenance: "captured"}}, nil
		},
		Resolve: func(evidence.Evidence) catalog.ResolutionResult {
			if !resolved {
				return catalog.ResolutionResult{Uncertain: []string{"no exact match"}}
			}
			familyCopy, driverCopy := family, driver
			return catalog.ResolutionResult{
				NormalizedModel: "Test Model",
				Family:          &familyCopy,
				Driver:          &driverCopy,
				Confidence:      1,
			}
		},
		Families: func() []catalog.Family {
			return []catalog.Family{family}
		},
		DriverFor: func(id string) (catalog.DriverPackage, bool) {
			return driver, id == family.ID
		},
	}
}

func workflowEnvironment(elevated, driverPresent bool) *fakeEnvironment {
	return &fakeEnvironment{
		elevated:      elevated,
		driverPresent: driverPresent,
		configuration: PrinterConfiguration{
			PrinterName: "Test Printer",
			PortName:    "SpoolSmith-192.0.2.10",
			DriverName:  "Verified Windows Driver",
		},
	}
}

func environmentFailingCommand(index int) *fakeEnvironment {
	env := workflowEnvironment(true, true)
	env.runErrors = map[int]error{index: errors.New("simulated command failure")}
	return env
}
