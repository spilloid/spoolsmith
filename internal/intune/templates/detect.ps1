# Upload this file as the custom detection script. Run as 64-bit SYSTEM.
# Emit stdout ONLY after validating the revision AND actual Windows inventory.
$ErrorActionPreference='Stop'
try {
    if (-not [Environment]::Is64BitProcess) { exit 1 }
    $root=Join-Path ([Environment]::GetFolderPath('CommonApplicationData')) 'SpoolSmith\Deployments\{{.ID}}'
    $manifest=Get-Content -LiteralPath (Join-Path $root 'current.json') -Raw -Encoding UTF8 | ConvertFrom-Json
    if ($manifest.id -ne '{{.ID}}' -or $manifest.revision -ne {{.Revision}} -or $manifest.configuration_sha256 -ne '{{.ConfigSHA256}}') { exit 1 }
    if (Test-Path -LiteralPath (Join-Path $root 'pending.json')) { exit 1 }
    $expected=[Text.Encoding]::UTF8.GetString([Convert]::FromBase64String('{{json64 .Profile}}')) | ConvertFrom-Json
    # Inventory is queried directly: cached profiles, binaries and markers alone
    # never establish compliance and no printer network request is made.
    $q=@(Get-Printer -ErrorAction Stop | Where-Object { $_.Name -eq $expected.printer_name })
    if ($q.Count -ne 1 -or $q[0].DriverName -ne $expected.driver_name) { exit 1 }
    $expectedIP=[Net.IPAddress]::Parse($expected.target)
    if ($q[0].PortName -ne ('SpoolSmith-' + $expectedIP.ToString())) { exit 1 }
    $d=@(Get-PrinterDriver -ErrorAction Stop | Where-Object { $_.Name -eq $q[0].DriverName })
    $p=@(Get-PrinterPort -ErrorAction Stop | Where-Object { $_.Name -eq $q[0].PortName })
    if ($d.Count -eq 0 -or $p.Count -ne 1 -or $p[0].Protocol -ne 1 -or $p[0].PortNumber -ne 9100) { exit 1 }
    if (-not $expectedIP.Equals([Net.IPAddress]::Parse($p[0].PrinterHostAddress))) { exit 1 }
    Write-Output 'SpoolSmith {{.ID}} revision {{.Revision}} configured locally'
    exit 0
} catch { exit 1 }
