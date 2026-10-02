package install

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/spilloid/spoolsmith/internal/probe"
)

func sampleIPPProfile() Profile {
	p := sampleProfile()
	p.PortType = "ipp"
	p.SourcePort = "WSD-12345678"
	p.IPPURL = "ipp://192.0.2.10/ipp/print"
	p.DriverName = "Microsoft IPP Class Driver"
	return p
}

func TestIPPProfileRequiresLiteralTargetEndpointAndClassDriver(t *testing.T) {
	p := sampleIPPProfile()
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, endpoint := range []string{
		"", "ipp://printer.example/ipp/print", "ipp://192.0.2.11/ipp/print",
		"ftp://192.0.2.10/ipp/print", "ipp://user@192.0.2.10/ipp/print",
		"ipp://192.0.2.10/ipp/print?other=1", "ipp://192.0.2.10/ipp/print#fragment",
		"ipp://192.0.2.10:99999/ipp/print", "ipp://192.0.2.10/ipp/print\n",
	} {
		changed := p
		changed.IPPURL = endpoint
		if err := changed.Validate(); err == nil {
			t.Errorf("accepted IPP endpoint %q", endpoint)
		}
	}
	p.DriverName = "Brother vendor driver"
	if err := p.Validate(); err == nil {
		t.Fatal("accepted vendor driver for directed IPP install")
	}
}

func TestIPPProfilePreviewAndInboxDriverPreflight(t *testing.T) {
	p := sampleIPPProfile()
	w := testWorkflow(true)
	w.Collect = func(_ context.Context, target string) (probe.Result, error) {
		if target != p.Target {
			t.Fatalf("probed %q", target)
		}
		return probe.Result{Evidence: p.Evidence}, nil
	}
	env := workflowEnvironment(true, false) // Inbox driver may not be registered yet.
	outcome, code := w.RunInstall(context.Background(), env, panicReader{}, io.Discard, false, InstallOptions{Profile: &p, DryRun: true})
	if code != ExitSuccess || outcome.Plan == nil || !outcome.Plan.IPP || outcome.Plan.IPPURL != p.IPPURL || outcome.Plan.PortName != "" {
		t.Fatalf("code=%d plan=%#v error=%q", code, outcome.Plan, outcome.Error)
	}
	if env.driverChecks != 0 || len(env.ran) != 0 {
		t.Fatalf("preview checked an unregistered inbox driver or mutated Windows: %#v", env)
	}
	if len(outcome.Plan.Commands) != 1 || !strings.Contains(outcome.Plan.Commands[0], "Add-Printer -Name") || !strings.Contains(outcome.Plan.Commands[0], "-IppURL") || strings.Contains(outcome.Plan.Commands[0], "Add-PrinterPort") || strings.Contains(outcome.Plan.Commands[0], "-DriverName") {
		t.Fatalf("wrong IPP command: %#v", outcome.Plan.Commands)
	}
}

func TestIPPStatusRequiresVisibleEndpointAndIPPMonitor(t *testing.T) {
	p := sampleIPPProfile()
	matching := LocalConfiguration{QueuePresent: true, PrinterName: p.PrinterName, DriverName: p.DriverName, DriverPresent: true, PortName: p.IPPURL, PortPresent: true, PortMonitor: "Internet Port"}
	env := &offlineEnvironment{fakeEnvironment: workflowEnvironment(true, true), actual: matching}
	status, err := CheckStatus(context.Background(), env, p)
	if err != nil || !status.Compliant {
		t.Fatalf("matching IPP queue: %+v %v", status, err)
	}
	for _, change := range []func(*LocalConfiguration){
		func(c *LocalConfiguration) { c.PortName = "WSD-1234" },
		func(c *LocalConfiguration) { c.PortMonitor = "WSD Port Monitor" },
		func(c *LocalConfiguration) { c.DriverName = "Other driver" },
	} {
		env.actual = matching
		change(&env.actual)
		status, err := CheckStatus(context.Background(), env, p)
		if err != nil || status.Compliant {
			t.Fatalf("accepted changed queue: %+v %v", status, err)
		}
	}
}

func TestIPPProfileRemovalChecksLivePort(t *testing.T) {
	p := sampleIPPProfile()
	env := &offlineEnvironment{fakeEnvironment: workflowEnvironment(true, true), actual: LocalConfiguration{QueuePresent: true, PrinterName: p.PrinterName, DriverName: p.DriverName, DriverPresent: true, PortName: p.IPPURL, PortPresent: true, PortMonitor: "Internet Port"}}
	env.configuration = PrinterConfiguration{PrinterName: p.PrinterName, DriverName: p.DriverName, PortName: p.IPPURL}
	outcome, code := testWorkflow(true).RunUninstall(context.Background(), env, panicReader{}, io.Discard, false, UninstallOptions{Profile: &p, PrinterName: p.PrinterName, DryRun: true})
	if code != ExitSuccess || outcome.Plan == nil || len(env.ran) != 0 {
		t.Fatalf("code=%d outcome=%#v ran=%#v", code, outcome, env.ran)
	}
	env.actual.PortMonitor = "WSD Port Monitor"
	outcome, code = testWorkflow(true).RunUninstall(context.Background(), env, panicReader{}, io.Discard, false, UninstallOptions{Profile: &p, PrinterName: p.PrinterName, Yes: true})
	if code != ExitUnresolved || len(env.ran) != 0 || !strings.Contains(outcome.Error, "differs from profile") {
		t.Fatalf("code=%d outcome=%#v ran=%#v", code, outcome, env.ran)
	}
}
