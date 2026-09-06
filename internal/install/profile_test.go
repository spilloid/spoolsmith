package install

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spilloid/spoolsmith/internal/evidence"
	"github.com/spilloid/spoolsmith/internal/probe"
)

func sampleProfile() Profile {
	return Profile{Version: 1, Target: "192.0.2.10", PrinterName: "Accounts printer", DriverName: "Exact OEM driver", Evidence: evidence.Evidence{IP: "192.0.2.10", Provenance: "captured", HTTPTitle: "Example Model 123", PJLID: "Example Model 123:firmware1"}}
}

func TestProfileRoundTripAndNeverOverwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "printer.json")
	p := sampleProfile()
	if err := SaveProfile(path, p); err != nil {
		t.Fatal(err)
	}
	if err := SaveProfile(path, p); err == nil {
		t.Fatal("overwrote existing profile")
	}
	loaded, err := LoadProfile(path)
	if err != nil || loaded.PrinterName != p.PrinterName || loaded.Evidence.PJLID != p.Evidence.PJLID {
		t.Fatalf("loaded %#v, %v", loaded, err)
	}
}

func TestProfileEditPreservesPreviousVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "printer.json")
	p := sampleProfile()
	if err := SaveProfile(path, p); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"New queue", "Next queue"} {
		prior := p.PrinterName
		p.PrinterName = name
		backup, err := EditProfile(path, p)
		if err != nil {
			t.Fatal(err)
		}
		old, err := LoadProfile(backup)
		if err != nil || old.PrinterName != prior {
			t.Fatalf("backup=%#v %v", old, err)
		}
		current, err := LoadProfile(path)
		if err != nil || current.PrinterName != name {
			t.Fatalf("updated=%#v %v", current, err)
		}
	}
}

func TestProfileRejectsInvalidJSONAndFields(t *testing.T) {
	for _, raw := range []string{
		`{"version":2}`, `{"version":1,"commands":["evil"]}`, `{} {}`, strings.Repeat("x", 1024*1024+1),
	} {
		path := filepath.Join(t.TempDir(), "bad.json")
		if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadProfile(path); err == nil {
			t.Fatal("accepted invalid profile")
		}
	}
	for _, mutate := range []func(*Profile){
		func(p *Profile) { p.PrinterName = "bad\nname" },
		func(p *Profile) { p.DriverName = "bad\u202ename" },
		func(p *Profile) { p.Target = "not an IP" },
		func(p *Profile) { p.Evidence.Provenance = "synthetic" },
		func(p *Profile) { p.Evidence.HTTPTitle = ""; p.Evidence.PJLID = "" },
	} {
		p := sampleProfile()
		mutate(&p)
		if err := p.Validate(); err == nil {
			t.Fatalf("accepted invalid profile %#v", p)
		}
	}
}

func TestProfileInstallIdentityAndConfirmation(t *testing.T) {
	for _, tc := range []struct {
		name      string
		change    func(*evidence.Evidence)
		dry, yes  bool
		want      ExitCode
		mutations int
	}{
		{"dry-run overrides yes", nil, true, true, ExitSuccess, 0},
		{"requires confirmation", nil, false, false, ExitNotConfirmed, 0},
		{"confirmed", nil, false, true, ExitSuccess, 2},
		{"different printer", func(e *evidence.Evidence) { e.HTTPTitle = "Other Model" }, false, true, ExitUnresolved, 0},
		{"one model source unavailable", func(e *evidence.Evidence) { e.PJLID = "" }, false, true, ExitSuccess, 2},
		{"offline", func(e *evidence.Evidence) { e.PJLID = ""; e.HTTPTitle = "" }, false, true, ExitUnresolved, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := sampleProfile()
			w := testWorkflow(true)
			w.Collect = func(_ context.Context, target string) (probe.Result, error) {
				if target != p.Target {
					t.Fatalf("target = %s", target)
				}
				e := p.Evidence
				if tc.change != nil {
					tc.change(&e)
				}
				return probe.Result{Evidence: e}, nil
			}
			env := workflowEnvironment(true, true)
			outcome, code := w.RunInstall(context.Background(), env, panicReader{}, io.Discard, false, InstallOptions{Profile: &p, DryRun: tc.dry, Yes: tc.yes})
			if code != tc.want || len(env.ran) != tc.mutations {
				t.Fatalf("code=%d outcome=%#v mutations=%d", code, outcome, len(env.ran))
			}
			if outcome.Plan != nil && (outcome.Plan.PrinterName != p.PrinterName || outcome.Plan.DriverName != p.DriverName || outcome.Resolution != "operator-profile" || !outcome.Plan.ForcedOverride) {
				t.Fatalf("wrong plan: %#v", outcome)
			}
		})
	}
}

func TestProfileRejectsMixedSelectionBeforeProbe(t *testing.T) {
	w := testWorkflow(true)
	w.Collect = func(context.Context, string) (probe.Result, error) { panic("must validate before probe") }
	p := sampleProfile()
	for _, options := range []InstallOptions{{Profile: &p, Target: p.Target}, {Profile: &p, ForceFamily: "test-family"}} {
		_, code := w.RunInstall(context.Background(), workflowEnvironment(true, true), panicReader{}, io.Discard, false, options)
		if code != ExitUsageError {
			t.Fatalf("code=%d", code)
		}
	}
}

func TestProfilePlanQuotesOperatorInput(t *testing.T) {
	p := sampleProfile()
	p.PrinterName = "Printer $(Write-Output injected) \u201d ` \""
	r, err := p.resolution(p.Evidence)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildPlan(p.Target, r)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan.Commands[1], "`$(Write-Output injected)") || !strings.Contains(plan.Commands[1], "`\u201d") {
		t.Fatalf("unescaped: %s", plan.Commands[1])
	}
}

func TestProfileSNMPFallbackNeverReplacesSavedModelSources(t *testing.T) {
	p := sampleProfile()
	p.Evidence.SNMPSysDescr = "Printer network module"
	current := p.Evidence
	current.HTTPTitle, current.PJLID = "", ""
	if _, err := p.resolution(current); err == nil {
		t.Fatal("SNMP weakened a saved HTTP/PJL identity")
	}
	p.Evidence.HTTPTitle, p.Evidence.PJLID = "", ""
	if _, err := p.resolution(current); err != nil {
		t.Fatalf("explicit SNMP-only capture rejected: %v", err)
	}
	current.SNMPSysDescr = "Other device"
	if _, err := p.resolution(current); err == nil {
		t.Fatal("changed SNMP accepted")
	}
}

func TestProfileRemovalRequiresMatchingEndpointAndDriver(t *testing.T) {
	p := sampleProfile()
	for _, configuration := range []PrinterConfiguration{
		{PrinterName: p.PrinterName, PortName: "Other port", DriverName: p.DriverName},
		{PrinterName: p.PrinterName, PortName: "SpoolSmith-" + p.Target, DriverName: "Other driver"},
	} {
		env := workflowEnvironment(true, true)
		env.configuration = configuration
		outcome, code := testWorkflow(true).RunUninstall(context.Background(), env, panicReader{}, io.Discard, false, UninstallOptions{Profile: &p, PrinterName: p.PrinterName, Yes: true})
		if code != ExitUnresolved || len(env.ran) != 0 || !strings.Contains(outcome.Error, "differs from profile") {
			t.Fatalf("%#v code=%d mutations=%d", outcome, code, len(env.ran))
		}
	}
}

func TestProfileRetriesUnavailableIdentityOnce(t *testing.T) {
	p := sampleProfile()
	w := testWorkflow(true)
	calls := 0
	w.Collect = func(context.Context, string) (probe.Result, error) {
		calls++
		if calls == 1 {
			return probe.Result{Evidence: evidence.Evidence{IP: p.Target, Provenance: "captured"}}, nil
		}
		return probe.Result{Evidence: p.Evidence}, nil
	}
	env := workflowEnvironment(true, true)
	outcome, code := w.RunInstall(context.Background(), env, panicReader{}, io.Discard, false, InstallOptions{Profile: &p, DryRun: true})
	if code != ExitSuccess || calls != 2 || len(env.ran) != 0 {
		t.Fatalf("%#v code=%d calls=%d", outcome, code, calls)
	}
	calls = 0
	w.Collect = func(context.Context, string) (probe.Result, error) {
		calls++
		e := p.Evidence
		e.HTTPTitle = "Different printer"
		return probe.Result{Evidence: e}, nil
	}
	_, code = w.RunInstall(context.Background(), env, panicReader{}, io.Discard, false, InstallOptions{Profile: &p, Yes: true})
	if code != ExitUnresolved || calls != 1 || len(env.ran) != 0 {
		t.Fatalf("conflicting evidence was retried or mutated: code=%d calls=%d", code, calls)
	}
}
