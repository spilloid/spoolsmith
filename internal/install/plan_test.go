package install

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/spilloid/spoolsmith/internal/catalog"
)

func TestBuildPlanCommandsForBothFamilies(t *testing.T) {
	tests := []struct {
		name       string
		familyID   string
		model      string
		ip         string
		wantDriver string
	}{
		{
			name:       "HP LaserJet Pro M4xx",
			familyID:   "hp-laserjet-m4xx",
			model:      "HP LaserJet Pro M404dn",
			ip:         "192.0.2.10",
			wantDriver: "HP Universal Print Driver for Windows PCL 6",
		},
		{
			name:       "Brother HL-L2xxx",
			familyID:   "brother-hl-l2xxx",
			model:      "Brother HL-L2350DW",
			ip:         "198.51.100.25",
			wantDriver: "Brother model-specific Full Driver & Software Package",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolution := resolvedResult(t, tt.familyID, tt.model)
			got, err := BuildPlan(tt.ip, resolution)
			if err != nil {
				t.Fatalf("BuildPlan() error = %v", err)
			}
			if got.IPAddress != tt.ip || got.PortName != "SpoolSmith-"+tt.ip || got.PrinterName != tt.model || got.DriverName != tt.wantDriver {
				t.Fatalf("BuildPlan() metadata = %#v", got)
			}
			if got.Family.ID != tt.familyID || got.Driver.FamilyID != tt.familyID {
				t.Fatalf("BuildPlan() catalog data = Family %#v, Driver %#v", got.Family, got.Driver)
			}
			if len(got.Commands) != 2 || !strings.Contains(got.Commands[0], "Get-Printer -ErrorAction Stop") || !strings.Contains(got.Commands[1], "Add-Printer -Name") {
				t.Fatalf("BuildPlan() missing guarded commands = %#v", got.Commands)
			}
		})
	}
}

func TestBuildPlanFailsOnUnresolvedInput(t *testing.T) {
	fullyResolved := resolvedResult(t, "hp-laserjet-m4xx", "HP LaserJet Pro M404dn")
	tests := []struct {
		name       string
		resolution catalog.ResolutionResult
	}{
		{name: "both nil", resolution: catalog.ResolutionResult{Uncertain: []string{"unknown"}}},
		{name: "family nil", resolution: catalog.ResolutionResult{NormalizedModel: fullyResolved.NormalizedModel, Driver: fullyResolved.Driver}},
		{name: "driver nil", resolution: catalog.ResolutionResult{NormalizedModel: fullyResolved.NormalizedModel, Family: fullyResolved.Family}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := BuildPlan("192.0.2.10", tt.resolution); err == nil {
				t.Fatal("BuildPlan() error = nil, want unresolved-input error")
			}
		})
	}
}

func TestInstallFailsClosedWhenNotElevated(t *testing.T) {
	env := &fakeEnvironment{elevated: false, driverPresent: true}
	plan := mustPlan(t)

	result, err := Install(context.Background(), env, plan, true)
	if !errors.Is(err, ErrNotElevated) {
		t.Fatalf("Install() error = %v, want %v", err, ErrNotElevated)
	}
	if len(result.Ran) != 0 || len(env.ran) != 0 {
		t.Fatalf("Install() ran commands while not elevated: result=%#v env=%#v", result.Ran, env.ran)
	}
	if env.driverChecks != 0 {
		t.Fatalf("DriverPresent() calls = %d, want 0 after elevation failure", env.driverChecks)
	}
}

func TestInstallFailsClosedWhenDriverNotPresent(t *testing.T) {
	env := &fakeEnvironment{elevated: true, driverPresent: false}
	plan := mustPlan(t)

	result, err := Install(context.Background(), env, plan, true)
	if !errors.Is(err, ErrDriverNotPresent) {
		t.Fatalf("Install() error = %v, want %v", err, ErrDriverNotPresent)
	}
	if len(result.Ran) != 0 || len(env.ran) != 0 {
		t.Fatalf("Install() ran commands without a driver: result=%#v env=%#v", result.Ran, env.ran)
	}
	if env.driverChecks != 1 || env.checkedDriver != plan.DriverName {
		t.Fatalf("DriverPresent() calls/name = %d/%q, want 1/%q", env.driverChecks, env.checkedDriver, plan.DriverName)
	}
}

func TestInstallDoesNotRunUnlessConfirmed(t *testing.T) {
	env := &fakeEnvironment{elevated: true, driverPresent: true}
	plan := mustPlan(t)

	result, err := Install(context.Background(), env, plan, false)
	if !errors.Is(err, ErrNotConfirmed) {
		t.Fatalf("Install() error = %v, want %v", err, ErrNotConfirmed)
	}
	if len(result.Ran) != 0 || len(env.ran) != 0 {
		t.Fatalf("Install() ran unconfirmed commands: result=%#v env=%#v", result.Ran, env.ran)
	}
	if env.elevationChecks != 1 || env.driverChecks != 1 {
		t.Fatalf("preflight calls = elevation %d, driver %d; want 1 each", env.elevationChecks, env.driverChecks)
	}
}

