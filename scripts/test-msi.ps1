<#
.SYNOPSIS
Checks a SpoolSmith MSI on Windows: its signature, its payload, and a real
silent install, upgrade and uninstall.

.DESCRIPTION
-Signed        the MSI itself must carry a valid, timestamped signature
               (scripts/verify-signature.ps1, same rules as the EXEs).
-Payload       unpack the MSI (msiexec /a) and require its files to be
               byte-identical to the files in this folder; with -Signed, the
               EXEs inside must also carry valid, timestamped signatures.
-Lifecycle     install quietly, check the files, the Start Menu shortcut and a
               read-only CLI command, then uninstall quietly and check that
               everything the MSI owns is gone.
-UpgradeFrom   with -Lifecycle: install this MSI first, then check that the
               MSI under test replaces it (one SpoolSmith entry, new version).
               When it is an older version, it must then refuse to install
               over the newer one. A same-version rebuild must also replace.

Lifecycle checks install software machine-wide, so they only run on a
GitHub-hosted runner. Nothing here touches printers.
#>
param(
    [Parameter(Mandatory)][string]$Msi,
    [Parameter(Mandatory)][string]$Tag,
    [switch]$Signed,
    [string]$ExpectedSubject,
    [string]$Payload,
    [switch]$Lifecycle,
    [string]$UpgradeFrom,
    [string]$UpgradeFromTag
)
$ErrorActionPreference = 'Stop'
$Msi = (Resolve-Path $Msi).Path
$version = $Tag.TrimStart('v')
$installDir = Join-Path $env:ProgramFiles 'SpoolSmith'
$shortcut = Join-Path $env:ProgramData 'Microsoft\Windows\Start Menu\Programs\SpoolSmith.lnk'
$logs = Join-Path ($env:RUNNER_TEMP ?? [IO.Path]::GetTempPath()) "spoolsmith-msi-logs"
New-Item -ItemType Directory -Force $logs | Out-Null

