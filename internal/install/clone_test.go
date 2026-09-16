package install

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/spilloid/spoolsmith/internal/evidence"
	"github.com/spilloid/spoolsmith/internal/probe"
)

// cloneFakeEnvironment adds the optional clone capabilities to the suite's
// existing fake, so the base fake stays valid for every other test.
type cloneFakeEnvironment struct {
	*fakeEnvironment
	port      PortConfiguration
	portErr   error
	export    DriverExport
	exportErr error
}

func (f *cloneFakeEnvironment) LookupPort(context.Context, string) (PortConfiguration, error) {
	return f.port, f.portErr
}

func (f *cloneFakeEnvironment) ExportDriver(context.Context, string, string) (DriverExport, error) {
	return f.export, f.exportErr
}

func cloneEnv(port PortConfiguration) *cloneFakeEnvironment {
	return &cloneFakeEnvironment{fakeEnvironment: workflowEnvironment(true, true), port: port}
}

func TestCloneQueueReadsInstalledSetup(t *testing.T) {
	env := cloneEnv(PortConfiguration{PortName: "SpoolSmith-192.0.2.10", HostAddress: "192.0.2.10", PortNumber: 9100, Protocol: 1})
	cloned, err := CloneQueue(context.Background(), env, "Test Printer")
	if err != nil {
		t.Fatal(err)
	}
	if cloned.DriverName != "Verified Windows Driver" || cloned.HostAddress != "192.0.2.10" || cloned.PrinterName != "Test Printer" {
		t.Fatalf("CloneQueue() = %#v", cloned)
	}
}

func TestCloneQueueFailsClosedOnQueuesItCannotReproduce(t *testing.T) {
	tests := []struct {
		name string
		port PortConfiguration
		want string
	}{
		{
			name: "LPR protocol",
			port: PortConfiguration{PortName: "IP_192.0.2.10", HostAddress: "192.0.2.10", PortNumber: 515, Protocol: 2},
			want: "not RAW",
		},
		{
			name: "non-default TCP port",
			port: PortConfiguration{PortName: "IP_192.0.2.10", HostAddress: "192.0.2.10", PortNumber: 9101, Protocol: 1},
			want: "only maps the RAW 9100 default",
		},
		{
			name: "host name rather than address",
			port: PortConfiguration{PortName: "IP_printer.local", HostAddress: "printer.local", PortNumber: 9100, Protocol: 1},
			want: "host name rather than a literal IP",
		},
		{
			name: "no host address",
			port: PortConfiguration{PortName: "LPT1:", PortNumber: 9100, Protocol: 1},
			want: "not a standard TCP/IP port",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := CloneQueue(context.Background(), cloneEnv(tt.port), "Test Printer")
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("CloneQueue() = %v, want an error containing %q", err, tt.want)
			}
		})
	}
}

func TestCloneCommandsRejectInjectionAttempts(t *testing.T) {
	if _, err := lookupPortCommand("port\u202ename"); err == nil {
		t.Fatal("lookupPortCommand() accepted a bidirectional control character")
	}
	if _, err := exportDriverCommand("driver\nname", "C:\\temp"); err == nil {
		t.Fatal("exportDriverCommand() accepted a control character")
	}
	command, err := exportDriverCommand("Brother \u201cHL\u201d series", "C:\\temp\\export")
	if err != nil {
		t.Fatal(err)
	}
	// Smart quotes are real string delimiters to PowerShell; they must be
	// escaped rather than passed through into a generated command.
	if strings.Contains(command, "\u201cHL\u201d") && !strings.Contains(command, "`\u201cHL`\u201d") {
		t.Fatalf("exportDriverCommand() did not escape smart quotes: %s", command)
	}
	if !strings.Contains(command, "pnputil.exe /export-driver") || !strings.Contains(command, "/enum-drivers") {
		t.Fatalf("exportDriverCommand() lost its export steps: %s", command)
	}
}

func TestFingerprintPlanIsStableAndChangeSensitive(t *testing.T) {
	plan := Plan{PrinterName: "Accounting", PortName: "SpoolSmith-192.0.2.10", DriverName: "D", IPAddress: "192.0.2.10", Commands: []string{"one", "two"}}
	first, err := FingerprintPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	second, err := FingerprintPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("FingerprintPlan() is not stable: %q vs %q", first, second)
	}
	changed := plan
	changed.Commands = []string{"one", "two; Remove-Item C:\\ -Recurse"}
	third, err := FingerprintPlan(changed)
	if err != nil {
		t.Fatal(err)
	}
	if third == first {
		t.Fatal("FingerprintPlan() did not change when a command changed")
	}
}

