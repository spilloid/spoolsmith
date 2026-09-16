# Approved local provisioning. Intune must use SYSTEM install behavior.
$ErrorActionPreference = 'Stop'
if (-not [Environment]::Is64BitProcess -and [Environment]::Is64BitOperatingSystem) {
    & "$env:windir\Sysnative\WindowsPowerShell\v1.0\powershell.exe" -NoProfile -NonInteractive -ExecutionPolicy Bypass -File $PSCommandPath
    exit $LASTEXITCODE
}
. (Join-Path $PSScriptRoot 'runtime.ps1')
$lock=$null; $log=$null
try {
    Assert-Platform
    $manifest=Read-JSON (Join-Path $PSScriptRoot 'deployment.json')
    if ($manifest.id -ne '{{.ID}}' -or $manifest.revision -ne {{.Revision}} -or $manifest.configuration_sha256 -ne '{{.ConfigSHA256}}') { throw 'Deployment manifest differs from reviewed package' }
    Assert-Payload $PSScriptRoot $manifest
    $root=Initialize-Root
    try { $lock=[IO.File]::Open((Join-Path $root 'deployment.lock'),'OpenOrCreate','ReadWrite','None') } catch [IO.IOException] { exit 1618 }
    $stateDir=Join-Path $root $manifest.id
    if (-not (Test-Path -LiteralPath $stateDir)) { New-Item -ItemType Directory -Path $stateDir | Out-Null }
    Assert-Protected $stateDir
    $logs=Join-Path $stateDir 'logs'
    if (-not (Test-Path -LiteralPath $logs)) { New-Item -ItemType Directory -Path $logs | Out-Null }
    $log=Join-Path $logs 'lifecycle.log'
    Add-Content -LiteralPath $log -Value ((Get-Date -Format o) + ' install revision ' + $manifest.revision)
    $currentPath=Join-Path $stateDir 'current.json'; $pendingPath=Join-Path $stateDir 'pending.json'
    $current=$null; $pending=$null
    if (Test-Path -LiteralPath $currentPath) { $current=Read-JSON $currentPath }
    if (Test-Path -LiteralPath $pendingPath) { $pending=Read-JSON $pendingPath }
    if ($pending -and ($pending.configuration_sha256 -ne $manifest.configuration_sha256 -or $pending.revision -ne $manifest.revision)) { throw 'An interrupted deployment needs retry or explicit removal before a different revision' }
    if ($current) {
        if ($current.profile.printer_name -ne $manifest.profile.printer_name) { throw 'Queue rename requires explicit removal and a new deployment ID' }
        if ($manifest.revision -lt $current.revision) { throw 'Deployment downgrade refused' }
        if ($manifest.revision -eq $current.revision -and $manifest.configuration_sha256 -ne $current.configuration_sha256) { throw 'Changed configuration requires a higher revision' }
    }
    foreach ($directory in Get-ChildItem -LiteralPath $root -Directory) {
        if ($directory.Name -eq $manifest.id) { continue }
        foreach ($file in @('current.json','pending.json')) {
            $claim=Join-Path $directory.FullName $file
            if (Test-Path -LiteralPath $claim) {
                $other=Read-JSON $claim
                if ($other.profile.printer_name -eq $manifest.profile.printer_name) { throw 'Another deployment owns this queue name' }
            }
        }
    }
    $destination=Revision-Directory $stateDir $manifest
    if (-not (Test-Path -LiteralPath $destination)) {
        $stage=Join-Path $stateDir ('stage-' + [Guid]::NewGuid().ToString('N'))
        New-Item -ItemType Directory -Path $stage | Out-Null
        foreach ($file in @('deployment.json','profile.json','spoolsmith.exe','runtime.ps1','uninstall.ps1')) { Copy-Item -LiteralPath (Join-Path $PSScriptRoot $file) -Destination (Join-Path $stage $file) }
        if ($manifest.driver_sha256) { Copy-Item -LiteralPath (Join-Path $PSScriptRoot 'driver.exe') -Destination (Join-Path $stage 'driver.exe') }
        Assert-Payload $stage $manifest
        Move-Item -LiteralPath $stage -Destination $destination
    }
    Assert-Protected $destination
    Assert-Payload $destination $manifest
    $queues=@(Get-Printer -ErrorAction Stop | Where-Object { $_.Name -eq $manifest.profile.printer_name })
    if ($queues.Count -gt 1) { throw 'Multiple queues match the deployment' }
    if ($queues.Count -eq 1) {
        $matchesNew=Is-Matching $destination $logs
        if ($current) {
            $matchesOld=Is-Matching (Revision-Directory $stateDir $current) $logs
            if (-not $matchesNew -and -not $matchesOld) { throw 'Managed queue drifted; review configuration before updating' }
        } elseif (-not $matchesNew -or (-not $pending -and -not $manifest.adopt_matching_queue)) { throw 'Existing queue requires explicit adoption and an exact configuration match' }
    }
    # Persist all removal inputs BEFORE printer mutation; retries retain both revisions.
    Copy-Item -LiteralPath (Join-Path $destination 'runtime.ps1') -Destination (Join-Path $stateDir 'runtime.ps1') -Force
    Copy-Item -LiteralPath (Join-Path $destination 'uninstall.ps1') -Destination (Join-Path $stateDir 'uninstall.ps1') -Force
    Save-JSON $pendingPath $manifest
    $operation='add'; if ($current) { $operation='configure' }
    $result=Invoke-SpoolSmith $destination $operation ([bool]$manifest.offline) $logs
    if ($result.Code -ne 0) { Add-Content -LiteralPath $log -Value $result.Error; exit $result.Code }
    if (-not (Is-Matching $destination $logs)) { throw 'Post-install local configuration does not match' }
    Save-JSON $currentPath $manifest
    Remove-Item -LiteralPath $pendingPath
    Add-Content -LiteralPath $log -Value ((Get-Date -Format o) + ' configured locally; printing unverified')
    exit 0
} catch {
    if ($log) { Add-Content -LiteralPath $log -Value $_.Exception.Message }
    Write-Error $_.Exception.Message -ErrorAction Continue
    exit 1
} finally { if ($lock) { $lock.Dispose() } }