function Invoke-Msiexec([string[]]$Arguments, [string]$Log) {
    $process = Start-Process -FilePath msiexec.exe -ArgumentList ($Arguments + @('/l*v', "`"$Log`"")) -Wait -PassThru -NoNewWindow
    return $process.ExitCode
}

function Get-SpoolSmithEntries {
    @('HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\*',
      'HKLM:\SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall\*') |
        ForEach-Object { Get-ItemProperty $_ -ErrorAction SilentlyContinue } |
        Where-Object { $_.DisplayName -eq 'SpoolSmith' }
}

function Assert([bool]$Condition, [string]$Message) {
    if (-not $Condition) { throw "FAIL: $Message" }
    Write-Host "ok    $Message"
}

if ($Signed) {
    & (Join-Path $PSScriptRoot 'verify-signature.ps1') -Files $Msi -ExpectedSubject $ExpectedSubject
}

if ($Payload) {
    # An administrative install unpacks the files without installing anything.
    $unpacked = Join-Path ([IO.Path]::GetTempPath()) "spoolsmith-msi-admin-$([guid]::NewGuid().ToString('N'))"
    $code = Invoke-Msiexec @('/a', "`"$Msi`"", '/qn', "TARGETDIR=`"$unpacked`"") (Join-Path $logs 'admin.log')
    Assert ($code -eq 0) "administrative extract exited $code"
    $exes = Get-ChildItem $unpacked -Recurse -Filter *.exe
    Assert ($exes.Count -eq 2) "MSI carries two EXEs"
    if ($Signed) {
        # A signed release MSI must only ever carry signed application binaries.
        & (Join-Path $PSScriptRoot 'verify-signature.ps1') -Files ($exes | ForEach-Object FullName) -ExpectedSubject $ExpectedSubject
    }
    foreach ($name in 'spoolsmith.exe', 'spoolsmith-gui.exe', 'README.md', 'LICENSE') {
        $inside = Get-ChildItem $unpacked -Recurse -Filter $name | Select-Object -First 1
        $a = (Get-FileHash $inside.FullName -Algorithm SHA256).Hash
        $b = (Get-FileHash (Join-Path $Payload $name) -Algorithm SHA256).Hash
        Assert ($a -eq $b) "$name in the MSI matches the release payload"
    }
    Remove-Item $unpacked -Recurse -Force
}

if ($Lifecycle) {
    if ($env:GITHUB_ACTIONS -ne 'true' -or $env:RUNNER_ENVIRONMENT -ne 'github-hosted') {
        throw 'The install lifecycle test installs software machine-wide; it only runs on a GitHub-hosted runner.'
    }
    Assert (-not (Get-SpoolSmithEntries)) 'SpoolSmith is not installed before the test'
    Assert (-not (Test-Path $installDir)) "$installDir does not exist before the test"

    if ($UpgradeFrom) {
        $UpgradeFrom = (Resolve-Path $UpgradeFrom).Path
        $code = Invoke-Msiexec @('/i', "`"$UpgradeFrom`"", '/qn', '/norestart') (Join-Path $logs 'install-old.log')
        Assert ($code -eq 0) "older MSI installed (exit $code)"
        Assert ((Get-SpoolSmithEntries).DisplayVersion -eq $UpgradeFromTag.TrimStart('v')) "older version $UpgradeFromTag is registered"
    }

    $code = Invoke-Msiexec @('/i', "`"$Msi`"", '/qn', '/norestart') (Join-Path $logs 'install.log')
    Assert ($code -eq 0) "quiet install exited $code"
    $entries = @(Get-SpoolSmithEntries)
    Assert ($entries.Count -eq 1) "exactly one SpoolSmith in Apps & Features (found $($entries.Count))"
    Assert ($entries[0].DisplayVersion -eq $version) "registered version is $version"
    Assert ($entries[0].Publisher -eq 'Joseph Spillers') "publisher is Joseph Spillers"
    foreach ($name in 'spoolsmith.exe', 'spoolsmith-gui.exe', 'README.md', 'LICENSE') {
        Assert (Test-Path (Join-Path $installDir $name)) "$name installed in $installDir"
    }
    Assert (Test-Path $shortcut) 'Start Menu shortcut exists'
    $target = (New-Object -ComObject WScript.Shell).CreateShortcut($shortcut).TargetPath
    Assert ($target -eq (Join-Path $installDir 'spoolsmith-gui.exe')) "shortcut points at $target"

    # A read-only command from the installed CLI: prints this build's
    # capability marker, touches nothing.
    $output = & (Join-Path $installDir 'spoolsmith.exe') capabilities 2>&1 | Out-String
    Assert ($LASTEXITCODE -eq 0) "installed CLI runs (capabilities exited $LASTEXITCODE)"
    Write-Host $output.Trim()

    if ($UpgradeFrom -and [version]$UpgradeFromTag.TrimStart('v') -lt [version]$version) {
        $code = Invoke-Msiexec @('/i', "`"$UpgradeFrom`"", '/qn', '/norestart') (Join-Path $logs 'downgrade.log')
        Assert ($code -ne 0) "installing the older MSI over the newer one is refused (exit $code)"
        Assert ((Get-SpoolSmithEntries).DisplayVersion -eq $version) "version $version is still the one installed"
    }

    $code = Invoke-Msiexec @('/x', "`"$Msi`"", '/qn', '/norestart') (Join-Path $logs 'uninstall.log')
    Assert ($code -eq 0) "quiet uninstall exited $code"
    Assert (-not (Get-SpoolSmithEntries)) 'SpoolSmith is gone from Apps & Features'
    Assert (-not (Test-Path $installDir)) "$installDir is removed"
    Assert (-not (Test-Path $shortcut)) 'Start Menu shortcut is removed'
}
Write-Host "MSI checks passed for $([IO.Path]::GetFileName($Msi))"
