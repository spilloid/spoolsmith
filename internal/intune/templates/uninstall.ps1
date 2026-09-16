# This script is also retained under ProgramData, independent of Intune's cache.
$ErrorActionPreference='Stop'
if (-not [Environment]::Is64BitProcess -and [Environment]::Is64BitOperatingSystem) {
    & "$env:windir\Sysnative\WindowsPowerShell\v1.0\powershell.exe" -NoProfile -NonInteractive -ExecutionPolicy Bypass -File $PSCommandPath
    exit $LASTEXITCODE
}
. (Join-Path $PSScriptRoot 'runtime.ps1')
$lock=$null; $log=$null
try {
    Assert-Platform
    $root=Initialize-Root
    try { $lock=[IO.File]::Open((Join-Path $root 'deployment.lock'),'OpenOrCreate','ReadWrite','None') } catch [IO.IOException] { exit 1618 }
    $stateDir=Join-Path $root '{{.ID}}'
    if (-not (Test-Path -LiteralPath $stateDir)) { exit 0 }
    Assert-Protected $stateDir
    $logs=Join-Path $stateDir 'logs'; $log=Join-Path $logs 'lifecycle.log'
    $candidates=@()
    foreach ($name in @('pending.json','current.json')) {
        $path=Join-Path $stateDir $name
        if (Test-Path -LiteralPath $path) { $candidates+=Read-JSON $path }
    }
    if ($candidates.Count -eq 0) { exit 0 }
    $chosen=$null
    foreach ($manifest in $candidates) {
        if ($manifest.id -ne '{{.ID}}') { throw 'Stored deployment ID mismatch' }
        $directory=Revision-Directory $stateDir $manifest
        $queues=@(Get-Printer -ErrorAction Stop | Where-Object { $_.Name -eq $manifest.profile.printer_name })
        if ($queues.Count -eq 0 -or (Is-Matching $directory $logs)) { $chosen=$directory; break }
    }
    if (-not $chosen) { throw 'Queue differs from managed configuration; refusing removal' }
    $result=Invoke-SpoolSmith $chosen 'remove' $false $logs
    if ($result.Code -ne 0) { Add-Content -LiteralPath $log -Value $result.Error; exit $result.Code }
    $profile=(Read-JSON (Join-Path $chosen 'deployment.json')).profile
    if (@(Get-Printer -ErrorAction Stop | Where-Object { $_.Name -eq $profile.printer_name }).Count -ne 0) { throw 'Queue removal was not verified' }
    foreach ($name in @('pending.json','current.json')) { $path=Join-Path $stateDir $name; if (Test-Path -LiteralPath $path) { Remove-Item -LiteralPath $path } }
    Add-Content -LiteralPath $log -Value ((Get-Date -Format o) + ' removed; retained driver, shared ports, diagnostic files and previous revisions')
    exit 0
} catch {
    if ($log) { Add-Content -LiteralPath $log -Value $_.Exception.Message }
    Write-Error $_.Exception.Message -ErrorAction Continue
    exit 1
} finally { if ($lock) { $lock.Dispose() } }
