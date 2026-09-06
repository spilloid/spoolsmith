//go:build windows

package install

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
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
	output, err := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-Command", command).CombinedOutput()
	return string(output), err
}
