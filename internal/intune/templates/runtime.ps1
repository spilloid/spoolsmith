# Shared endpoint helpers. All printer operations go through the pinned CLI.
$ErrorActionPreference = 'Stop'
# Console encoding is best-effort under service hosts without a console handle.
# JSON file I/O below specifies UTF-8 independently.
try { [Console]::OutputEncoding = New-Object Text.UTF8Encoding($false) } catch {}
function Assert-Platform {
    if (-not [Environment]::Is64BitProcess) { throw 'Run this script in 64-bit PowerShell' }
    if ($env:PROCESSOR_ARCHITECTURE -ne 'AMD64') { throw 'This deployment requires Windows x64' }
    $principal = New-Object Security.Principal.WindowsPrincipal([Security.Principal.WindowsIdentity]::GetCurrent())
    if (-not $principal.IsInRole([Security.Principal.WindowsBuiltinRole]::Administrator)) { throw 'SYSTEM or Administrator is required' }
}
function Assert-PlainPath([string]$Path) {
    $item = Get-Item -LiteralPath $Path -Force
    while ($null -ne $item) {
        if ($item.Attributes -band [IO.FileAttributes]::ReparsePoint) { throw 'Deployment paths cannot contain reparse points' }
        if ($item -is [IO.FileInfo]) { $item=$item.Directory } else { $item=$item.Parent }
    }
}
function Assert-Protected([string]$Path) {
    Assert-PlainPath $Path
    $acl = Get-Acl -LiteralPath $Path
    $owner = $acl.GetOwner([Security.Principal.SecurityIdentifier]).Value
    if ($owner -notin @('S-1-5-18','S-1-5-32-544')) { throw 'Deployment directory has an untrusted owner' }
    $write = [Security.AccessControl.FileSystemRights]'Write,Modify,FullControl,Delete,ChangePermissions,TakeOwnership'
    foreach ($rule in $acl.GetAccessRules($true,$true,[Security.Principal.SecurityIdentifier])) {
        if ($rule.AccessControlType -eq 'Allow' -and ($rule.FileSystemRights -band $write) -and $rule.IdentityReference.Value -notin @('S-1-5-18','S-1-5-32-544')) {
            throw 'Deployment directory permits unprivileged writes'
        }
    }
}
function Initialize-Root {
    $parent = Join-Path ([Environment]::GetFolderPath('CommonApplicationData')) 'SpoolSmith'
    if (Test-Path -LiteralPath $parent) { Assert-Protected $parent } else {
        # Supply a protected ACL at creation, avoiding an inheritance window.
        $acl = New-Object Security.AccessControl.DirectorySecurity
        $acl.SetAccessRuleProtection($true,$false)
        $acl.SetOwner((New-Object Security.Principal.SecurityIdentifier('S-1-5-32-544')))
        foreach ($sid in @('S-1-5-18','S-1-5-32-544')) {
            $rule = New-Object Security.AccessControl.FileSystemAccessRule((New-Object Security.Principal.SecurityIdentifier($sid)), 'FullControl', 'ContainerInherit,ObjectInherit', 'None', 'Allow')
            $acl.AddAccessRule($rule)
        }
        [IO.Directory]::CreateDirectory($parent,$acl) | Out-Null
        Assert-Protected $parent
    }
    $root = Join-Path $parent 'Deployments'
    if (-not (Test-Path -LiteralPath $root)) { New-Item -ItemType Directory -Path $root | Out-Null }
    Assert-Protected $root
    return $root
}
function Read-JSON([string]$Path) { return (Get-Content -LiteralPath $Path -Raw -Encoding UTF8 | ConvertFrom-Json) }
function Save-JSON([string]$Path,$Value) {
    $temp = $Path + '.' + [Guid]::NewGuid().ToString('N') + '.tmp'
    [IO.File]::WriteAllText($temp,($Value | ConvertTo-Json -Depth 20),(New-Object Text.UTF8Encoding($false)))
    if (Test-Path -LiteralPath $Path) { [IO.File]::Replace($temp,$Path,($Path + '.previous')) } else { [IO.File]::Move($temp,$Path) }
}
function Assert-Hash([string]$Path,[string]$Expected) {
    Assert-PlainPath $Path
    if ((Get-FileHash -LiteralPath $Path -Algorithm SHA256).Hash -ne $Expected) { throw ('Payload hash mismatch: ' + $Path) }
}
# Each stored revision retains its matching CLI and profile format. This lets
# current scripts inspect/remove old revisions without converting their payloads.
function Payload-ProfileName($Manifest) {
    switch ($Manifest.format) {
        1 { return 'profile.json' }
        2 { return 'profile.ssb' }
        default { throw ('Unsupported deployment format: ' + $Manifest.format) }
    }
}
function Assert-Payload([string]$Directory,$Manifest) {
    Assert-Hash (Join-Path $Directory 'spoolsmith.exe') $Manifest.binary_sha256
    Assert-Hash (Join-Path $Directory (Payload-ProfileName $Manifest)) $Manifest.profile_sha256
    if ($Manifest.driver_sha256) { Assert-Hash (Join-Path $Directory 'driver.exe') $Manifest.driver_sha256 }
    if ($Manifest.bundle_sha256) { Assert-Hash (Join-Path $Directory 'bundle.ssb') $Manifest.bundle_sha256 }
}
function Revision-Directory([string]$StateDir,$Manifest) {
    if ($Manifest.revision -lt 1 -or $Manifest.configuration_sha256 -notmatch '^[0-9a-f]{64}$') { throw 'Invalid stored revision' }
    return (Join-Path $StateDir ('r' + $Manifest.revision + '-' + $Manifest.configuration_sha256))
}
function Invoke-SpoolSmith([string]$Directory,[string]$Operation,[bool]$Offline,[string]$LogDir) {
    $manifest = Read-JSON (Join-Path $Directory 'deployment.json')
    Assert-Payload $Directory $manifest
    $prefix = Join-Path $LogDir ([DateTime]::UtcNow.ToString('yyyyMMddTHHmmssfff') + '-' + [Guid]::NewGuid().ToString('N'))
    $outFile = $prefix + '.stdout.log'; $errFile = $prefix + '.stderr.log'
    # A profile sourced from a .ssb bundle carries its driver payload only
    # inside that bundle. `apply` reopens it and runs the same catalog-
    # signature-checked staging `spoolsmith copy`/`apply` already use on the
    # CLI. Mutating operations use the original payload bundle; status and
    # removal use the settings-only profile.ssb with the same reviewed settings.
    if ($manifest.bundle_sha256 -and $Operation -in @('add','configure')) {
        $arguments = @('apply',('"' + (Join-Path $Directory 'bundle.ssb') + '"'),'--json')
        if ($Operation -eq 'configure') { $arguments += '--update' }
    } else {
        $arguments = @($Operation,'--profile',('"' + (Join-Path $Directory (Payload-ProfileName $manifest)) + '"'),'--json')
    }
    if ($Operation -ne 'status') { $arguments += @('--yes','--non-interactive') }
    if ($Offline -and $Operation -ne 'status' -and $Operation -ne 'remove') { $arguments += '--offline' }
    # Own the Process from creation: Start-Process -PassThru can hand back an
    # already-exited process without a usable exit-code handle on Windows PS 5.1.
    # Drain both pipes asynchronously into durable files so neither can block
    # a verbose CLI while the timeout is being enforced.
    $start = New-Object Diagnostics.ProcessStartInfo
    $start.FileName = Join-Path $Directory 'spoolsmith.exe'
    $start.Arguments = $arguments -join ' '
    $start.WorkingDirectory = $Directory
    $start.UseShellExecute = $false
    $start.CreateNoWindow = $true
    $start.RedirectStandardOutput = $true
    $start.RedirectStandardError = $true
    $process = New-Object Diagnostics.Process
    $process.StartInfo = $start
    $stdoutStream=$null; $stderrStream=$null; $drained=$false
    $timer=[Diagnostics.Stopwatch]::StartNew()
    try {
        $stdoutStream=[IO.File]::Open($outFile,[IO.FileMode]::CreateNew,[IO.FileAccess]::Write,[IO.FileShare]::Read)
        $stderrStream=[IO.File]::Open($errFile,[IO.FileMode]::CreateNew,[IO.FileAccess]::Write,[IO.FileShare]::Read)
        if (-not $process.Start()) { throw 'Could not start the SpoolSmith CLI' }
        $copyOut=$process.StandardOutput.BaseStream.CopyToAsync($stdoutStream)
        $copyErr=$process.StandardError.BaseStream.CopyToAsync($stderrStream)
        $timedOut=-not $process.WaitForExit(600000)
        if ($timedOut) {
            & (Join-Path $env:SystemRoot 'System32\taskkill.exe') /PID $process.Id /T /F | Out-Null
            $null=$process.WaitForExit(5000)
            $code=1460
        } else {
            $code=$process.ExitCode
        }
        # Preserve the native verdict even if logging fails. A surviving pipe
        # writer must not extend the operation indefinitely after the CLI exits.
        $drainMs=[int][Math]::Max(0,[Math]::Min(30000,600000-$timer.ElapsedMilliseconds))
        try { $drained=[Threading.Tasks.Task]::WaitAll([Threading.Tasks.Task[]]@($copyOut,$copyErr),$drainMs) } catch { $drained=$false }
    } finally {
        if ($null -ne $stdoutStream) { $stdoutStream.Dispose() }
        if ($null -ne $stderrStream) { $stderrStream.Dispose() }
        $process.Dispose()
    }
    if ($timedOut) { return @{Code=1460; Data=$null; Error='Timed out after ten minutes; review durable logs before retry'} }
    if ($null -eq $code) { throw 'Native process exited without a readable exit code; refusing to report success' }
    $data=$null
    if (Test-Path -LiteralPath $outFile) { try { $data=Read-JSON $outFile } catch {} }
    $diagnostic=''
    try { $diagnostic=[string](Get-Content -LiteralPath $errFile -Raw -Encoding UTF8) } catch { $drained=$false }
    if (-not $drained) { $diagnostic += '; durable log may be truncated' }
    return @{Code=$code; Data=$data; Error=$diagnostic}
}
function Is-Matching([string]$Directory,[string]$Logs) {
    $result = Invoke-SpoolSmith $Directory 'status' $false $Logs
    if ($result.Code -eq 0 -and $result.Data.compliant -eq $true) { return $true }
    if ($result.Code -eq 3) { return $false }
    throw ('Local inventory failed: ' + $result.Error)
}
