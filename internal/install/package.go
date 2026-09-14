package install

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/spilloid/spoolsmith/catalog/packages"
)

// PackageSelection references a reviewed recipe and a locally staged archive.
// Relative archive paths are resolved against the profile's directory on load.
type PackageSelection struct {
	ID      string `json:"id"`
	Archive string `json:"archive"`
}

// PackageSHA256 exposes the reviewed payload pin to local packaging tools.
func (p PackageSelection) PackageSHA256(driver string) (string, error) {
	r, err := p.record(driver)
	return r.SHA256, err
}

type packageRecord struct {
	ID        string `json:"id"`
	SHA256    string `json:"sha256"`
	SourceURL string `json:"source_url"`
	Version   string `json:"driver_version"`
	INF       string `json:"inf_path"`
	Catalog   string `json:"catalog_path"`
	Models    []struct {
		Driver string `json:"windows_driver_name"`
	} `json:"verified_model_entries"`
}

func (p PackageSelection) record(driver string) (packageRecord, error) {
	var r packageRecord
	if err := json.Unmarshal([]byte(packages.BrotherY14), &r); err != nil {
		return r, err
	}
	if p.ID != r.ID {
		return r, fmt.Errorf("package: unsupported recipe %q", p.ID)
	}
	if strings.TrimSpace(p.Archive) == "" {
		return r, errors.New("package: local archive path is required")
	}
	if err := validatePlanValue("package archive", p.Archive); err != nil {
		return r, err
	}
	for _, m := range r.Models {
		if driver == m.Driver {
			return r, nil
		}
	}
	return r, fmt.Errorf("package: recipe %q does not support driver %q", p.ID, driver)
}

func packageCommand(p PackageSelection, driver string) (string, error) {
	r, err := p.record(driver)
	if err != nil {
		return "", err
	}
	archive, err := filepath.Abs(p.Archive)
	if err != nil {
		return "", err
	}
	// Only the pinned archive is extracted. Never execute its vendor bootstrapper.
	// Extraction is private to this attempt; retain it for diagnostics/retry rather
	// than introduce recursive deletion into a privileged install operation.
	// tar is resolved to System32 rather than through PATH: this archive is a
	// self-extracting EXE that only Windows' bsdtar reads, and a developer machine
	// with Git/MSYS on PATH resolves a bare "tar.exe" to GNU tar, which cannot read
	// it and fails the staging path with a message blaming the archive.
	script := fmt.Sprintf(`$drivers = @(Get-PrinterDriver -ErrorAction Stop | Where-Object { $_.Name -eq %[1]s });
if ($drivers.Count -gt 0) { 'Unchanged driver' } else {
 if (-not [Environment]::Is64BitProcess -or $env:PROCESSOR_ARCHITECTURE -ne 'AMD64') { throw 'Package staging currently requires Windows x64' };
 $tar = Join-Path ([Environment]::GetFolderPath('System')) 'tar.exe';
 if (-not (Test-Path -LiteralPath $tar -PathType Leaf)) { throw 'Windows tar.exe was not found in System32; cannot read the driver archive' };
 $archive = %[2]s;
 $archiveLock = [IO.File]::Open($archive, [IO.FileMode]::Open, [IO.FileAccess]::Read, [IO.FileShare]::Read);
 try {
 if ((Get-FileHash -LiteralPath $archive -Algorithm SHA256 -ErrorAction Stop).Hash -ne %[3]s) { throw 'Driver archive SHA-256 mismatch; no driver was staged' };
 $signature = Get-AuthenticodeSignature -LiteralPath $archive -ErrorAction Stop;
 if ($signature.Status -ne 'Valid' -or $signature.SignerCertificate.GetNameInfo([Security.Cryptography.X509Certificates.X509NameType]::SimpleName, $false) -ne 'Brother Industries, Ltd.') { throw 'Driver archive signature is not valid for Brother Industries' };
 $entries = @(& $tar -tf $archive); if ($LASTEXITCODE -ne 0 -or $entries.Count -eq 0) { throw 'Cannot list driver archive' };
 foreach ($entry in $entries) { if ($entry -match '(^[/\\]|:|(^|[/\\])\.\.([/\\]|$))') { throw 'Unsafe archive entry' } };
 $stage = Join-Path ([IO.Path]::GetTempPath()) ('SpoolSmith-driver-' + [Guid]::NewGuid().ToString('N'));
 New-Item -ItemType Directory -Path $stage -ErrorAction Stop | Out-Null;
 Write-Output ('Driver staging directory: ' + $stage);
 & $tar -xf $archive -C $stage; if ($LASTEXITCODE -ne 0) { throw 'Cannot extract driver archive' };
 $inf = Join-Path $stage %[4]s; $cat = Join-Path $stage %[5]s;
 $signature = Get-AuthenticodeSignature -LiteralPath $cat -ErrorAction Stop;
 if ($signature.Status -ne 'Valid' -or $signature.SignerCertificate.GetNameInfo([Security.Cryptography.X509Certificates.X509NameType]::SimpleName, $false) -ne 'Microsoft Windows Hardware Compatibility Publisher') { throw 'Driver catalog signature is invalid' };
 & pnputil.exe /add-driver $inf; if ($LASTEXITCODE -ne 0) { throw 'Windows driver-store staging failed' };
 Add-PrinterDriver -Name %[1]s -ErrorAction Stop;
 $drivers = @(Get-PrinterDriver -ErrorAction Stop | Where-Object { $_.Name -eq %[1]s });
 if ($drivers.Count -eq 0) { throw 'Driver registration could not be verified' };
 'Registered driver'
 } finally { $archiveLock.Dispose() }
}`, powerShellString(driver), powerShellString(archive), powerShellString(r.SHA256), powerShellString(r.INF), powerShellString(r.Catalog))
	return powerShellCommand(script), nil
}
