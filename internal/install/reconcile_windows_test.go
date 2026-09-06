//go:build windows

package install

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// Real PowerShell executes the generated control flow against in-memory cmdlet
// doubles. No PrintManagement cmdlet can be reached by these scripts.
const printerHarness = `
function Write-Error { param($Message) [Console]::Error.WriteLine($Message) }
$global:printers = @(); $global:ports = @()
$global:addedPrinters=0; $global:addedPorts=0; $global:updated=0; $global:removedPrinters=0; $global:removedPorts=0
function Get-Printer { [CmdletBinding()]param() $global:printers }
function Get-PrinterPort { [CmdletBinding()]param() $global:ports }
function Get-PrinterDriver { [CmdletBinding()]param() @() }
function Add-PrinterPort { [CmdletBinding()]param($Name,$PrinterHostAddress,$PortNumber)
 $global:addedPorts++; $global:ports += [PSCustomObject]@{Name=$Name;PrinterHostAddress=$PrinterHostAddress;PortNumber=$PortNumber;Protocol=1}
}
function Add-Printer { [CmdletBinding()]param($Name,$DriverName,$PortName)
 $global:addedPrinters++; $global:printers += [PSCustomObject]@{Name=$Name;DriverName=$DriverName;PortName=$PortName}
}
function Set-Printer { [CmdletBinding()]param($InputObject,$DriverName,$PortName)
 $global:updated++; $InputObject.DriverName=$DriverName; $InputObject.PortName=$PortName
}
function Remove-Printer { [CmdletBinding()]param($InputObject)
 $global:removedPrinters++; $global:printers = @($global:printers | Where-Object {$_.Name -ne $InputObject.Name})
}
function Remove-PrinterPort { [CmdletBinding()]param([Parameter(ValueFromPipeline=$true)]$InputObject)
 process { $global:removedPorts++; $global:ports = @($global:ports | Where-Object {$_.Name -ne $InputObject.Name}) }
}
function Remove-PrinterDriver { [CmdletBinding()]param([Parameter(ValueFromPipeline=$true)]$InputObject) process {} }
`

func TestPowerShellReapplyAndRemovalAreIdempotent(t *testing.T) {
	plan := mustPlan(t)
	remove, err := BuildUninstallPlan(plan.PrinterName, plan.PortName, plan.DriverName, false)
	if err != nil {
		t.Fatal(err)
	}
	script := printerHarness + strings.Join(plan.Commands, "; ") + "; " + strings.Join(plan.Commands, "; ") + "; " + strings.Join(remove.Commands, "; ") + "; " + strings.Join(remove.Commands, "; ") + "; " + `
'RESULT:' + (@{AddedPrinters=$global:addedPrinters;AddedPorts=$global:addedPorts;RemovedPrinters=$global:removedPrinters;RemovedPorts=$global:removedPorts;Printers=$global:printers.Count;Ports=$global:ports.Count} | ConvertTo-Json -Compress)
`
	output, err := runPowerShell(context.Background(), script)
	if err != nil {
		t.Fatalf("PowerShell: %v\n%s", err, output)
	}
	index := strings.LastIndex(output, "RESULT:")
	if index < 0 {
		t.Fatalf("missing result: %s", output)
	}
	var got struct{ AddedPrinters, AddedPorts, RemovedPrinters, RemovedPorts, Printers, Ports int }
	if err := json.Unmarshal([]byte(strings.TrimSpace(output[index+7:])), &got); err != nil {
		t.Fatal(err)
	}
	if got.AddedPrinters != 1 || got.AddedPorts != 1 || got.RemovedPrinters != 1 || got.RemovedPorts != 1 || got.Printers != 0 || got.Ports != 0 {
		t.Fatalf("non-idempotent: %#v\n%s", got, output)
	}
}

func TestPowerShellConflictsFailBeforeCreatingPort(t *testing.T) {
	plan := mustPlan(t)
	for _, setup := range []string{
		`$global:printers=@([PSCustomObject]@{Name='HP LaserJet Pro M404dn';DriverName='Other driver';PortName='other'})`,
		`$global:ports=@([PSCustomObject]@{Name='SpoolSmith-192.0.2.10';PrinterHostAddress='192.0.2.99';PortNumber=9100;Protocol=1})`,
		`$global:ports=@([PSCustomObject]@{Name='SpoolSmith-192.0.2.10';PrinterHostAddress='192.0.2.10';PortNumber=515;Protocol=2})`,
	} {
		script := printerHarness + setup + `; function Add-PrinterPort { throw 'UNEXPECTED MUTATION' }; ` + strings.Join(plan.Commands, "; ")
		output, err := runPowerShell(context.Background(), script)
		if err == nil || strings.Contains(output, "UNEXPECTED MUTATION") || !strings.Contains(output, "Existing") {
			t.Fatalf("conflict did not fail closed: %v %s", err, output)
		}
	}
}

func TestPowerShellConfigureAndSharedPort(t *testing.T) {
	plan := mustPlan(t)
	plan.UpdateExisting = true
	plan.Commands = installCommands(plan)
	remove, err := BuildUninstallPlan(plan.PrinterName, plan.PortName, plan.DriverName, false)
	if err != nil {
		t.Fatal(err)
	}
	setup := `$global:printers=@([PSCustomObject]@{Name='HP LaserJet Pro M404dn';DriverName='Old driver';PortName='old'},[PSCustomObject]@{Name='Shared queue';DriverName='Other driver';PortName='SpoolSmith-192.0.2.10'}); `
	output, err := runPowerShell(context.Background(), printerHarness+setup+strings.Join(plan.Commands, "; ")+"; "+strings.Join(plan.Commands, "; ")+"; "+strings.Join(remove.Commands, "; ")+`; if ($global:updated -ne 1 -or $global:removedPorts -ne 0 -or $global:printers.Count -ne 1) { throw 'Wrong final state' }; 'VERIFIED'`)
	if err != nil || !strings.Contains(output, "VERIFIED") || !strings.Contains(output, "Retained shared port") {
		t.Fatalf("configure/shared: %v %s", err, output)
	}
}
