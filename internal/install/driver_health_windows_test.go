//go:build windows

package install

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Use actual files: a driver-list double alone cannot reproduce a stale
// registration that points at a missing configuration DLL.
func healthyDriverHarness(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, name := range []string{"driver.dll", "config.dll", "data.gpd", "dependency.dll"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("test driver file"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return "$env:PROCESSOR_ARCHITECTURE = 'AMD64'; $env:PROCESSOR_ARCHITEW6432 = ''; " +
		"function New-TestPrinterDriver([string]$Name) { [PSCustomObject]@{Name=$Name; PrinterEnvironment='Windows x64'; MajorVersion=3; Path=" + powerShellString(filepath.Join(dir, "driver.dll")) +
		"; ConfigFile='config.dll'; DataFile='data.gpd'; DependentFiles=@('dependency.dll')} }; "
}

func TestDriverPresenceRequiresNativeEnvironmentAndFiles(t *testing.T) {
	for _, c := range []struct {
		name, change, want string
		setup              string
	}{
		{"healthy", "", "True", ""},
		{"missing configuration DLL", "$d.ConfigFile = 'missing.dll';", "False", ""},
		{"missing dependency", "$d.DependentFiles = @('missing.dll');", "False", ""},
		{"missing data file", "$d.DataFile = 'missing.gpd';", "False", ""},
		{"missing driver binary", "$d.Path = 'missing.dll';", "False", ""},
		{"wrong environment", "$d.PrinterEnvironment = 'Windows NT x86';", "False", ""},
		{"x86 Windows", "$d.PrinterEnvironment = 'Windows NT x86';", "True", "$env:PROCESSOR_ARCHITECTURE = 'x86'; "},
		{"32-bit process on x64 Windows", "", "True", "$env:PROCESSOR_ARCHITECTURE = 'x86'; $env:PROCESSOR_ARCHITEW6432 = 'AMD64'; "},
		{"ARM64 Windows", "$d.PrinterEnvironment = 'Windows ARM64';", "True", "$env:PROCESSOR_ARCHITECTURE = 'ARM64'; "},
		{"ambiguous native registrations", "$d;", "False", ""},
		{"v3 without configuration DLL", "$d.ConfigFile = $null;", "False", ""},
		{"v4 without configuration DLL", "$d.MajorVersion = 4; $d.ConfigFile = $null;", "True", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			command, err := driverPresenceCommand("Test Driver [1]")
			if err != nil {
				t.Fatal(err)
			}
			harness := healthyDriverHarness(t) + c.setup + "function Get-PrinterDriver { [CmdletBinding()]param() $d = New-TestPrinterDriver 'Test Driver [1]'; " + c.change + " $d }; "
			out, err := runPowerShell(context.Background(), harness+command)
			if err != nil || strings.TrimSpace(out) != c.want {
				t.Fatalf("presence = %q, %v; want %s", out, err, c.want)
			}
		})
	}
}

// The cmdlet's binary property is Path, not Win32_PrinterDriver.DriverPath.
// Check an actual installed inbox driver as well as the file-based doubles so
// a mistaken CIM property cannot make every driver look incomplete. Read only:
// no driver installation, UI loading, queue changes or physical printer needed.
func TestDriverPresenceRecognizesInstalledInboxDriver(t *testing.T) {
	query := powerShellCommand(`$candidates = @(Get-PrinterDriver -ErrorAction Stop | Where-Object {
 $_.PrinterEnvironment -eq 'Windows x64' -and $_.Name -in @('Microsoft Print To PDF', 'Microsoft XPS Document Writer v4', 'Microsoft PCL6 Class Driver')
 } | Sort-Object Name); if ($candidates.Count -gt 0) { $candidates[0].Name }`)
	out, err := runPowerShell(context.Background(), query)
	if err != nil {
		t.Skipf("Windows printer inventory is unavailable: %v", err)
	}
	name := strings.TrimSpace(out)
	if name == "" {
		t.Skip("no supported x64 inbox test driver is registered")
	}
	present, err := checkDriverPresent(context.Background(), runPowerShell, name)
	if err != nil || !present {
		t.Fatalf("installed inbox driver %q rejected: present=%t err=%v", name, present, err)
	}
	t.Logf("verified installed inbox driver %q", name)
}
