package install

import (
	"context"
	"io"
	"strings"
	"testing"
)

func TestWindowsIPPProfileCapturesProtocolIdentity(t *testing.T) {
	p, err := windowsIPPProfile("192.0.2.10", "Office", "ipp://192.0.2.10/ipp/print", "Example printer")
	if err != nil || p.DriverName != "Microsoft IPP Class Driver" || !p.hasCapturedIdentity() {
		t.Fatalf("profile=%+v err=%v", p, err)
	}
	if _, err := p.resolution(p.Evidence); err != nil {
		t.Fatal(err)
	}
	changed := p.Evidence
	changed.IPPModel = "Different printer"
	if _, err := p.resolution(changed); err == nil {
		t.Fatal("accepted conflicting IPP model")
	}
	for _, endpoint := range []string{"ipp://printer.example/ipp/print", "ipp://192.0.2.11/ipp/print"} {
		if _, err := windowsIPPProfile(p.Target, p.PrinterName, endpoint, p.Evidence.IPPModel); err == nil {
			t.Fatalf("accepted %s", endpoint)
		}
	}
	if _, err := windowsIPPProfile(p.Target, p.PrinterName, p.IPPURL, ""); err == nil {
		t.Fatal("accepted missing model")
	}
}

func TestWindowsIPPOnlyPreviewAndIdentityConflict(t *testing.T) {
	p, err := windowsIPPProfile("192.0.2.10", "Office", "ipp://192.0.2.10/ipp/print", "Example printer")
	if err != nil {
		t.Fatal(err)
	}
	w := testWorkflow(true)
	w.DiscoverIPP = func(context.Context, string) (string, string, error) { return p.IPPURL, p.Evidence.IPPModel, nil }
	w.Collect = nil // This path must not require a legacy probe.
	// Workflow validation requires Collect even though this branch won't call it.
	w.Collect = testWorkflow(true).Collect
	env := workflowEnvironment(true, false)
	out, code := w.RunInstall(context.Background(), env, strings.NewReader(""), io.Discard, false, InstallOptions{Profile: &p, DryRun: true})
	if code != ExitSuccess || out.Plan == nil || !out.Plan.IPP || out.Plan.Offline || len(env.ran) != 0 || env.driverChecks != 0 {
		t.Fatalf("preview: %+v, code=%d, env=%+v", out, code, env)
	}
	w.DiscoverIPP = func(context.Context, string) (string, string, error) { return p.IPPURL, "Different printer", nil }
	out, code = w.RunInstall(context.Background(), env, strings.NewReader(""), io.Discard, false, InstallOptions{Profile: &p, Yes: true})
	if code != ExitUnresolved || len(env.ran) != 0 {
		t.Fatalf("conflict mutated Windows: %+v, code=%d", out, code)
	}
}
