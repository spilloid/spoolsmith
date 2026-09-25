<#
.SYNOPSIS
Checks a release ZIP against its published SHA-256 and unpacks its files.

.DESCRIPTION
-From holds spoolsmith-<Tag>-windows-amd64.zip and its .sha256, exactly as
published. -To receives spoolsmith.exe, spoolsmith-gui.exe, README.md and
LICENSE, flattened out of the ZIP's spoolsmith/ folder. Anything else in the
ZIP, or a missing file, is an error.

-VerifySignatures also requires both EXEs to carry valid, timestamped
Authenticode signatures (scripts/verify-signature.ps1).

Used by the MSI workflow so the installer is built only from a payload that is
provably the one the release published.
#>
param(
    [Parameter(Mandatory)][string]$Tag,
    [Parameter(Mandatory)][string]$From,
    [Parameter(Mandatory)][string]$To,
    [switch]$VerifySignatures,
    [string]$ExpectedSubject
)
$ErrorActionPreference = 'Stop'
if ($Tag -notmatch '^v[0-9]+\.[0-9]+\.[0-9]+$') { throw "Invalid release tag '$Tag'" }

$name = "spoolsmith-$Tag-windows-amd64.zip"
$zip = Join-Path $From $name
$sidecar = "$zip.sha256"
foreach ($path in $zip, $sidecar) {
    if (-not (Test-Path $path)) { throw "$path is missing" }
}
$line = (Get-Content $sidecar -Raw).Trim()
if ($line -notmatch '^([0-9a-f]{64})  (\S+)$' -or $Matches[2] -ne $name) {
    throw "$sidecar is not a SHA-256 line for $name"
}
$expected = $Matches[1]
$actual = (Get-FileHash $zip -Algorithm SHA256).Hash.ToLowerInvariant()
if ($actual -ne $expected) { throw "$name does not match its published SHA-256 ($actual, expected $expected)" }
Write-Host "ok    $name matches its published SHA-256"

$unpacked = Join-Path ([IO.Path]::GetTempPath()) "spoolsmith-payload-$([guid]::NewGuid().ToString('N'))"
Expand-Archive -Path $zip -DestinationPath $unpacked
$files = @(Get-ChildItem $unpacked -Recurse -File)
$expectedEntries = @('spoolsmith/LICENSE', 'spoolsmith/README.md', 'spoolsmith/spoolsmith-gui.exe', 'spoolsmith/spoolsmith.exe')
$entries = @($files | ForEach-Object { $_.FullName.Substring($unpacked.Length + 1).Replace('\', '/') } | Sort-Object)
if (($entries -join '|') -ne ($expectedEntries -join '|')) {
    throw "$name holds unexpected files: $($entries -join ', ')"
}
New-Item -ItemType Directory -Force $To | Out-Null
foreach ($file in $files) { Copy-Item $file.FullName (Join-Path $To $file.Name) }
Remove-Item $unpacked -Recurse -Force
Write-Host "ok    unpacked $($files.Count) files to $To"

if ($VerifySignatures) {
    & (Join-Path $PSScriptRoot 'verify-signature.ps1') -Files (Join-Path $To 'spoolsmith.exe'), (Join-Path $To 'spoolsmith-gui.exe') -ExpectedSubject $ExpectedSubject
}
