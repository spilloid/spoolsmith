package install

import "fmt"

// These guards execute again at mutation time. Inventory failures are fatal;
// absence is determined by filtering a successful enumeration, never by hiding errors.
func installCommands(plan Plan) []string {
	name, port, driver, ip := powerShellString(plan.PrinterName), powerShellString(plan.PortName), powerShellString(plan.DriverName), powerShellString(plan.IPAddress)
	queue := "$printer = @(Get-Printer -ErrorAction Stop | Where-Object { $_.Name -eq " + name + " }); "
	portQuery := "$port = @(Get-PrinterPort -ErrorAction Stop | Where-Object { $_.Name -eq " + port + " }); "
	portGuard := "if ($port.Count -gt 1) { throw 'Multiple matching ports' }; if ($port.Count -eq 1 -and ($port[0].PrinterHostAddress -ne " + ip + " -or $port[0].PortNumber -ne 9100 -or $port[0].Protocol -ne 1)) { throw 'Existing port conflicts with requested RAW 9100 endpoint; choose a different target or repair the port' }; "
	queueGuard := "if ($printer.Count -gt 1) { throw 'Multiple matching printers' }; "
	differs := "($printer[0].DriverName -ne " + driver + " -or $printer[0].PortName -ne " + port + ")"
	if !plan.UpdateExisting {
		queueGuard += "if ($printer.Count -eq 1 -and " + differs + ") { throw 'Existing printer has different settings; use configure --profile to review an update' }; "
	}
	first := queue + queueGuard + portQuery + portGuard + "if ($port.Count -eq 0) { Add-PrinterPort -Name " + port + " -PrinterHostAddress " + ip + " -PortNumber 9100 -ErrorAction Stop; 'Created port' } else { 'Unchanged port' }"
	second := portQuery + portGuard + "if ($port.Count -ne 1) { throw 'Expected printer port is missing' }; " + queue + queueGuard + "if ($printer.Count -eq 0) { Add-Printer -Name " + name + " -DriverName " + driver + " -PortName " + port + " -ErrorAction Stop; 'Created printer' }"
	if plan.UpdateExisting {
		second += " elseif (" + differs + ") { Set-Printer -InputObject $printer[0] -DriverName " + driver + " -PortName " + port + " -ErrorAction Stop; 'Updated printer' }"
	}
	second += " else { 'Unchanged printer' }"
	return []string{powerShellCommand(first), powerShellCommand(second)}
}

func uninstallCommands(plan Plan, purgeDriver bool) []string {
	name, port, driver := powerShellString(plan.PrinterName), powerShellString(plan.PortName), powerShellString(plan.DriverName)
	commands := []string{
		powerShellCommand("$printer = @(Get-Printer -ErrorAction Stop | Where-Object { $_.Name -eq " + name + " }); if ($printer.Count -gt 1) { throw 'Multiple matching printers' }; if ($printer.Count -eq 1) { if ($printer[0].PortName -ne " + port + " -or $printer[0].DriverName -ne " + driver + ") { throw 'Printer changed after plan was shown; retry removal' }; Remove-Printer -InputObject $printer[0] -ErrorAction Stop; 'Removed printer' } else { 'Printer already absent' }"),
		powerShellCommand("$users = @(Get-Printer -ErrorAction Stop | Where-Object { $_.PortName -eq " + port + " }); $port = @(Get-PrinterPort -ErrorAction Stop | Where-Object { $_.Name -eq " + port + " }); if ($users.Count -gt 0) { 'Retained shared port' } elseif (-not " + port + ".StartsWith('SpoolSmith-', [StringComparison]::OrdinalIgnoreCase)) { 'Retained external port' } elseif ($port.Count -gt 0) { $port | Remove-PrinterPort -ErrorAction Stop; 'Removed unused SpoolSmith port' } else { 'Port already absent' }"),
	}
	if purgeDriver {
		commands = append(commands, powerShellCommand(fmt.Sprintf("$users = @(Get-Printer -ErrorAction Stop | Where-Object { $_.DriverName -eq %s }); $drivers = @(Get-PrinterDriver -ErrorAction Stop | Where-Object { $_.Name -eq %s }); if ($users.Count -gt 0) { 'Retained shared driver' } elseif ($drivers.Count -gt 0) { $drivers | Remove-PrinterDriver -ErrorAction Stop; 'Removed unused driver' } else { 'Driver already absent' }", driver, driver)))
	}
	return commands
}
