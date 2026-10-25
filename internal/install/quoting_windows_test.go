//go:build windows

package install

import (
	"context"
	"strings"
	"testing"
)

// The string-shape test in plan_test.go checks powerShellString against the
// escapes its author expected. This one asks the real PowerShell tokenizer:
// every hostile value must come back byte-for-byte as data and nothing in it
// may run. PowerShell treats four double-quote characters (" U+201C U+201D
// U+201E) as string delimiters, so each one is exercised as a break-out.
func TestPowerShellStringRoundTripsHostileValuesThroughRealPowerShell(t *testing.T) {
	values := []string{
		`plain`,
		`a"; Write-Output 'INJECTED'; "b`,
		"a“; Write-Output 'INJECTED'; “b",
		"a”; Write-Output 'INJECTED'; ”b",
		"a„; Write-Output 'INJECTED'; „b",
		"a‘’‚‛'b",
		"a$(Write-Output INJECTED)b",
		"a${env:COMPUTERNAME}b",
		"a`; Write-Output INJECTED; `b",
		"a`„; Write-Output INJECTED; „b",
		"Büro Drucker 打印机   ✓",
		strings.Repeat("Long name ", 25),
	}
	for _, value := range values {
		script := "$v = " + powerShellString(value) + "; [Console]::Out.Write('<<' + $v + '>>')"
		output, err := runPowerShell(context.Background(), script)
		if err != nil {
			t.Errorf("value %q: PowerShell failed: %v\n%s", value, err, output)
			continue
		}
		// Exact equality is the injection check: an executed Write-Output
		// would add a line and break the value apart.
		want := "<<" + value + ">>"
		if strings.TrimSpace(output) != want {
			t.Errorf("value %q did not round-trip as data:\n got %q\nwant %q", value, strings.TrimSpace(output), want)
		}
	}
}

// The driver-presence check runs during preview (`apply --dry-run`, the
// desktop review sheet) before any confirmation, with the driver name taken
// straight from the bundle. A hostile name must stay a name. The stub returns
// one driver so the Where-Object filter (where the name lands) really runs.
func TestDriverPresenceCheckDoesNotRunCodeFromABundleDriverName(t *testing.T) {
	for _, quote := range []string{"\"", "“", "”", "„"} {
		name := "Evil" + quote + "; Set-Variable -Scope Global -Name pwned -Value PWNED; " + quote
		command, err := driverPresenceCommand(name)
		if err != nil {
			t.Fatal(err)
		}
		script := "function Get-PrinterDriver { [CmdletBinding()]param() [PSCustomObject]@{Name='Other driver'} }; " + command + "; if ($global:pwned) { $global:pwned }"
		output, err := runPowerShell(context.Background(), script)
		if err != nil || strings.Contains(output, "PWNED") || strings.TrimSpace(output) != "False" {
			t.Errorf("quote %q: driver name was not treated as data: err=%v output=%q", quote, err, output)
		}
	}
}
