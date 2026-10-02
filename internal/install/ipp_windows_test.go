//go:build windows

package install

import (
	"context"
	"strings"
	"testing"
)

// These cmdlet doubles exercise the actual generated PowerShell control flow.
// They cannot call Windows PrintManagement or change an installed queue.
const ippPrinterHarness = `
$global:printers = @(); $global:ports = @(); $global:added = 0; $global:removed = 0
function Write-Error { param($Message) [Console]::Error.WriteLine($Message); [Console]::Error.WriteLine("STATE:$global:added,$global:removed,$($global:printers.Count)") }
function Get-Printer { [CmdletBinding()]param() $global:printers }
function Get-PrinterPort { [CmdletBinding()]param() $global:ports }
function Add-Printer { [CmdletBinding()]param($Name,$IppURL)
 $global:added++; $global:printers += [PSCustomObject]@{Name=$Name;DriverName='Microsoft IPP Class Driver';PortName=$IppURL};
 $global:ports += [PSCustomObject]@{Name=$IppURL;PortMonitor='Internet Port';PrinterHostAddress=$null}
}
function Remove-Printer { [CmdletBinding()]param($InputObject)
 $global:removed++; $global:printers = @($global:printers | Where-Object { $_.Name -ne $InputObject.Name })
}
`

func ippTestPlan() Plan {
	p := sampleIPPProfile()
	return Plan{IPP: true, IPPURL: p.IPPURL, IPAddress: p.Target, PrinterName: p.PrinterName, DriverName: p.DriverName, Commands: installIPPCommands(Plan{IPP: true, IPPURL: p.IPPURL, IPAddress: p.Target, PrinterName: p.PrinterName, DriverName: p.DriverName})}
}

func TestPowerShellIPPCreateReapplyAndRejectConflict(t *testing.T) {
	plan := ippTestPlan()
	output, err := runPowerShell(context.Background(), ippPrinterHarness+strings.Join(plan.Commands, "; ")+"; "+strings.Join(plan.Commands, "; ")+`; "STATE:$global:added,$global:removed,$($global:printers.Count)"`)
	if err != nil || !strings.Contains(output, "STATE:1,0,1") || !strings.Contains(output, "Unchanged IPP printer") {
		t.Fatalf("create/reapply: %v %s", err, output)
	}
	setup := `$global:printers = @([PSCustomObject]@{Name='Accounts printer';DriverName='Other driver';PortName='other'}); `
	output, err = runPowerShell(context.Background(), ippPrinterHarness+setup+plan.Commands[0])
	if err == nil || !strings.Contains(output, "different driver") || !strings.Contains(output, "STATE:0,0,1") {
		t.Fatalf("existing queue was changed: %v %s", err, output)
	}
}

func TestPowerShellIPPRollsBackNewQueueOnVerificationFailure(t *testing.T) {
	plan := ippTestPlan()
	for _, setup := range []string{
		`function Add-Printer { [CmdletBinding()]param($Name,$IppURL) $global:added++; $global:printers += [PSCustomObject]@{Name=$Name;DriverName='Wrong driver';PortName=$IppURL}; $global:ports += [PSCustomObject]@{Name=$IppURL;PortMonitor='Internet Port';PrinterHostAddress=$null} }; `,
		`function Add-Printer { [CmdletBinding()]param($Name,$IppURL) $global:added++; $global:printers += [PSCustomObject]@{Name=$Name;DriverName='Microsoft IPP Class Driver';PortName='opaque-port'}; $global:ports += [PSCustomObject]@{Name='opaque-port';PortMonitor='Internet Port';PrinterHostAddress=$null} }; `,
	} {
		output, err := runPowerShell(context.Background(), ippPrinterHarness+setup+plan.Commands[0])
		if err == nil || !strings.Contains(output, "newly created IPP queue removed") || !strings.Contains(output, "STATE:1,1,0") {
			t.Fatalf("new queue not rolled back: %v %s", err, output)
		}
	}
}

func TestPowerShellIPPReportsCleanupFailure(t *testing.T) {
	plan := ippTestPlan()
	setup := `function Add-Printer { [CmdletBinding()]param($Name,$IppURL) $global:added++; $global:printers += [PSCustomObject]@{Name=$Name;DriverName='Wrong driver';PortName=$IppURL} }; function Remove-Printer { [CmdletBinding()]param($InputObject) throw 'cleanup denied' }; `
	output, err := runPowerShell(context.Background(), ippPrinterHarness+setup+plan.Commands[0])
	if err == nil || !strings.Contains(output, "different driver") || !strings.Contains(output, "cleanup failed: cleanup denied") || !strings.Contains(output, "STATE:1,0,1") {
		t.Fatalf("cleanup failure lost: %v %s", err, output)
	}
}
