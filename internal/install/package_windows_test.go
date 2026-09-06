//go:build windows

package install

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPowerShellPackageAlreadyRegisteredNeverOpensArchive(t *testing.T) {
	p := brotherProfile()
	command, err := packageCommand(*p.DriverPackage, p.DriverName)
	if err != nil {
		t.Fatal(err)
	}
	harness := `function Get-PrinterDriver { [CmdletBinding()]param() [PSCustomObject]@{Name='Brother HL-L2315D series'} }; function Get-FileHash { throw 'UNEXPECTED ARCHIVE READ' }; `
	out, err := runPowerShell(context.Background(), harness+command)
	if err != nil || !strings.Contains(out, "Unchanged driver") {
		t.Fatalf("%v %s", err, out)
	}
}

func TestPowerShellPackageHashMismatchStopsBeforeStaging(t *testing.T) {
	p := brotherProfile()
	p.DriverPackage.Archive = filepath.Join(t.TempDir(), "driver.EXE")
	if err := os.WriteFile(p.DriverPackage.Archive, []byte("invalid archive"), 0600); err != nil {
		t.Fatal(err)
	}
	command, err := packageCommand(*p.DriverPackage, p.DriverName)
	if err != nil {
		t.Fatal(err)
	}
	harness := `function Write-Error { param($Message) [Console]::Error.WriteLine($Message) }; function Get-PrinterDriver { [CmdletBinding()]param() @() }; function Get-FileHash { [CmdletBinding()]param($LiteralPath,$Algorithm) [PSCustomObject]@{Hash='WRONG'} }; function Get-AuthenticodeSignature { throw 'UNEXPECTED SIGNATURE READ' }; `
	out, err := runPowerShell(context.Background(), harness+command)
	if err == nil || !strings.Contains(out, "SHA-256 mismatch") || strings.Contains(out, "UNEXPECTED SIGNATURE READ") {
		t.Fatalf("%v %s", err, out)
	}
}

// The optional local vendor archive exercises real hashing, signature checks and
// extraction. Both Windows mutation commands are replaced by in-memory doubles.
func TestLocalBrotherArchiveVerificationWithStagingDoubles(t *testing.T) {
	archive, err := filepath.Abs("../../profiles/.packages/brother/Y14A_C1-hostm-1110.EXE")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(archive); err != nil {
		t.Skip("local vendor archive unavailable")
	}
	p := brotherProfile()
	p.DriverPackage.Archive = archive
	command, err := packageCommand(*p.DriverPackage, p.DriverName)
	if err != nil {
		t.Fatal(err)
	}
	harness := `$env:TEMP = ` + powerShellString(t.TempDir()) + `;
$global:registered = $false; $global:staged = 0;
function Get-PrinterDriver { [CmdletBinding()]param() if ($global:registered) { [PSCustomObject]@{Name='Brother HL-L2315D series'} } };
function pnputil.exe { if ($args[0] -ne '/add-driver' -or -not (Test-Path -LiteralPath $args[1])) { throw 'Wrong staging input' }; $global:staged++; $global:LASTEXITCODE=0 };
function Add-PrinterDriver { [CmdletBinding()]param($Name) if ($Name -ne 'Brother HL-L2315D series') { throw 'Wrong driver name' }; $global:registered=$true };
`
	out, err := runPowerShell(context.Background(), harness+command+"; "+command+"; if ($global:staged -ne 1) { throw 'Staged more than once' }")
	if err != nil || !strings.Contains(out, "Registered driver") || !strings.Contains(out, "Unchanged driver") {
		t.Fatalf("local package verification: %v\n%s", err, out)
	}
}
