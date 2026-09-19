# macOS and Linux packages are future work; this builds the Windows x64 release.
#
# Stages exist so the binaries can be Authenticode-signed between compiling and
# zipping: the published SHA-256 has to cover the signed zip, and a signature
# applied after packaging would not be inside it.
#   build    compile dist/spoolsmith.exe and dist/spoolsmith-gui.exe
#   package  zip those two plus README/LICENSE, write the .sha256 sidecar
#   all      build, sign if -Sign was passed, then package (default)
# CI runs `build`, signs with azure/artifact-signing-action, then runs `package`.
# By hand, `-Sign` does the same thing through scripts/sign-windows.ps1.
param(
    [string]$Tag = "dev-$(git rev-parse --short HEAD)",
    [ValidateSet('all', 'build', 'package')][string]$Stage = 'all',
    [switch]$Sign
)
$ErrorActionPreference = "Stop"
if ($Tag -notmatch '^[A-Za-z0-9][A-Za-z0-9._-]*$') { throw 'Invalid release tag' }
if ($Sign -and $Stage -eq 'package') { throw 'Nothing to sign in the package stage; sign during build or all' }
$env:GOOS = 'windows'
$env:GOARCH = 'amd64'
$asset = "spoolsmith-$Tag-windows-amd64.zip"
$binaries = @('dist/spoolsmith.exe', 'dist/spoolsmith-gui.exe')
New-Item -ItemType Directory -Force dist | Out-Null

if ($Stage -eq 'all' -or $Stage -eq 'build') {
    go build -trimpath -ldflags "-s -w -X main.version=$Tag" -o dist/spoolsmith.exe ./cmd/spoolsmith
    if ($LASTEXITCODE -ne 0) { throw 'CLI build failed' }
    go build -trimpath -ldflags '-s -w -H windowsgui' -o dist/spoolsmith-gui.exe ./cmd/spoolsmith-gui
    if ($LASTEXITCODE -ne 0) { throw 'GUI build failed' }
}

if ($Sign) {
    & (Join-Path $PSScriptRoot 'sign-windows.ps1') -Files $binaries
}

if ($Stage -eq 'all' -or $Stage -eq 'package') {
    foreach ($file in $binaries) {
        if (-not (Test-Path $file)) { throw "$file is missing — run the build stage first" }
    }
    # Explicit forward-slash ZIP entries work with Windows and other extractors.
    Add-Type -AssemblyName System.IO.Compression.FileSystem
    $assetPath = Join-Path (Resolve-Path dist) $asset
    if (Test-Path $assetPath) { Remove-Item $assetPath }
    $zip = [IO.Compression.ZipFile]::Open($assetPath, [IO.Compression.ZipArchiveMode]::Create)
    try {
        foreach ($file in $binaries + @('README.md', 'LICENSE')) {
            $entry = 'spoolsmith/' + [IO.Path]::GetFileName($file)
            [IO.Compression.ZipFileExtensions]::CreateEntryFromFile($zip, (Resolve-Path $file), $entry, [IO.Compression.CompressionLevel]::Optimal) | Out-Null
        }
    } finally { $zip.Dispose() }
    $hash = (Get-FileHash $assetPath -Algorithm SHA256).Hash.ToLowerInvariant()
    "$hash  $asset" | Out-File "$assetPath.sha256" -Encoding ascii
    Write-Output $assetPath
}
