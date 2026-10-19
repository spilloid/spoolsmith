//go:build windows

package install

import (
	"context"
	"strings"
	"testing"
)

// A stopped or broken spooler is the commonest real failure, and the first
// read-only check to hit it decides what the helpdesk tech reads. Run the
// real generated commands through real PowerShell with the cmdlets throwing
// what Windows throws, and require the reason, not the echoed script.
const stoppedSpooler = `function Get-PrinterDriver { [CmdletBinding()]param() throw 'The spooler service is not reachable.  Ensure the spooler service is running.' }
function Get-Printer { [CmdletBinding()]param() throw 'The spooler service is not reachable.  Ensure the spooler service is running.' }
`

func stubbedRunner(prefix string) powerShellRunner {
	return func(ctx context.Context, command string) (string, error) {
		return runPowerShell(ctx, prefix+command)
	}
}

func assertSpoolerReason(t *testing.T, what string, err error) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: expected an error", what)
	}
	message := err.Error()
	if !strings.Contains(message, "The spooler service is not reachable") {
		t.Errorf("%s: error lost Windows' reason: %q", what, message)
	}
	if strings.Contains(message, "-ErrorAction Stop") || strings.Contains(message, "CategoryInfo") || len(message) > 400 {
		t.Errorf("%s: error repeats the script or the error record instead of the reason (%d chars): %q", what, len(message), message)
	}
}

func TestSpoolerFailureReasonReachesPreflightAndLookupErrors(t *testing.T) {
	runner := stubbedRunner(stoppedSpooler)
	_, err := checkDriverPresent(context.Background(), runner, "Verified Driver")
	assertSpoolerReason(t, "driver presence", err)
	_, err = lookupPrinterWith(context.Background(), runner, "Accounting")
	assertSpoolerReason(t, "printer lookup", err)
}
