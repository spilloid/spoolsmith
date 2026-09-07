package install

import (
	"context"
	"io"
	"testing"
)

func TestInstallExecutionMustMatchReviewedPlan(t *testing.T) {
	for _, changed := range []bool{false, true} {
		w := testWorkflow(true)
		env := workflowEnvironment(true, true)
		options := InstallOptions{Target: "192.0.2.10", DryRun: true}
		preview, code := w.RunInstall(context.Background(), env, panicReader{}, io.Discard, false, options)
		if code != ExitSuccess || preview.Plan == nil {
			t.Fatal(preview)
		}
		options.DryRun = false
		options.Yes = true
		options.ExpectedPlan = preview.Plan
		options.UpdateExisting = changed
		outcome, code := w.RunInstall(context.Background(), env, panicReader{}, io.Discard, false, options)
		if changed {
			if code != ExitNotConfirmed || len(env.ran) != 0 {
				t.Fatalf("changed plan executed: %#v", outcome)
			}
		} else if code != ExitSuccess || len(env.ran) == 0 {
			t.Fatalf("matching plan failed: %#v", outcome)
		}
	}
}

func TestRemovalCannotReplanAfterApproval(t *testing.T) {
	w := testWorkflow(true)
	env := workflowEnvironment(true, true)
	options := UninstallOptions{PrinterName: env.configuration.PrinterName, DryRun: true}
	preview, code := w.RunUninstall(context.Background(), env, panicReader{}, io.Discard, false, options)
	if code != ExitSuccess || preview.Plan == nil {
		t.Fatal(preview)
	}
	options.DryRun = false
	options.Yes = true
	options.ExpectedPlan = preview.Plan
	env.configuration.PortName = "Changed-after-preview"
	outcome, code := w.RunUninstall(context.Background(), env, panicReader{}, io.Discard, false, options)
	if code != ExitNotConfirmed || len(env.ran) != 0 {
		t.Fatalf("removed changed queue: %#v", outcome)
	}
}
