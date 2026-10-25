package install

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// A failed step used to surface as `install: run "<several hundred characters
// of PowerShell>": exit status 1`: the reason Windows gave was only in the
// per-command output, so the GUI dialog, the --json error field and the action
// log all lost it, and nothing said which earlier steps had already changed
// the machine.
func TestFailedStepErrorCarriesWindowsReasonAndWhatAlreadyChanged(t *testing.T) {
	plan := mustPlan(t)
	env := &fakeEnvironment{elevated: true, driverPresent: true,
		runOutputs: map[int]string{0: "Created port\r\n", 1: "Add-Printer : The spooler service is not reachable.\r\nAt line:1 char:1\r\n+ ...\r\n"},
		runErrors:  map[int]error{1: errors.New("exit status 1")},
	}
	_, err := Install(context.Background(), env, plan, true)
	if err == nil {
		t.Fatal("expected failure")
	}
	message := err.Error()
	for _, want := range []string{"step 2 of 2", "The spooler service is not reachable", "Created port"} {
		if !strings.Contains(message, want) {
			t.Errorf("error %q does not contain %q", message, want)
		}
	}
	if strings.Contains(message, "Get-Printer") || len(message) > 600 {
		t.Errorf("error should name the reason, not repeat the generated command: %q", message)
	}
	var stepErr *StepError
	if !errors.As(err, &stepErr) || stepErr.Step != 2 || stepErr.Total != 2 {
		t.Fatalf("error is not a StepError for step 2 of 2: %#v", err)
	}
}

func TestStepDiagnosticExtractsTheMessageFromRealWriteErrorOutput(t *testing.T) {
	// Captured from Windows PowerShell 5.1: Write-Error in a -Command script
	// echoes the (console-wrapped) script line before the message.
	output := "[Console]::OutputEncoding = New-Object System.Text.UTF8Encoding($false); $ErrorActionPreference = 'Stop'; try { $prin\r\nter = @(Get-Printer) } catch { Write-Error $_.Exception.Message; exit 1 } : Existing printer has different settings; use \r\nconfigure --profile to review an update\r\nAt line:1 char:300\r\n+ ... catch { Write-Error $_.Exception.Message; exit 1 }\r\n+           ~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~\r\n    + CategoryInfo          : NotSpecified: (:) [Write-Error], WriteErrorException\r\n    + FullyQualifiedErrorId : Microsoft.PowerShell.Commands.WriteErrorException\r\n \r\n"
	if got, want := stepDiagnostic(output), "Existing printer has different settings; use configure --profile to review an update"; got != want {
		t.Fatalf("stepDiagnostic = %q, want %q", got, want)
	}
	if got := stepDiagnostic("Created port\r\nsomething else went wrong\r\n"); got != "something else went wrong" {
		t.Fatalf("fallback = %q", got)
	}
	if got := stepDiagnostic(""); got != "" {
		t.Fatalf("empty = %q", got)
	}
}