func TestInstallRunsPlanCommandsInOrderWhenConfirmed(t *testing.T) {
	env := &fakeEnvironment{elevated: true, driverPresent: true}
	plan := mustPlan(t)

	result, err := Install(context.Background(), env, plan, true)
	if err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	if !reflect.DeepEqual(env.ran, plan.Commands) {
		t.Fatalf("Run() commands = %#v, want %#v", env.ran, plan.Commands)
	}
	if len(result.Ran) != len(plan.Commands) {
		t.Fatalf("len(Result.Ran) = %d, want %d", len(result.Ran), len(plan.Commands))
	}
	for i, commandResult := range result.Ran {
		if commandResult.Command != plan.Commands[i] || commandResult.Output != "output: "+plan.Commands[i] || commandResult.Err != nil {
			t.Fatalf("Result.Ran[%d] = %#v", i, commandResult)
		}
	}
}

func TestUninstallRemovesPrinterAndPortBeforeOptionalDriver(t *testing.T) {
	env := &fakeEnvironment{elevated: true}
	result, err := Uninstall(context.Background(), env, "HP LaserJet Pro M404dn", "SpoolSmith-192.0.2.10", "HP Universal Print Driver for Windows PCL 6", true)
	if err != nil {
		t.Fatalf("Uninstall() error = %v", err)
	}
	if len(env.ran) != 3 || !reflect.DeepEqual(env.ran, result.Plan.Commands) || !strings.Contains(env.ran[0], "Remove-Printer -InputObject") || !strings.Contains(env.ran[1], "Retained shared port") || !strings.Contains(env.ran[2], "Retained shared driver") {
		t.Fatalf("Uninstall() missing ordered, guarded removal commands = %#v / %#v", env.ran, result.Plan.Commands)
	}
}

func TestPowerShellStringEscapesSmartQuoteInjection(t *testing.T) {
	injection := "safe\u201d; Remove-Printer -Name \u2018owned\u2019; \u201c"
	want := "\"safe`\u201d; Remove-Printer -Name `\u2018owned`\u2019; `\u201c\""
	if got := powerShellString(injection); got != want {
		t.Fatalf("powerShellString() = %q, want %q", got, want)
	}
}

func TestGeneratedCmdletsStopAndTranslateNonTerminatingErrors(t *testing.T) {
	plan := mustPlan(t)
	uninstall, err := BuildUninstallPlan("printer", "port", "driver", true)
	if err != nil {
		t.Fatalf("BuildUninstallPlan() error = %v", err)
	}
	driverCommand, err := driverPresenceCommand("driver")
	if err != nil {
		t.Fatalf("driverPresenceCommand() error = %v", err)
	}
	lookupCommand, err := lookupPrinterCommand("printer")
	if err != nil {
		t.Fatalf("lookupPrinterCommand() error = %v", err)
	}
	commands := append(append([]string{}, plan.Commands...), uninstall.Commands...)
	commands = append(commands, driverCommand, lookupCommand)
	for _, command := range commands {
		for _, fragment := range []string{
			"$ErrorActionPreference = 'Stop'; try { ",
			"-ErrorAction Stop",
			" } catch { Write-Error $_.Exception.Message; exit 1 }",
		} {
			if !strings.Contains(command, fragment) {
				t.Errorf("command %q does not contain %q", command, fragment)
			}
		}
	}
}

func TestDriverPresentPreservesInfrastructureError(t *testing.T) {
	wantErr := errors.New("access denied while loading PrintManagement")
	called := false
	present, err := checkDriverPresent(context.Background(), func(_ context.Context, command string) (string, error) {
		called = true
		if !strings.Contains(command, "Get-PrinterDriver -ErrorAction Stop") || !strings.Contains(command, "exit 1") {
			t.Fatalf("driver check command is not fail-closed: %q", command)
		}
		return "simulated PowerShell error record", wantErr
	}, "Verified Driver")
	if !called || present || !errors.Is(err, wantErr) {
		t.Fatalf("checkDriverPresent() = (%t, %v), called=%t; want (false, preserved error, true)", present, err, called)
	}
	if !strings.Contains(err.Error(), "simulated PowerShell error record") {
		t.Fatalf("checkDriverPresent() discarded captured PowerShell diagnostic: %v", err)
	}
}

func TestDriverPresentReportsCleanAbsenceWithoutError(t *testing.T) {
	present, err := checkDriverPresent(context.Background(), func(context.Context, string) (string, error) {
		return "False\r\n", nil
	}, "Verified Driver")
	if err != nil || present {
		t.Fatalf("checkDriverPresent() = (%t, %v), want (false, nil)", present, err)
	}
}

func TestPreflightSurfacesDriverCheckErrorWithGuidance(t *testing.T) {
	driverErr := errors.New("Print Spooler unavailable")
	env := &fakeEnvironment{elevated: true, driverErr: driverErr}
	result, err := Preflight(context.Background(), env, mustPlan(t))
	if !errors.Is(err, driverErr) || errors.Is(err, ErrDriverNotPresent) {
		t.Fatalf("Preflight() error = %v, want actual infrastructure error distinct from ErrDriverNotPresent", err)
	}
	if !strings.Contains(err.Error(), "Print Spooler unavailable") || !strings.Contains(err.Error(), "install it via Windows Update") {
		t.Fatalf("Preflight() error does not contain real error and guidance: %v", err)
	}
	if !result.Elevated || !result.DriverChecked || result.DriverPresent {
		t.Fatalf("Preflight() result = %#v", result)
	}
}

func TestPlanConstructionRejectsControlCharacters(t *testing.T) {
	tests := []struct {
		name  string
		value string
	}{
		{name: "NUL", value: "printer\x00name"},
		{name: "newline", value: "printer\nname"},
		{name: "carriage return", value: "printer\rname"},
		{name: "bidi override", value: "printer\u202ename"},
		{name: "bidi isolate", value: "printer\u2066name"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := BuildUninstallPlan(tt.value, "port", "driver", false); err == nil || !strings.Contains(err.Error(), "prohibited") {
				t.Fatalf("BuildUninstallPlan() error = %v, want prohibited control error", err)
			}
		})
	}
}

func TestLookupPrinterRejectsNULBeforeEnvironmentCall(t *testing.T) {
	env := &fakeEnvironment{}
	if _, err := LookupPrinter(context.Background(), env, "printer\x00name"); err == nil {
		t.Fatal("LookupPrinter() error = nil, want NUL validation error")
	}
	if env.lookupCalls != 0 {
		t.Fatalf("LookupPrinter() reached environment %d times, want 0", env.lookupCalls)
	}
}

func TestBuildPlanFailsClosedWithoutVerifiedWindowsDriverName(t *testing.T) {
	resolution := resolvedResult(t, "hp-laserjet-m4xx", "HP LaserJet Pro M404dn")
	resolution.Driver.WindowsDriverName = ""
	_, err := BuildPlan("192.0.2.10", resolution)
	if err == nil || !strings.Contains(err.Error(), "Windows driver name not yet verified for this family") || !strings.Contains(err.Error(), "Get-PrinterDriver") {
		t.Fatalf("BuildPlan() error = %v, want specific real-hardware verification guidance", err)
	}
}

func TestCommandResultJSONIncludesErrorText(t *testing.T) {
	encoded, err := json.Marshal(CommandResult{Command: "command", Output: "diagnostic", Err: errors.New("failed")})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	for _, value := range []string{"command", "diagnostic", "failed"} {
		if !strings.Contains(string(encoded), value) {
			t.Fatalf("encoded command result %s does not contain %q", encoded, value)
		}
	}
}

type fakeEnvironment struct {
	elevated        bool
	elevationErr    error
	driverPresent   bool
	driverErr       error
	elevationChecks int
	driverChecks    int
	checkedDriver   string
	ran             []string
	runErrors       map[int]error
	runOutputs      map[int]string
	configuration   PrinterConfiguration
	lookupErr       error
	lookupCalls     int
}

func (f *fakeEnvironment) IsElevated(context.Context) (bool, error) {
	f.elevationChecks++
	return f.elevated, f.elevationErr
}

func (f *fakeEnvironment) DriverPresent(_ context.Context, driverName string) (bool, error) {
	f.driverChecks++
	f.checkedDriver = driverName
	return f.driverPresent, f.driverErr
}

func (f *fakeEnvironment) Run(_ context.Context, command string) (string, error) {
	f.ran = append(f.ran, command)
	index := len(f.ran) - 1
	if err := f.runErrors[index]; err != nil {
		return f.runOutputs[index], err
	}
	return "output: " + command, nil
}

func (f *fakeEnvironment) LookupPrinter(context.Context, string) (PrinterConfiguration, error) {
	f.lookupCalls++
	return f.configuration, f.lookupErr
}

func mustPlan(t *testing.T) Plan {
	t.Helper()
	plan, err := BuildPlan("192.0.2.10", resolvedResult(t, "hp-laserjet-m4xx", "HP LaserJet Pro M404dn"))
	if err != nil {
		t.Fatalf("BuildPlan() error = %v", err)
	}
	return plan
}

func resolvedResult(t *testing.T, familyID, model string) catalog.ResolutionResult {
	t.Helper()
	var family *catalog.Family
	for _, candidate := range catalog.Families() {
		if candidate.ID == familyID {
			candidate := candidate
			family = &candidate
			break
		}
	}
	if family == nil {
		t.Fatalf("catalog family %q not found", familyID)
	}
	driver, ok := catalog.DriverFor(familyID)
	if !ok {
		t.Fatalf("catalog driver %q not found", familyID)
	}
	driver.WindowsDriverName = driver.Name
	return catalog.ResolutionResult{
		NormalizedModel: model,
		Family:          family,
		Driver:          &driver,
		Confidence:      1,
	}
}

func wrappedForTest(command string) string {
	return "$ErrorActionPreference = 'Stop'; try { " + command + " } catch { Write-Error $_.Exception.Message; exit 1 }"
}
