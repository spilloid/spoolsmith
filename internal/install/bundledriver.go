package install

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// BundleDriver describes a driver payload carried inside a bundle, in the
// stable terms that appear in a plan.
//
// Every field here is derived from the bundle's own content, never from the
// machine applying it. That is what lets one reviewed plan be named by
// fingerprint and reused across a fleet: the same bundle yields the same plan
// text everywhere, and any difference in the payload changes the plan.
type BundleDriver struct {
	WindowsDriverName string `json:"windows_driver_name"`
	PublishedName     string `json:"published_name,omitempty"`
	SourceHost        string `json:"source_host,omitempty"`
	// INF is the payload-relative path to the INF that will be staged.
	INF string `json:"inf"`
	// StageDirName is the deterministic extraction directory's base name,
	// resolved against the Windows temp directory at execution time.
	StageDirName  string `json:"stage_dir_name"`
	FileCount     int    `json:"file_count"`
	TotalBytes    int64  `json:"total_bytes"`
	PayloadDigest string `json:"payload_digest"`
}

// Validate checks the fields that end up inside generated PowerShell.
func (b BundleDriver) Validate() error {
	for _, item := range []struct{ field, value string }{
		{"bundle driver name", b.WindowsDriverName},
		{"bundle INF path", b.INF},
		{"bundle staging directory", b.StageDirName},
		{"bundle payload digest", b.PayloadDigest},
		{"bundle published name", b.PublishedName},
		{"bundle source host", b.SourceHost},
	} {
		if err := validatePlanValue(item.field, item.value); err != nil {
			return err
		}
	}
	for _, item := range []struct{ field, value string }{
		{"bundle driver name", b.WindowsDriverName},
		{"bundle INF path", b.INF},
		{"bundle staging directory", b.StageDirName},
	} {
		if strings.TrimSpace(item.value) == "" {
			return fmt.Errorf("install: %s is required", item.field)
		}
	}
	if strings.ContainsAny(b.StageDirName, `\/:`) {
		return errors.New("install: bundle staging directory must be a single directory name")
	}
	if !strings.EqualFold(filepath.Ext(b.INF), ".inf") {
		return fmt.Errorf("install: bundle INF path %q is not an .inf file", b.INF)
	}
	return nil
}

// PublisherTrust is the plan's statement of the one trust-store change a
// bundled driver may need. Windows refuses to stage a package whose catalog
// signature is valid but whose signer is not yet in the machine's Trusted
// Publishers store, so without this a portable bundle cannot reproduce a setup
// on a clean PC.
//
// It states the rule, never the outcome on one machine: whether the signer is
// already trusted differs per PC, and recording that here would make the same
// bundle fingerprint differently across a fleet. Execution is idempotent.
type PublisherTrust struct {
	Source              string `json:"source"`
	Store               string `json:"store"`
	OnlyIfMissing       bool   `json:"only_if_missing"`
	RequireValidCatalog bool   `json:"require_valid_catalog"`
}

func bundlePublisherTrust() *PublisherTrust {
	return &PublisherTrust{
		Source:              "signer certificate of each catalog the driver INF names, only when Windows reports its signature Valid",
		Store:               `LocalMachine\TrustedPublisher`,
		OnlyIfMissing:       true,
		RequireValidCatalog: true,
	}
}

// bundleDriverCommand stages a bundle's driver payload if, and only if, the
// driver is not already registered.
//
// The trust chain here is deliberately narrower than the reviewed vendor-
// archive path and must not be described as equivalent. There is no pinned
// vendor hash and no vendor Authenticode check, because a bundle's payload
// came out of an operator's own driver store rather than from a vendor
// download. SpoolSmith verified every payload byte against the bundle manifest
// before this command runs, and Windows itself enforces driver signing when
// pnputil stages the INF. SpoolSmith does not pre-reject a payload Windows
// might accept: an unsigned or non-validating catalog is reported and pnputil
// decides. A catalog whose signature does not match its own content
// (HashMismatch) still stops here, because that is evidence of modification.
//
// Publisher trust: a catalog can validate and still be refused by pnputil
// because its signer is not in LocalMachine\TrustedPublisher. This command
// closes that gap, and these rules are the security boundary:
//   - Only catalogs the INF names in its [Version] CatalogFile entries, in the
//     INF's own directory, are considered. Other .cat files in the payload are
//     ignored, so an unrelated catalog can never widen trust. Several named
//     catalogs are all the INF's own, so each valid signer among them is
//     trusted; there is no ambiguity to resolve.
//   - Only the SignerCertificate Get-AuthenticodeSignature returns for a
//     catalog whose Status is Valid is eligible. Nothing from the bundle
//     manifest, no bundled .cer file and no subject string ever is. A bundle is
//     tamper-evident, not authenticated; Valid means Windows chained the signer
//     to a root this PC already trusts, which a bundle cannot fake.
//   - Matching is by exact thumbprint. Another certificate with the same
//     subject does not count as trusted.
//   - A missing entry is added directly through X509Store, with no .cer file
//     written, and re-checked before pnputil runs.
//   - Nothing is ever added to the Root store, so a self-signed or privately
//     rooted catalog is never made to validate.
func bundleDriverCommand(b BundleDriver) (string, error) {
	if err := b.Validate(); err != nil {
		return "", err
	}
	infRelative := filepath.FromSlash(b.INF)
	script := fmt.Sprintf(`$drivers = @(Get-PrinterDriver -ErrorAction Stop | Where-Object { $_.Name -eq %[1]s });
if ($drivers.Count -gt 0) { 'Unchanged driver' } else {
 $stage = Join-Path ([IO.Path]::GetTempPath()) %[2]s;
 $inf = Join-Path $stage %[3]s;
 if (-not (Test-Path -LiteralPath $inf -PathType Leaf)) { throw 'Verified driver payload is not staged where this plan expects it; re-run apply so the bundle is extracted, and check that TEMP is the same for this process' };
 $infDir = Split-Path -Parent $inf; $section = '';
 $declared = @(foreach ($line in @(Get-Content -LiteralPath $inf -ErrorAction Stop)) {
  if ($line -match '^\s*\[\s*([^\]]+?)\s*\]') { $section = $matches[1] }
  elseif ($section -eq 'Version' -and $line -match '^\s*CatalogFile(\.[^=\s]+)?\s*=\s*"?([^";]+?)"?\s*(;.*)?$') { $matches[2] }
 });
 $catalogs = @();
 foreach ($name in @($declared | Sort-Object -Unique)) {
  if ($name -match '[\\/:]') { throw ('Driver INF names catalog ' + $name + ' outside its own directory; SpoolSmith will not use it') };
  $path = Join-Path $infDir $name;
  if (Test-Path -LiteralPath $path -PathType Leaf) { $catalogs += $path } else { Write-Output ('Catalog ' + $name + ' named by the INF is not in the payload') }
 };
 if ($catalogs.Count -eq 0) { Write-Output 'The driver INF names no catalog present in the payload. SpoolSmith is not changing publisher trust; Windows decides whether it accepts this driver package.' };
 $signers = @{};
 foreach ($catalog in $catalogs) {
  $leaf = Split-Path -Leaf $catalog;
  $signature = Get-AuthenticodeSignature -LiteralPath $catalog -ErrorAction Stop;
  if ([string]$signature.Status -eq 'HashMismatch') { throw ('Catalog signature validation failed: ' + $leaf + ' does not match its own signature, so the payload was modified after signing. Nothing was staged.') };
  if ([string]$signature.Status -ne 'Valid') { Write-Output ('Catalog ' + $leaf + ' signature status: ' + $signature.Status + '. SpoolSmith will not trust its publisher; Windows decides whether it accepts this driver package.'); continue };
  $signer = $signature.SignerCertificate;
  if ($null -eq $signer -or [string]$signer.Thumbprint -notmatch '^[0-9A-Fa-f]{40}$') { throw ('Catalog ' + $leaf + ' reports a valid signature but no usable signer certificate; SpoolSmith will not establish publisher trust from it') };
  Write-Output ('Catalog ' + $leaf + ' signed by: ' + $signer.Subject);
  $signers[([string]$signer.Thumbprint).ToUpperInvariant()] = $signer
 };
 $nl = [Environment]::NewLine;
 foreach ($thumbprint in @($signers.Keys | Sort-Object)) {
  $signer = $signers[$thumbprint];
  $trustPath = 'Cert:\LocalMachine\TrustedPublisher\' + $thumbprint;
  if (Test-Path -LiteralPath $trustPath) { Write-Output ('Driver publisher already trusted: ' + $signer.Subject + ' (' + $thumbprint + ')') } else {
   $failure = 'SpoolSmith could not establish publisher trust for:' + $nl + '  Subject: ' + $signer.Subject + $nl + '  Thumbprint: ' + $thumbprint + $nl + 'The catalog signature is valid, but Windows does not trust this publisher for driver installation.';
   $store = New-Object -TypeName System.Security.Cryptography.X509Certificates.X509Store -ArgumentList 'TrustedPublisher', 'LocalMachine';
   try { $store.Open('ReadWrite'); $store.Add($signer) } catch { throw ($failure + $nl + $_.Exception.Message) } finally { $store.Close() };
   if (-not (Test-Path -LiteralPath $trustPath)) { throw $failure };
   Write-Output ('Trusted driver publisher: ' + $signer.Subject + ' (' + $thumbprint + ')')
  }
 };
 $staging = @(& pnputil.exe /add-driver $inf); $code = $LASTEXITCODE;
 $staging;
 if ($code -ne 0 -and $code -ne 3010) {
  $text = @($staging | Where-Object { $_ }) -join ' ';
  $reason = if ($text -match 'not yet been established as trusted') { 'valid driver publisher is not trusted by this PC' } elseif ($text -match 'digital signature|digitally signed|not present in the specified catalog|root certificate which is not trusted|signature') { 'Windows could not validate the driver package signature' } elseif ($text -match 'architecture|platform') { 'driver package is not compatible with this PC''s architecture' } elseif ($text -match 'syntax|INF .*invalid|invalid INF|malformed') { 'driver package INF is malformed' } else { 'Windows rejected the driver package' };
  throw ('Windows driver-store staging failed (pnputil exit ' + $code + '): ' + $reason + '. pnputil output: ' + $text)
 };
 'Driver package staged';
 try { Add-PrinterDriver -Name %[1]s -ErrorAction Stop } catch { throw ('Printer driver registration failed after the package was staged: ' + $_.Exception.Message) };
 $drivers = @(Get-PrinterDriver -ErrorAction Stop | Where-Object { $_.Name -eq %[1]s });
 if ($drivers.Count -eq 0) { throw 'Driver registration could not be verified' };
 'Registered driver from bundle'
}`, powerShellString(b.WindowsDriverName), powerShellString(b.StageDirName), powerShellString(infRelative))
	return powerShellCommand(script), nil
}