func TestBundleDriverCommandValidation(t *testing.T) {
	valid := BundleDriver{WindowsDriverName: "D", INF: "a.inf", StageDirName: "SpoolSmith-bundle-abc", PayloadDigest: "abc"}
	if _, err := bundleDriverCommand(valid); err != nil {
		t.Fatalf("bundleDriverCommand() = %v, want nil", err)
	}
	escaped := valid
	escaped.StageDirName = "../../windows"
	if _, err := bundleDriverCommand(escaped); err == nil {
		t.Fatal("bundleDriverCommand() accepted a staging directory with path separators")
	}
	notInf := valid
	notInf.INF = "driver.exe"
	if _, err := bundleDriverCommand(notInf); err == nil {
		t.Fatal("bundleDriverCommand() accepted a non-INF staging target")
	}
	command, err := bundleDriverCommand(valid)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Get-PrinterDriver", "Unchanged driver", "*.cat", "Get-AuthenticodeSignature", "pnputil.exe /add-driver", "Add-PrinterDriver"} {
		if !strings.Contains(command, want) {
			t.Fatalf("bundleDriverCommand() is missing %q: %s", want, command)
		}
	}
}

func TestPreflightAcceptsMissingDriverOnlyWithAPayload(t *testing.T) {
	plan := Plan{PrinterName: "Accounting", PortName: "P", DriverName: "D", Commands: []string{"c"}}
	if _, err := Preflight(context.Background(), workflowEnvironment(true, false), plan); err != ErrDriverNotPresent {
		t.Fatalf("Preflight() with no payload = %v, want ErrDriverNotPresent", err)
	}
	withPayload := plan
	withPayload.BundleDriver = &BundleDriver{WindowsDriverName: "D", INF: "a.inf", StageDirName: "SpoolSmith-bundle-abc", PayloadDigest: "abc"}
	if _, err := Preflight(context.Background(), workflowEnvironment(true, false), withPayload); err != nil {
		t.Fatalf("Preflight() with a bundle payload = %v, want nil", err)
	}
	mismatched := withPayload
	mismatched.BundleDriver = &BundleDriver{WindowsDriverName: "Other", INF: "a.inf", StageDirName: "SpoolSmith-bundle-abc", PayloadDigest: "abc"}
	if _, err := Preflight(context.Background(), workflowEnvironment(true, false), mismatched); err == nil {
		t.Fatal("Preflight() accepted a payload providing a different driver than the plan maps")
	}
}

// TestPlanHashConfirmation is the property scripted rollout depends on: a
// fingerprint authorizes exactly one plan, and any other plan stops.
func TestPlanHashConfirmation(t *testing.T) {
	profile := Profile{
		Version:     1,
		Target:      "192.0.2.10",
		PrinterName: "Accounting",
		DriverName:  "Verified Windows Driver",
		Evidence:    evidence.Evidence{IP: "192.0.2.10", Provenance: "captured", HTTPTitle: "Test", PJLID: "Test"},
	}
	options := func() InstallOptions {
		p := profile
		return InstallOptions{Profile: &p, NonInteractive: true}
	}
	// The live printer reports the same identity the profile captured, which
	// is what the profile path requires before it will build a plan.
	workflow := func() Workflow {
		w := testWorkflow(true)
		w.Collect = func(context.Context, string) (probe.Result, error) {
			return probe.Result{Evidence: profile.Evidence}, nil
		}
		return w
	}

	preview := options()
	preview.DryRun = true
	outcome, code := workflow().RunInstall(context.Background(), cloneEnv(PortConfiguration{}), panicReader{}, io.Discard, false, preview)
	if code != ExitSuccess || outcome.PlanHash == "" {
		t.Fatalf("preview = code %d, hash %q", code, outcome.PlanHash)
	}

	t.Run("matching fingerprint confirms without a prompt", func(t *testing.T) {
		env := cloneEnv(PortConfiguration{})
		apply := options()
		apply.ConfirmPlanHash = outcome.PlanHash
		got, code := workflow().RunInstall(context.Background(), env, panicReader{}, io.Discard, false, apply)
		if code != ExitSuccess || got.Status != "success" || !got.Confirmed {
			t.Fatalf("RunInstall() = code %d, outcome %#v", code, got)
		}
		if len(env.ran) == 0 {
			t.Fatal("a confirmed plan ran no commands")
		}
	})

	t.Run("wrong fingerprint stops before mutating", func(t *testing.T) {
		env := cloneEnv(PortConfiguration{})
		apply := options()
		apply.ConfirmPlanHash = strings.Repeat("0", 64)
		got, code := workflow().RunInstall(context.Background(), env, panicReader{}, io.Discard, false, apply)
		if code != ExitNotConfirmed || got.Status != "not-confirmed" {
			t.Fatalf("RunInstall() = code %d, outcome %#v", code, got)
		}
		if len(env.ran) != 0 {
			t.Fatalf("an unconfirmed plan ran %d commands", len(env.ran))
		}
		if !strings.Contains(got.Error, "not the reviewed plan") {
			t.Fatalf("error did not explain the mismatch: %q", got.Error)
		}
	})

	t.Run("a changed plan invalidates the fingerprint", func(t *testing.T) {
		env := cloneEnv(PortConfiguration{})
		apply := options()
		apply.ConfirmPlanHash = outcome.PlanHash
		apply.UpdateExisting = true // any difference at all must invalidate it
		got, code := workflow().RunInstall(context.Background(), env, panicReader{}, io.Discard, false, apply)
		if code != ExitNotConfirmed {
			t.Fatalf("RunInstall() = code %d, outcome %#v", code, got)
		}
		if len(env.ran) != 0 {
			t.Fatalf("a changed plan ran %d commands", len(env.ran))
		}
	})
}
