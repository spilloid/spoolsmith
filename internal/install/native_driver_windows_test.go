//go:build windows

package install

import (
	"context"
	"strings"
	"testing"
)

// The native-driver step is one PowerShell command, so its selection rule is
// exercised by really running it with Get-PrinterDriver stubbed out.
func TestNativeDriverSelectionRule(t *testing.T) {
	cases := []struct {
		model string
		names string
		want  string
	}{
		{"Brother HL-L2315D series", "'Microsoft IPP Class Driver','Brother HL-L2315D series'", "Brother HL-L2315D series"},
		{"HL-L2315D Series", "'brother hl-l2315d series','Other'", ""},
		{"Example 100", "'Example 100 PCL','Example 1000'", ""},
		{"Example 100", "'Example-100','example 100'", ""},
		{"", "'Anything'", ""},
		{"Example 100", "'Microsoft IPP Class Driver'", ""},
	}
	for _, c := range cases {
		script := nativeDriverFunctions + "$r = Get-SpoolSmithNativeDriverName " + powerShellString(c.model) + " @(" + c.names + "); if ($null -eq $r) { 'none' } else { $r }"
		out, err := runPowerShell(context.Background(), script)
		if err != nil {
			t.Fatalf("%s: %v: %s", c.model, err, out)
		}
		got := strings.TrimSpace(out)
		if got == "none" {
			got = ""
		}
		if got != c.want {
			t.Errorf("model %q names %s: got %q want %q", c.model, c.names, got, c.want)
		}
	}
}

func TestNativeDriverCommandParses(t *testing.T) {
	command := nativeDriverCommand(nativeTestPlan(t))
	script := "$e=$null; [void][System.Management.Automation.Language.Parser]::ParseInput(" + powerShellString(command) + ", [ref]$null, [ref]$e); if ($e.Count -gt 0) { $e | % { $_.Message }; exit 1 } else { 'ok' }"
	if out, err := runPowerShell(context.Background(), script); err != nil || !strings.Contains(out, "ok") {
		t.Fatalf("native driver command does not parse: %v: %s", err, out)
	}
}

const nativeHarness = ippPrinterHarness + `
$global:drivers = @('Microsoft IPP Class Driver'); $global:addDriver = $null; $global:set = @()
function Get-PrinterDriver { [CmdletBinding()]param() $global:drivers | ForEach-Object { [PSCustomObject]@{Name=$_} } }
function Add-PrinterDriver { [CmdletBinding()]param($Name) if ($global:addDriver -ne $Name) { throw 'not available' }; $global:drivers += $Name }
function Set-Printer { [CmdletBinding()]param($Name,$DriverName) $global:set += $DriverName; ($global:printers | Where-Object { $_.Name -eq $Name }).DriverName = $DriverName }
function New-Object { [CmdletBinding()]param($ComObject) throw 'Windows Update unavailable' }
$global:printers = @([PSCustomObject]@{Name='Office';DriverName='Microsoft IPP Class Driver';PortName='ipp://192.0.2.10/ipp/print'});
$global:ports = @([PSCustomObject]@{Name='ipp://192.0.2.10/ipp/print';PortMonitor='Internet Port';PrinterHostAddress=$null});
`

func runNative(t *testing.T, setup string) string {
	t.Helper()
	out, err := runPowerShell(context.Background(), nativeHarness+setup+nativeDriverScript(nativeTestPlan(t))+`; "DRIVER:$($global:printers[0].DriverName)|SET:$($global:set -join ',')"`)
	if err != nil {
		t.Fatalf("native step must always exit 0: %v: %s", err, out)
	}
	return out
}

func TestNativeDriverStepUpgradesWhenWindowsHasTheDriver(t *testing.T) {
	out := runNative(t, `$global:addDriver = 'Example Printer 100'; `)
	if !strings.Contains(out, "SPOOLSMITH-NATIVE-DRIVER: applied Example Printer 100") || !strings.Contains(out, "DRIVER:Example Printer 100|") {
		t.Fatal(out)
	}
	out = runNative(t, `$global:drivers += 'Example Printer 100'; `)
	if !strings.Contains(out, "applied Example Printer 100") {
		t.Fatalf("already-registered driver not used: %s", out)
	}
}

