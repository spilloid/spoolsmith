//go:build windows

package install

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"syscall"
)

type windowsEnvironment struct{}

// NewEnvironment returns the real Windows-backed install environment.
func NewEnvironment() Environment {
	return windowsEnvironment{}
}

func (windowsEnvironment) IsElevated(ctx context.Context) (bool, error) {
	output, err := runPowerShell(ctx, "([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltinRole]::Administrator)")
	if err != nil {
		return false, err
	}
	switch strings.ToLower(strings.TrimSpace(output)) {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, fmt.Errorf("install: unexpected elevation result %q", strings.TrimSpace(output))
	}
}

func (windowsEnvironment) DriverPresent(ctx context.Context, driverName string) (bool, error) {
	return checkDriverPresent(ctx, runPowerShell, driverName)
}

func (windowsEnvironment) Run(ctx context.Context, command string) (string, error) {
	return runPowerShell(ctx, command)
}

func (windowsEnvironment) LookupPrinter(ctx context.Context, printerName string) (PrinterConfiguration, error) {
	command, err := lookupPrinterCommand(printerName)
	if err != nil {
		return PrinterConfiguration{}, err
	}
	output, err := runPowerShell(ctx, command)
	if err != nil {
		return PrinterConfiguration{}, err
	}
	var configuration PrinterConfiguration
	if strings.TrimSpace(output) == "null" {
		return configuration, ErrPrinterNotFound
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(output)), &configuration); err != nil {
		return PrinterConfiguration{}, fmt.Errorf("install: decode printer configuration: %w", err)
	}
	return configuration, nil
}

func runPowerShell(ctx context.Context, command string) (string, error) {
	process := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-Command", command)
	process.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	output, err := process.CombinedOutput()
	return string(output), err
}

func (windowsEnvironment) DriverNames(ctx context.Context) ([]string, error) {
	output, err := runPowerShell(ctx, powerShellCommand("ConvertTo-Json -InputObject @(Get-PrinterDriver -ErrorAction Stop | Select-Object -ExpandProperty Name | Sort-Object -Unique)"))
	if err != nil {
		return nil, fmt.Errorf("list registered drivers: %w: %s", err, output)
	}
	var names []string
	if err := json.Unmarshal([]byte(output), &names); err != nil {
		return nil, err
	}
	return names, nil
}
