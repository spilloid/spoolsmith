# Run from an elevated PowerShell or elevated Codex session before v1.4.0.
# Creates uniquely named temporary queues, reapplies them, checks local status,
# and removes them. Sends no print jobs and never removes an installed driver.
# -PrepareIPPSource temporarily removes/restores the source WSD queue after
# capture to avoid directed-discovery collisions. Backups remain in dist/validation.
# Windows may allocate a new WSD port ID on restoration. -IPPOnly limits retries.
param([string]$WSDQueue = 'Brother HL-L2315D series Printer', [switch]$PrepareIPPSource, [switch]$IPPOnly)
$ErrorActionPreference = 'Stop'
$validationIdentity = [Security.Principal.WindowsIdentity]::GetCurrent()
$validationPrincipal = [Security.Principal.WindowsPrincipal]::new($validationIdentity)
if (-not $validationPrincipal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    throw 'Open PowerShell or Codex as Administrator, then run this script again.'
}
$validationRoot = Split-Path $PSScriptRoot -Parent
$validationSavedQueue = $env:SPOOLSMITH_TEST_WSD_QUEUE
$validationSavedGate = $env:SPOOLSMITH_TEST_ADMIN_MUTATIONS
$validationSavedCache = $env:GOCACHE
$validationSavedPreparation = $env:SPOOLSMITH_TEST_PREPARE_IPP_SOURCE
Push-Location $validationRoot
try {
    $env:SPOOLSMITH_TEST_WSD_QUEUE = $WSDQueue
    $env:SPOOLSMITH_TEST_ADMIN_MUTATIONS = '1'
    $env:SPOOLSMITH_TEST_PREPARE_IPP_SOURCE = if ($PrepareIPPSource) { '1' } else { '' }
    $env:GOCACHE = Join-Path $env:TEMP 'spoolsmith-go-cache'
    New-Item -ItemType Directory -Path 'dist/validation' -Force | Out-Null
    $validationLog = Join-Path $validationRoot ('dist/validation/printers-' + (Get-Date -Format 'yyyyMMdd-HHmmss') + '.txt')
    $validationFilter = if ($IPPOnly) { '^TestHardwareAdminIPPImport$' } else { '^TestHardware(WSD|Admin)' }
    & go test ./internal/install ./internal/bundle -run $validationFilter -count=1 -v 2>&1 | Tee-Object -FilePath $validationLog
    if ($LASTEXITCODE -ne 0) { throw "Hardware validation failed. See $validationLog" }
    Write-Output "Hardware validation passed. Record: $validationLog"
    Write-Output 'USB preparation reused an installed driver; a clean target is still needed to prove first-time driver installation. Physical printing was not tested.'
} finally {
    $env:SPOOLSMITH_TEST_WSD_QUEUE = $validationSavedQueue
    $env:SPOOLSMITH_TEST_ADMIN_MUTATIONS = $validationSavedGate
    $env:GOCACHE = $validationSavedCache
    $env:SPOOLSMITH_TEST_PREPARE_IPP_SOURCE = $validationSavedPreparation
    Pop-Location
}
