package install

import (
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spilloid/spoolsmith/internal/probe"
)

func brotherProfile() Profile {
	p := sampleProfile()
	p.DriverName = "Brother HL-L2315D series"
	p.DriverPackage = &PackageSelection{ID: "brother-y14a-c1-hostm-1110", Archive: "driver.EXE"}
	return p
}

func TestPackageProfileValidationAndRelativePath(t *testing.T) {
	p := brotherProfile()
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := p.ResolvePackagePath(filepath.Join(dir, "printer.json")); err != nil {
		t.Fatal(err)
	}
	if p.DriverPackage.Archive != filepath.Join(dir, "driver.EXE") {
		t.Fatal(p.DriverPackage.Archive)
	}
	p.DriverName = "Unrelated driver"
	if p.Validate() == nil {
		t.Fatal("accepted unrelated driver")
	}
	p = brotherProfile()
	p.DriverPackage.ID = "unreviewed"
	if p.Validate() == nil {
		t.Fatal("accepted unreviewed package")
	}
}

func TestPackageWorkflowConfirmationAndFailure(t *testing.T) {
	for _, mode := range []string{"dry-run", "decline", "package-fails", "success"} {
		t.Run(mode, func(t *testing.T) {
			p := brotherProfile()
			w := testWorkflow(true)
			w.Collect = func(context.Context, string) (probe.Result, error) { return probe.Result{Evidence: p.Evidence}, nil }
			env := workflowEnvironment(true, false)
			if mode == "package-fails" {
				env = environmentFailingCommand(0)
				env.driverPresent = false
			}
			opts := InstallOptions{Profile: &p, DryRun: mode == "dry-run", Yes: mode == "success" || mode == "package-fails" || mode == "dry-run"}
			out, code := w.RunInstall(context.Background(), env, panicReader{}, io.Discard, false, opts)
			if out.Plan == nil || len(out.Plan.Commands) != 3 {
				t.Fatalf("missing package plan: %#v", out)
			}
			switch mode {
			case "dry-run":
				if code != ExitSuccess || out.Confirmed || len(env.ran) != 0 {
					t.Fatal(out)
				}
			case "decline":
				if code != ExitNotConfirmed || len(env.ran) != 0 {
					t.Fatal(out)
				}
			case "package-fails":
				if code != ExitGeneralError || len(env.ran) != 1 {
					t.Fatal(out)
				}
			case "success":
				if code != ExitSuccess || len(env.ran) != 3 {
					t.Fatal(out)
				}
			}
			if !strings.Contains(out.Plan.Commands[0], "SHA-256 mismatch") {
				t.Fatal("verification absent")
			}
		})
	}
}
