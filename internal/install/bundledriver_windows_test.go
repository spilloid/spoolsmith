//go:build windows

package install

import (
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"
)

const (
	hpThumbprint    = "0123456789ABCDEF0123456789ABCDEF01234567"
	otherThumbprint = "89ABCDEF0123456789ABCDEF0123456789ABCDEF"
)

// bundleTrustHarness replaces every cmdlet the staging script touches that
// would read or change real machine state: the printer driver list, catalog
// signatures, the Cert: drive, the certificate store and pnputil. Each double
// prints an EVENT line so a test can assert what happened and in what order,
// including on paths where the script exits with an error.
//
// The pnputil double behaves like Windows: when $global:requireTrust is set it
// refuses the package unless the HP signer's exact thumbprint is trusted.
const bundleTrustHarness = `
function Write-Error { param($Message) [Console]::Error.WriteLine($Message) };
function Get-PrinterDriver { [CmdletBinding()]param() if ($global:registered) { [PSCustomObject]@{Name='HP Universal Printing PCL 6'} } };
function Get-AuthenticodeSignature { [CmdletBinding()]param($LiteralPath)
 $leaf = Split-Path -Leaf $LiteralPath;
 if (-not $global:sigs.ContainsKey($leaf)) { throw ('UNEXPECTED SIGNATURE READ ' + $leaf) };
 [Console]::Out.WriteLine('EVENT:sig:' + $leaf); $global:sigs[$leaf] };
function Test-Path { [CmdletBinding()]param([string]$LiteralPath, $PathType)
 if ($LiteralPath -like 'Cert:*') {
  if ($LiteralPath -notlike 'Cert:\LocalMachine\TrustedPublisher\*') { throw ('WRONG STORE ' + $LiteralPath) };
  return ($global:trusted -contains ($LiteralPath -split '\\')[-1]) };
 Microsoft.PowerShell.Management\Test-Path @PSBoundParameters };
function New-Object { param($TypeName, $ArgumentList)
 if ($TypeName -ne 'System.Security.Cryptography.X509Certificates.X509Store') { throw ('UNEXPECTED TYPE ' + $TypeName) };
 if (($ArgumentList -join ',') -ne 'TrustedPublisher,LocalMachine') { throw ('WRONG STORE ' + ($ArgumentList -join ',')) };
 $store = [PSCustomObject]@{};
 $store | Add-Member -MemberType ScriptMethod -Name Open -Value { param($flags) if ($flags -ne 'ReadWrite') { throw 'WRONG MODE' } };
 $store | Add-Member -MemberType ScriptMethod -Name Add -Value { param($cert)
  if ($global:storeFails) { throw 'Access is denied.' };
  [Console]::Out.WriteLine('EVENT:trust:' + $cert.Thumbprint);
  if (-not $global:storeDropsAdds) { $global:trusted += $cert.Thumbprint } };
 $store | Add-Member -MemberType ScriptMethod -Name Close -Value { };
 $store };
function pnputil.exe {
 [Console]::Out.WriteLine('EVENT:pnputil');
 if ($global:pnputilFails) { $global:pnputilFails; $global:LASTEXITCODE = 1; return };
 if ($global:requireTrust -and -not ($global:trusted -contains '` + hpThumbprint + `')) {
  'Failed to add driver package: The publisher of an Authenticode(tm) signed catalog has not yet been established as trusted.'; $global:LASTEXITCODE = 1; return };
 'Driver package added successfully.'; $global:LASTEXITCODE = $global:pnputilExit };
function Add-PrinterDriver { [CmdletBinding()]param($Name)
 if ($Name -ne 'HP Universal Printing PCL 6') { throw 'Wrong driver name' };
 [Console]::Out.WriteLine('EVENT:register'); $global:registered = $true };
$global:registered = $false; $global:trusted = @(); $global:sigs = @{};
$global:storeFails = $false; $global:storeDropsAdds = $false; $global:requireTrust = $true;
$global:pnputilFails = $null; $global:pnputilExit = 0;
`

