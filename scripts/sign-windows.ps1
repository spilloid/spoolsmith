<#
.SYNOPSIS
Authenticode-signs SpoolSmith binaries with Azure Artifact Signing.

.DESCRIPTION
The release workflow does not call this script — it uses
azure/artifact-signing-action@v2, which wraps the same signtool + dlib
invocation. This exists so a release can be reproduced by hand on a Windows
machine, per docs/ci-cd-spec.md's rule that the pipeline is never the only way
to produce a release build.

Account details come from parameters or SPOOLSMITH_SIGN_* environment
variables. Azure credentials come from DefaultAzureCredential, so `az login`
is the usual local path. Setup, roles and endpoints: docs/code-signing.md.

Prerequisites (see docs/code-signing.md for the full list):
  winget install -e --id Microsoft.Azure.ArtifactSigningClientTools

.EXAMPLE
./scripts/sign-windows.ps1 -Files dist/spoolsmith.exe, dist/spoolsmith-gui.exe
#>
param(
    [Parameter(Mandatory)][string[]]$Files,
    [string]$Endpoint = $env:SPOOLSMITH_SIGN_ENDPOINT,
    [string]$AccountName = $env:SPOOLSMITH_SIGN_ACCOUNT,
    [string]$ProfileName = $env:SPOOLSMITH_SIGN_PROFILE,
    [string]$SignTool = $env:SPOOLSMITH_SIGNTOOL,
    [string]$Dlib = $env:SPOOLSMITH_SIGN_DLIB,
    [switch]$SkipVerify
)
$ErrorActionPreference = 'Stop'

foreach ($pair in @(@('SPOOLSMITH_SIGN_ENDPOINT', $Endpoint), @('SPOOLSMITH_SIGN_ACCOUNT', $AccountName), @('SPOOLSMITH_SIGN_PROFILE', $ProfileName))) {
    if ([string]::IsNullOrWhiteSpace($pair[1])) {
        throw "Signing is not configured: set $($pair[0]) or pass the matching parameter. See docs/code-signing.md."
    }
}
# A region/endpoint mismatch surfaces as an opaque 403 during signing, so reject
# an obviously wrong host up front instead.
if ($Endpoint -notmatch '^https://[a-z0-9]+\.codesigning\.azure\.net/?$') {
    throw "Endpoint '$Endpoint' does not look like a regional Artifact Signing endpoint (e.g. https://eus.codesigning.azure.net). See docs/code-signing.md."
}

$resolved = foreach ($file in $Files) { (Resolve-Path $file).Path }

if (-not $SignTool) {
    # Artifact Signing needs the SDK's signtool, not the one some toolchains ship;
    # take the newest installed SDK bin directory.
    $SignTool = Get-ChildItem "${env:ProgramFiles(x86)}\Windows Kits\10\bin\*\x64\signtool.exe" -ErrorAction SilentlyContinue |
        Sort-Object { [version]($_.Directory.Parent.Name) } -Descending |
        Select-Object -First 1 -ExpandProperty FullName
}
if (-not $SignTool -or -not (Test-Path $SignTool)) {
    throw 'signtool.exe not found. Install the Windows SDK (10.0.22621 or newer) or set SPOOLSMITH_SIGNTOOL. See docs/code-signing.md.'
}

if (-not $Dlib) {
    $roots = @($env:ProgramFiles, ${env:ProgramFiles(x86)}, $env:LOCALAPPDATA) | Where-Object { $_ }
    $Dlib = Get-ChildItem -Path $roots -Filter 'Azure.CodeSigning.Dlib.dll' -Recurse -ErrorAction SilentlyContinue |
        Where-Object { $_.FullName -match '\\x64\\' } |
        Select-Object -First 1 -ExpandProperty FullName
}
if (-not $Dlib -or -not (Test-Path $Dlib)) {
    throw 'Azure.CodeSigning.Dlib.dll not found. Run "winget install -e --id Microsoft.Azure.ArtifactSigningClientTools" or set SPOOLSMITH_SIGN_DLIB. See docs/code-signing.md.'
}

$metadataDir = Join-Path ([IO.Path]::GetTempPath()) ([guid]::NewGuid())
New-Item -ItemType Directory -Force $metadataDir | Out-Null
$metadata = Join-Path $metadataDir 'metadata.json'
try {
    @{
        Endpoint               = $Endpoint.TrimEnd('/')
        CodeSigningAccountName = $AccountName
        CertificateProfileName = $ProfileName
        CorrelationId          = "spoolsmith-local-$(git rev-parse --short HEAD 2>$null)"
    } | ConvertTo-Json | Set-Content $metadata -Encoding utf8

    foreach ($file in $resolved) {
        Write-Host "Signing $file"
        # Artifact Signing certificates are valid for three days, so the RFC3161
        # countersignature is what keeps the signature verifiable afterwards —
        # never drop /tr.
        & $SignTool sign /v /debug /fd SHA256 /tr 'http://timestamp.acs.microsoft.com' /td SHA256 `
            /dlib $Dlib /dmdf $metadata $file
        if ($LASTEXITCODE -ne 0) { throw "signtool failed for $file (exit $LASTEXITCODE)" }
    }
} finally {
    Remove-Item $metadataDir -Recurse -Force -ErrorAction SilentlyContinue
}

if (-not $SkipVerify) {
    & (Join-Path $PSScriptRoot 'verify-signature.ps1') -Files $resolved
}
