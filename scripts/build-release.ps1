# macOS and Linux packages are future work; this builds the Windows x64 release.
param([string]$Tag = "dev-$(git rev-parse --short HEAD)")
$ErrorActionPreference = "Stop"
if ($Tag -notmatch '^[A-Za-z0-9][A-Za-z0-9._-]*$') { throw 'Invalid release tag' }
$env:GOOS = 'windows'
$env:GOARCH = 'amd64'
$asset = "spoolsmith-$Tag-windows-amd64.zip"
New-Item -ItemType Directory -Force dist | Out-Null
go build -trimpath -ldflags "-s -w -X main.version=$Tag" -o dist/spoolsmith.exe ./cmd/spoolsmith
if ($LASTEXITCODE -ne 0) { throw 'CLI build failed' }
go build -trimpath -ldflags '-s -w -H windowsgui' -o dist/spoolsmith-gui.exe ./cmd/spoolsmith-gui
if ($LASTEXITCODE -ne 0) { throw 'GUI build failed' }
# Explicit forward-slash ZIP entries work with Windows and other extractors.
Add-Type -AssemblyName System.IO.Compression.FileSystem
$assetPath = Join-Path (Resolve-Path dist) $asset
if (Test-Path $assetPath) { Remove-Item $assetPath }
$zip = [IO.Compression.ZipFile]::Open($assetPath, [IO.Compression.ZipArchiveMode]::Create)
try {
    foreach ($file in @('dist/spoolsmith.exe', 'dist/spoolsmith-gui.exe', 'README.md', 'LICENSE')) {
        $entry = 'spoolsmith/' + [IO.Path]::GetFileName($file)
        [IO.Compression.ZipFileExtensions]::CreateEntryFromFile($zip, (Resolve-Path $file), $entry, [IO.Compression.CompressionLevel]::Optimal) | Out-Null
    }
} finally { $zip.Dispose() }
$hash = (Get-FileHash $assetPath -Algorithm SHA256).Hash.ToLowerInvariant()
"$hash  $asset" | Out-File "$assetPath.sha256" -Encoding ascii
Write-Output $assetPath
