# macOS and Linux release packages are future roadmap work and are not built here, per DR-0005 section 8.
param(
    [string]$Tag = "dev-$(git rev-parse --short HEAD)"
)

$ErrorActionPreference = "Stop"
$Goos = "windows"
$Goarch = "amd64"
$Asset = "spoolsmith-$Tag-$Goos-$Goarch.zip"

$env:GOOS = $Goos
$env:GOARCH = $Goarch

New-Item -ItemType Directory -Force dist | Out-Null
go build -o dist/spoolsmith.exe ./cmd/spoolsmith
if ($LASTEXITCODE -ne 0) {
    throw "go build failed with exit code $LASTEXITCODE"
}

New-Item -ItemType Directory -Force spoolsmith | Out-Null
Copy-Item dist/spoolsmith.exe,README.md,LICENSE spoolsmith/ -Force
Compress-Archive -Path spoolsmith -DestinationPath $Asset -Force
$Hash = Get-FileHash $Asset -Algorithm SHA256
"$($Hash.Hash.ToLowerInvariant())  $Asset" | Out-File "$Asset.sha256" -Encoding ascii
