package install

import (
	"strings"
	"testing"
)

func TestBuildRepointPlanKeepsTheQueueAndMovesThePort(t *testing.T) {
	current := PrinterConfiguration{PrinterName: "Accounting", PortName: "SpoolSmith-192.0.2.10", DriverName: "Brother HL-L2315D series"}
	plan, err := BuildRepointPlan(current, "192.0.2.50")
	if err != nil {
		t.Fatal(err)
	}
	if plan.PrinterName != "Accounting" {
		t.Fatalf("repoint renamed the queue: %q", plan.PrinterName)
	}
	if plan.DriverName != "Brother HL-L2315D series" {
		t.Fatalf("repoint changed the driver: %q", plan.DriverName)
	}
	if plan.PortName != "SpoolSmith-192.0.2.50" || plan.IPAddress != "192.0.2.50" {
		t.Fatalf("repoint did not move the port: %q -> %q", plan.PortName, plan.IPAddress)
	}
	if !plan.UpdateExisting {
		t.Fatal("repoint must update the existing queue rather than refuse it")
	}
	// The old port must not be removed: another queue may still print through it.
	for _, command := range plan.Commands {
		if strings.Contains(command, "Remove-PrinterPort") {
			t.Fatalf("repoint removes the old port: %s", command)
		}
	}
	joined := strings.Join(plan.Commands, "\n")
	if !strings.Contains(joined, "Set-Printer") || !strings.Contains(joined, "Add-PrinterPort") {
		t.Fatalf("repoint lost its port/queue steps: %s", joined)
	}
}

func TestBuildRepointPlanRefusesNoOpAndBadInput(t *testing.T) {
	base := PrinterConfiguration{PrinterName: "Accounting", PortName: "SpoolSmith-192.0.2.10", DriverName: "Brother HL-L2315D series"}
	tests := []struct {
		name    string
		current PrinterConfiguration
		address string
		want    string
	}{
		{name: "already there", current: base, address: "192.0.2.10", want: "nothing to change"},
		{name: "not an address", current: base, address: "printer.local", want: "not a literal IP address"},
		{name: "no driver", current: PrinterConfiguration{PrinterName: "Accounting", PortName: "p"}, address: "192.0.2.50", want: "no driver"},
		{name: "control character in name", current: PrinterConfiguration{PrinterName: "Acc\nounting", PortName: "p", DriverName: "d"}, address: "192.0.2.50", want: "control character"},
		{name: "bidirectional override in driver", current: PrinterConfiguration{PrinterName: "Accounting", PortName: "p", DriverName: "d‮rivers"}, address: "192.0.2.50", want: "bidirectional"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := BuildRepointPlan(tt.current, tt.address)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("BuildRepointPlan() = %v, want an error containing %q", err, tt.want)
			}
		})
	}
}

// A repoint is a mutation, so it must be reachable only through the same
// shown-plan-then-confirm gate as every other mutating path.
func TestRunRepointRequiresConfirmation(t *testing.T) {
	env := workflowEnvironment(true, true)
	workflow := NewWorkflow()
	outcome, code := workflow.RunRepoint(t.Context(), env, strings.NewReader(""), discardWriter{}, false, RepointOptions{
		PrinterName: "Test Printer",
		NewAddress:  "192.0.2.50",
	})
	if code != ExitNotConfirmed || outcome.Status != "not-confirmed" {
		t.Fatalf("RunRepoint() = %q / %d, want not-confirmed", outcome.Status, code)
	}
	if len(env.ran) != 0 {
		t.Fatalf("RunRepoint() ran %d commands without confirmation: %v", len(env.ran), env.ran)
	}
}

func TestRunRepointDryRunMutatesNothing(t *testing.T) {
	env := workflowEnvironment(true, true)
	workflow := NewWorkflow()
	outcome, code := workflow.RunRepoint(t.Context(), env, strings.NewReader(""), discardWriter{}, false, RepointOptions{
		PrinterName: "Test Printer",
		NewAddress:  "192.0.2.50",
		DryRun:      true,
	})
	if code != ExitSuccess || outcome.Status != "dry-run" {
		t.Fatalf("RunRepoint() = %q / %d, want a successful dry run", outcome.Status, code)
	}
	if len(env.ran) != 0 {
		t.Fatalf("dry run ran %d commands: %v", len(env.ran), env.ran)
	}
	if outcome.PlanHash == "" {
		t.Fatal("a previewed repoint plan has no fingerprint to review")
	}
}

// discardWriter is a writer that keeps the test output quiet.
type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }
