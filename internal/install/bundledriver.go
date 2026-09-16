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

// bundleDriverCommand stages a bundle's driver payload if, and only if, the
// driver is not already registered.
//
// The trust chain here is deliberately narrower than the reviewed vendor-
// archive path and must not be described as equivalent. There is no pinned
// vendor hash and no vendor Authenticode check, because a bundle's payload
// came out of an operator's own driver store rather than from a vendor
// download. What is checked: SpoolSmith verified every payload byte against
// the bundle manifest before this command runs, this command refuses a payload
// with no catalog file or an invalid catalog signature, and Windows itself
// enforces driver signing when pnputil stages the INF. The catalog's signer is
// printed so it lands in the operator's record rather than being assumed.
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
 $catalogs = @(Get-ChildItem -LiteralPath $stage -Filter *.cat -Recurse -File);
 if ($catalogs.Count -eq 0) { throw 'Driver payload contains no catalog file; SpoolSmith does not stage an unsigned driver' };
 foreach ($catalog in $catalogs) {
  $signature = Get-AuthenticodeSignature -LiteralPath $catalog.FullName -ErrorAction Stop;
  if ($signature.Status -ne 'Valid') { throw ('Driver catalog ' + $catalog.Name + ' has signature status ' + $signature.Status) };
  Write-Output ('Catalog ' + $catalog.Name + ' signed by: ' + $signature.SignerCertificate.Subject)
 };
 & pnputil.exe /add-driver $inf; if ($LASTEXITCODE -ne 0) { throw 'Windows driver-store staging failed' };
 Add-PrinterDriver -Name %[1]s -ErrorAction Stop;
 $drivers = @(Get-PrinterDriver -ErrorAction Stop | Where-Object { $_.Name -eq %[1]s });
 if ($drivers.Count -eq 0) { throw 'Driver registration could not be verified' };
 'Registered driver from bundle'
}`, powerShellString(b.WindowsDriverName), powerShellString(b.StageDirName), powerShellString(infRelative))
	return powerShellCommand(script), nil
}
