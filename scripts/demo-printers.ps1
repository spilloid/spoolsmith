# Creates a small office's worth of network printer queues so the website
# screenshots show SpoolSmith with real work to do.
#
# Only for the throwaway GitHub-hosted runner that captures the screenshots:
# it adds printers, ports and a driver to Windows, so it refuses to run
# anywhere else. The desktop test suite itself never changes printers.
#
# The queues point at private addresses nothing answers on, and use an inbox
# Microsoft class driver so no single printer brand leads the pictures.
[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'

if ($env:GITHUB_ACTIONS -ne 'true' -or $env:RUNNER_ENVIRONMENT -ne 'github-hosted') {
    throw 'demo-printers.ps1 adds printers to Windows; it only runs on a GitHub-hosted runner.'
}

# The first inbox driver this image can install.
$driver = $null
foreach ($candidate in 'Microsoft PCL6 Class Driver', 'Microsoft PS Class Driver', 'Generic / Text Only') {
    try {
        if (-not (Get-PrinterDriver -Name $candidate -ErrorAction SilentlyContinue)) {
            Add-PrinterDriver -Name $candidate
        }
        $driver = $candidate
        break
    } catch {
        Write-Host "Driver '$candidate' is not available here: $($_.Exception.Message)"
    }
}
if (-not $driver) { throw 'No inbox printer driver could be installed.' }
Write-Host "Using driver: $driver"

$printers = [ordered]@{
    'Front Desk'             = '10.20.1.21'
    'Accounting - 2nd Floor' = '10.20.2.40'
    'HR Laser'               = '10.20.2.41'
    'Executive Suite Color'  = '10.20.3.15'
    'Conference Room B'      = '10.20.3.52'
    'Legal Copier'           = '10.20.4.30'
    'Shipping & Receiving'   = '10.20.9.11'
    'Warehouse Labels'       = '10.20.9.12'
}
foreach ($name in $printers.Keys) {
    $address = $printers[$name]
    $port = "RAW9100-$address"
    if (-not (Get-PrinterPort -Name $port -ErrorAction SilentlyContinue)) {
        Add-PrinterPort -Name $port -PrinterHostAddress $address -PortNumber 9100
    }
    if (-not (Get-Printer -Name $name -ErrorAction SilentlyContinue)) {
        Add-Printer -Name $name -DriverName $driver -PortName $port
    }
    Write-Host "Queue ready: $name -> $address"
}