func validSignature(subject, thumbprint string) string {
	return "[PSCustomObject]@{Status='Valid'; SignerCertificate=[PSCustomObject]@{Subject='" + subject + "'; Thumbprint='" + thumbprint + "'}}"
}

// utf16INF encodes an INF the way many vendor INFs ship: UTF-16LE with a BOM.
func utf16INF(text string) []byte {
	units := utf16.Encode([]rune(text))
	out := []byte{0xFF, 0xFE}
	for _, unit := range units {
		out = binary.LittleEndian.AppendUint16(out, unit)
	}
	return out
}

const hpINF = "; HP Universal Printing\r\n[Version]\r\nSignature=\"$Windows NT$\"\r\nClass=Printer\r\nCatalogFile.NTamd64 = hpcu360u.cat ; x64 catalog\r\n\r\n[Manufacturer]\r\n\"HP\"=HP,NTamd64\r\n; CatalogFile=not-in-version.cat must be ignored outside [Version]\r\n[Strings]\r\nCatalogFile=decoy.cat\r\n"

type bundleTrustCase struct {
	name   string
	inf    []byte
	files  []string
	setup  string
	wantOK bool
	// wantEvents is the exact ordered EVENT sequence the run must produce.
	wantEvents []string
	wantOutput []string
}

func runBundleTrustCase(t *testing.T, c bundleTrustCase, repeat bool) string {
	t.Helper()
	sandbox := t.TempDir()
	payload := BundleDriver{WindowsDriverName: "HP Universal Printing PCL 6", INF: "driver/hpcu360u.inf", StageDirName: "SpoolSmith-bundle-test", PayloadDigest: "38d8dd0d5fee6659cb353013ab40e936"}
	dir := filepath.Join(sandbox, payload.StageDirName, "driver")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	inf := c.inf
	if inf == nil {
		inf = utf16INF(hpINF)
	}
	if err := os.WriteFile(filepath.Join(dir, "hpcu360u.inf"), inf, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, name := range c.files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("catalog"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	command, err := bundleDriverCommand(payload)
	if err != nil {
		t.Fatal(err)
	}
	script := "$env:TMP = " + powerShellString(sandbox) + "; $env:TEMP = " + powerShellString(sandbox) + ";" + bundleTrustHarness + c.setup + "\n" + command
	if repeat {
		script += "; " + command
	}
	out, runErr := runPowerShell(context.Background(), script)
	if (runErr == nil) != c.wantOK {
		t.Fatalf("run error = %v, want success %t\n%s", runErr, c.wantOK, out)
	}
	var events []string
	for _, line := range strings.Split(out, "\n") {
		if event, ok := strings.CutPrefix(strings.TrimSpace(line), "EVENT:"); ok {
			events = append(events, event)
		}
	}
	if strings.Join(events, ",") != strings.Join(c.wantEvents, ",") {
		t.Fatalf("events = %v, want %v\n%s", events, c.wantEvents, out)
	}
	for _, want := range c.wantOutput {
		if !strings.Contains(out, want) {
			t.Fatalf("output is missing %q:\n%s", want, out)
		}
	}
	return out
}

func TestBundleDriverPublisherTrust(t *testing.T) {
	hpSig := "$global:sigs['hpcu360u.cat'] = " + validSignature("CN=HP Inc.", hpThumbprint) + ";"
	for _, c := range []bundleTrustCase{
		{
			// The real failure: a Valid catalog whose publisher this PC has
			// never trusted. Without trust, the pnputil double refuses it.
			name:       "valid but untrusted signer is trusted before staging",
			files:      []string{"hpcu360u.cat"},
			setup:      hpSig,
			wantOK:     true,
			wantEvents: []string{"sig:hpcu360u.cat", "trust:" + hpThumbprint, "pnputil", "register"},
			wantOutput: []string{"Catalog hpcu360u.cat signed by: CN=HP Inc.", "Trusted driver publisher: CN=HP Inc. (" + hpThumbprint + ")", "Driver package staged", "Registered driver from bundle"},
		},
		{
			name:       "already trusted signer adds nothing",
			files:      []string{"hpcu360u.cat"},
			setup:      hpSig + "$global:trusted = @('" + hpThumbprint + "');",
			wantOK:     true,
			wantEvents: []string{"sig:hpcu360u.cat", "pnputil", "register"},
			wantOutput: []string{"Driver publisher already trusted: CN=HP Inc."},
		},
		{
			// Same subject, different certificate: a subject match must not
			// count, or a stale or look-alike certificate would block the add.
			name:       "same subject with another thumbprint does not count",
			files:      []string{"hpcu360u.cat"},
			setup:      hpSig + "$global:trusted = @('" + otherThumbprint + "');",
			wantOK:     true,
			wantEvents: []string{"sig:hpcu360u.cat", "trust:" + hpThumbprint, "pnputil", "register"},
		},
		{
			name:       "modified catalog fails before trust and staging",
			files:      []string{"hpcu360u.cat"},
			setup:      "$global:sigs['hpcu360u.cat'] = [PSCustomObject]@{Status='HashMismatch'; SignerCertificate=[PSCustomObject]@{Subject='CN=HP Inc.'; Thumbprint='" + hpThumbprint + "'}};",
			wantEvents: []string{"sig:hpcu360u.cat"},
			wantOutput: []string{"Catalog signature validation failed"},
		},
		{
			name:       "valid status without a signer certificate fails closed",
			files:      []string{"hpcu360u.cat"},
			setup:      "$global:sigs['hpcu360u.cat'] = [PSCustomObject]@{Status='Valid'; SignerCertificate=$null};",
			wantEvents: []string{"sig:hpcu360u.cat"},
			wantOutput: []string{"no usable signer certificate"},
		},
		{
			name:       "trust store refusal stops before staging",
			files:      []string{"hpcu360u.cat"},
			setup:      hpSig + "$global:storeFails = $true;",
			wantEvents: []string{"sig:hpcu360u.cat"},
			wantOutput: []string{"SpoolSmith could not establish publisher trust for:", "Subject: CN=HP Inc.", "Thumbprint: " + hpThumbprint, "Access is denied."},
		},
		{
			name:       "unverified trust store addition stops before staging",
			files:      []string{"hpcu360u.cat"},
			setup:      hpSig + "$global:storeDropsAdds = $true;",
			wantEvents: []string{"sig:hpcu360u.cat", "trust:" + hpThumbprint},
			wantOutput: []string{"SpoolSmith could not establish publisher trust for:"},
		},
		{
			// Unsigned drivers are Windows' call, not SpoolSmith's: the publisher
			// is never trusted, pnputil still gets to decide.
			name:       "unsigned catalog is never trusted but Windows decides",
			files:      []string{"hpcu360u.cat"},
			setup:      "$global:sigs['hpcu360u.cat'] = [PSCustomObject]@{Status='NotSigned'; SignerCertificate=$null}; $global:requireTrust = $false;",
			wantOK:     true,
			wantEvents: []string{"sig:hpcu360u.cat", "pnputil", "register"},
			wantOutput: []string{"signature status: NotSigned. SpoolSmith will not trust its publisher"},
		},
		{
			name:       "untrusted root is never trusted and Windows refusal is classified",
			files:      []string{"hpcu360u.cat"},
			setup:      "$global:sigs['hpcu360u.cat'] = [PSCustomObject]@{Status='UnknownError'; SignerCertificate=[PSCustomObject]@{Subject='CN=Self'; Thumbprint='" + otherThumbprint + "'}}; $global:pnputilFails = 'A certificate chain processed, but terminated in a root certificate which is not trusted by the trust provider.';",
			wantEvents: []string{"sig:hpcu360u.cat", "pnputil"},
			wantOutput: []string{"Windows could not validate the driver package signature", "root certificate which is not trusted"},
		},
		{
			// A .cat the INF does not name is never read, so it can never widen
			// trust; the harness throws if its signature is requested.
			name:       "catalog the INF does not name is ignored",
			files:      []string{"hpcu360u.cat", "unrelated.cat", "decoy.cat", "not-in-version.cat"},
			setup:      hpSig,
			wantOK:     true,
			wantEvents: []string{"sig:hpcu360u.cat", "trust:" + hpThumbprint, "pnputil", "register"},
		},
		{
			name:       "every valid signer the INF names is trusted",
			inf:        []byte("[Version]\r\nCatalogFile = a.cat\r\nCatalogFile.NTamd64 = \"b.cat\"\r\n"),
			files:      []string{"a.cat", "b.cat"},
			setup:      hpSig + "$global:sigs['a.cat'] = " + validSignature("CN=HP Inc.", hpThumbprint) + "; $global:sigs['b.cat'] = " + validSignature("CN=Other", otherThumbprint) + ";",
			wantOK:     true,
			wantEvents: []string{"sig:a.cat", "sig:b.cat", "trust:" + hpThumbprint, "trust:" + otherThumbprint, "pnputil", "register"},
		},
		{
			name:       "INF naming no catalog leaves the decision to Windows",
			inf:        []byte("[Version]\r\nClass=Printer\r\n"),
			files:      []string{"hpcu360u.cat"},
			setup:      "$global:requireTrust = $false;",
			wantOK:     true,
			wantEvents: []string{"pnputil", "register"},
			wantOutput: []string{"names no catalog present in the payload"},
		},
		{
			name:       "catalog path outside the INF directory is refused",
			inf:        []byte("[Version]\r\nCatalogFile=..\\evil.cat\r\n"),
			wantEvents: nil,
			wantOutput: []string{"outside its own directory"},
		},
		{
			name:       "reboot-required staging counts as success",
			files:      []string{"hpcu360u.cat"},
			setup:      hpSig + "$global:pnputilExit = 3010;",
			wantOK:     true,
			wantEvents: []string{"sig:hpcu360u.cat", "trust:" + hpThumbprint, "pnputil", "register"},
		},
		{
			name:       "native pnputil output survives an unclassified failure",
			files:      []string{"hpcu360u.cat"},
			setup:      hpSig + "$global:pnputilFails = 'Something unusual happened (0xDEADBEEF).';",
			wantEvents: []string{"sig:hpcu360u.cat", "trust:" + hpThumbprint, "pnputil"},
			wantOutput: []string{"pnputil exit 1", "Windows rejected the driver package", "Something unusual happened (0xDEADBEEF)."},
		},
		{
			name:       "registration failure is named",
			files:      []string{"hpcu360u.cat"},
			setup:      hpSig + "function Add-PrinterDriver { [CmdletBinding()]param($Name) throw 'The specified driver does not exist.' };",
			wantEvents: []string{"sig:hpcu360u.cat", "trust:" + hpThumbprint, "pnputil"},
			wantOutput: []string{"Printer driver registration failed after the package was staged: The specified driver does not exist."},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			runBundleTrustCase(t, c, false)
		})
	}
}

// A second identical apply changes nothing: the driver is registered, so the
// payload is not re-read, trust is not re-added and pnputil does not run.
func TestBundleDriverPublisherTrustIsIdempotent(t *testing.T) {
	runBundleTrustCase(t, bundleTrustCase{
		files:      []string{"hpcu360u.cat"},
		setup:      "$global:sigs['hpcu360u.cat'] = " + validSignature("CN=HP Inc.", hpThumbprint) + ";",
		wantOK:     true,
		wantEvents: []string{"sig:hpcu360u.cat", "trust:" + hpThumbprint, "pnputil", "register"},
		wantOutput: []string{"Registered driver from bundle", "Unchanged driver"},
	}, true)
}