func TestNativeDriverStepKeepsClassDriverWhenWindowsHasNothing(t *testing.T) {
	out := runNative(t, ``)
	if !strings.Contains(out, "skipped") || !strings.Contains(out, "DRIVER:Microsoft IPP Class Driver|SET:\r\n") && !strings.Contains(out, "DRIVER:Microsoft IPP Class Driver|SET:\n") {
		t.Fatalf("expected a quiet skip with the class driver kept: %s", out)
	}
}

func TestNativeDriverStepRestoresClassDriverWhenEndpointCannotBeVerified(t *testing.T) {
	// The port disappears when the driver is switched.
	out := runNative(t, `$global:addDriver = 'Example Printer 100'; function Set-Printer { [CmdletBinding()]param($Name,$DriverName) $global:set += $DriverName; ($global:printers | Where-Object { $_.Name -eq $Name }).DriverName = $DriverName; $global:ports = @() }; `)
	if !strings.Contains(out, "skipped") || !strings.Contains(out, "restored Microsoft IPP Class Driver") || !strings.Contains(out, "DRIVER:Microsoft IPP Class Driver|SET:Example Printer 100,Microsoft IPP Class Driver") {
		t.Fatal(out)
	}
}

func TestNativeDriverStepLeavesOtherDriversAlone(t *testing.T) {
	out := runNative(t, `$global:printers[0].DriverName = 'Vendor Driver'; `)
	if !strings.Contains(out, "in-use Vendor Driver") || !strings.Contains(out, "SET:\r\n") && !strings.Contains(out, "SET:\n") {
		t.Fatal(out)
	}
}

func TestIPPCreateAcceptsOnlyClassOrModelNamedDriver(t *testing.T) {
	plan := nativeTestPlan(t)
	reuse := func(driver string) (string, error) {
		setup := `$global:printers = @([PSCustomObject]@{Name='Office';DriverName='` + driver + `';PortName='ipp://192.0.2.10/ipp/print'}); $global:ports = @([PSCustomObject]@{Name='ipp://192.0.2.10/ipp/print';PortMonitor='Internet Port';PrinterHostAddress=$null}); `
		return runPowerShell(context.Background(), ippPrinterHarness+setup+plan.Commands[0])
	}
	if out, err := reuse("Example Printer 100"); err != nil || !strings.Contains(out, "Unchanged IPP printer") {
		t.Fatalf("upgraded queue rejected: %v %s", err, out)
	}
	if out, err := reuse("Some Other Printer"); err == nil || !strings.Contains(out, "different driver") {
		t.Fatalf("unrelated driver accepted: %v %s", err, out)
	}
}

func TestNativeDriverStepFailedRestoreNeedsAttention(t *testing.T) {
	out := runNative(t, `$global:addDriver = 'Example Printer 100'; function Set-Printer { [CmdletBinding()]param($Name,$DriverName) $global:set += $DriverName; if ($DriverName -eq 'Microsoft IPP Class Driver') { throw 'spooler stopped' }; ($global:printers | Where-Object { $_.Name -eq $Name }).DriverName = $DriverName; $global:ports = @() }; `)
	if !strings.Contains(out, "SPOOLSMITH-NATIVE-DRIVER: attention") || !strings.Contains(out, "could not restore Microsoft IPP Class Driver: spooler stopped") {
		t.Fatal(out)
	}
}

func TestNativeDriverStepDoesNotSwitchAQueueThatMovedDuringTheSearch(t *testing.T) {
	// The port no longer shows the reviewed endpoint when the step re-checks it.
	out := runNative(t, `$global:addDriver = 'Example Printer 100'; $global:ports = @([PSCustomObject]@{Name='ipp://192.0.2.99/ipp/print';PortMonitor='Internet Port';PrinterHostAddress=$null}); $global:printers[0].PortName = 'ipp://192.0.2.99/ipp/print'; `)
	if !strings.Contains(out, "skipped IPP endpoint could not be verified") || !strings.Contains(out, "SET:\r\n") && !strings.Contains(out, "SET:\n") {
		t.Fatalf("queue on another endpoint was changed: %s", out)
	}
}

func TestNativeDriverStepIgnoresUnusableModelName(t *testing.T) {
	plan := nativeTestPlan(t)
	plan.NativeDriverModel = "打印机"
	out, err := runPowerShell(context.Background(), nativeHarness+`$global:addDriver = ''; `+nativeDriverScript(plan)+`; "SET:$($global:set -join ',')"`)
	if err != nil || !strings.Contains(out, "no usable model name") || strings.Contains(out, "Windows Update unavailable") {
		t.Fatalf("%v %s", err, out)
	}
}
