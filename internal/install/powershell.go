package install

import (
	"context"
	"fmt"
	"strings"
)

type powerShellRunner func(context.Context, string) (string, error)

func driverPresenceCommand(driverName string) (string, error) {
	if err := validatePlanValue("Windows driver name", driverName); err != nil {
		return "", err
	}
	if strings.TrimSpace(driverName) == "" {
		return "", fmt.Errorf("install: Windows driver name is empty")
	}
	query := "$driver = @(Get-PrinterDriver -ErrorAction Stop | Where-Object { $_.Name -eq " + powerShellString(driverName) + " }); " +
		"if ($driver.Count -gt 0) { 'True' } else { 'False' }"
	return powerShellCommand(query), nil
}

func checkDriverPresent(ctx context.Context, runner powerShellRunner, driverName string) (bool, error) {
	command, err := driverPresenceCommand(driverName)
	if err != nil {
		return false, err
	}
	output, err := runner(ctx, command)
	if err != nil {
		if diagnostic := strings.TrimSpace(output); diagnostic != "" {
			return false, fmt.Errorf("%s: %w", diagnostic, err)
		}
		return false, err
	}
	switch strings.ToLower(strings.TrimSpace(output)) {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, fmt.Errorf("install: unexpected driver presence result %q", strings.TrimSpace(output))
	}
}

func lookupPrinterCommand(printerName string) (string, error) {
	if err := validatePlanValue("printer name", printerName); err != nil {
		return "", err
	}
	if strings.TrimSpace(printerName) == "" {
		return "", fmt.Errorf("install: printer name is empty")
	}
	command := "$printer = Get-Printer -Name " + powerShellString(printerName) + " -ErrorAction Stop; " +
		"$port = Get-PrinterPort -Name $printer.PortName -ErrorAction Stop; " +
		"[PSCustomObject]@{PrinterName=$printer.Name;PortName=$port.Name;DriverName=$printer.DriverName} | ConvertTo-Json -Compress"
	return powerShellCommand(command), nil
}
