// Package install builds and executes reviewable Windows printer installation plans.
package install

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/spilloid/spoolsmith/internal/catalog"
)

// Environment is the seam between orchestration logic and the real OS.
type Environment interface {
	IsElevated(ctx context.Context) (bool, error)
	DriverPresent(ctx context.Context, driverName string) (bool, error)
	Run(ctx context.Context, command string) (output string, err error)
}

// Plan is the complete, reviewable set of commands for one operation.
type Plan struct {
	IPAddress      string                `json:"ip_address,omitempty"`
	PrinterName    string                `json:"printer_name"`
	PortName       string                `json:"port_name"`
	DriverName     string                `json:"driver_name,omitempty"`
	Family         catalog.Family        `json:"family,omitempty"`
	Driver         catalog.DriverPackage `json:"driver,omitempty"`
	Commands       []string              `json:"commands"`
	ForcedOverride bool                  `json:"forced_override"`
}

// Result records the plan and every command attempted.
type Result struct {
	Plan Plan            `json:"plan"`
	Ran  []CommandResult `json:"ran"`
}

// CommandResult records one command's output and status.
type CommandResult struct {
	Command string `json:"command"`
	Output  string `json:"output,omitempty"`
	Err     error  `json:"-"`
}

// MarshalJSON preserves useful diagnostics instead of encoding an error
// interface as an empty JSON object.
func (r CommandResult) MarshalJSON() ([]byte, error) {
	type wireResult struct {
		Command string `json:"command"`
		Output  string `json:"output,omitempty"`
		Error   string `json:"error,omitempty"`
	}
	wire := wireResult{Command: r.Command, Output: r.Output}
	if r.Err != nil {
		wire.Error = r.Err.Error()
	}
	return json.Marshal(wire)
}

var (
	ErrNotElevated      = errors.New("install: administrator privileges are required")
	ErrDriverNotPresent = errors.New("install: driver not found — install it via Windows Update or run the vendor package manually first, then retry")
	ErrNotConfirmed     = errors.New("install: operation was not confirmed")
)

const unverifiedWindowsDriverName = "Windows driver name not yet verified for this family — run `Get-PrinterDriver` after staging the real vendor package on a Windows machine and populate `WindowsDriverName`"

// PrinterConfiguration is the installed configuration needed for an exact uninstall.
type PrinterConfiguration struct {
	PrinterName string `json:"printer_name"`
	PortName    string `json:"port_name"`
	DriverName  string `json:"driver_name"`
}

type printerLookupEnvironment interface {
	LookupPrinter(ctx context.Context, printerName string) (PrinterConfiguration, error)
}

// BuildPlan turns a fully resolved catalog result into literal PowerShell commands.
func BuildPlan(ip string, resolution catalog.ResolutionResult) (Plan, error) {
	if resolution.Family == nil || resolution.Driver == nil {
		return Plan{}, errors.New("install: cannot build a plan from an unresolved catalog result")
	}
	if resolution.Driver.FamilyID != resolution.Family.ID {
		return Plan{}, fmt.Errorf("install: driver family %q does not match resolved family %q", resolution.Driver.FamilyID, resolution.Family.ID)
	}
	if err := validatePlanValue("IP address", ip); err != nil {
		return Plan{}, err
	}
	parsedIP := net.ParseIP(strings.TrimSpace(ip))
	if parsedIP == nil {
		return Plan{}, fmt.Errorf("install: invalid IP address %q", ip)
	}
	if err := validatePlanValue("printer name", resolution.NormalizedModel); err != nil {
		return Plan{}, err
	}
	if strings.TrimSpace(resolution.NormalizedModel) == "" {
		return Plan{}, errors.New("install: resolved model name is empty")
	}
	if err := validateCatalogValues(*resolution.Family, *resolution.Driver); err != nil {
		return Plan{}, err
	}
	if strings.TrimSpace(resolution.Driver.WindowsDriverName) == "" {
		return Plan{}, errors.New("install: " + unverifiedWindowsDriverName)
	}

	ip = parsedIP.String()
	portName := "SpoolSmith-" + ip
	plan := Plan{
		IPAddress:   ip,
		PrinterName: resolution.NormalizedModel,
		PortName:    portName,
		DriverName:  resolution.Driver.WindowsDriverName,
		Family:      *resolution.Family,
		Driver:      *resolution.Driver,
	}
	plan.Commands = []string{
		powerShellCommand(fmt.Sprintf("Add-PrinterPort -Name %s -PrinterHostAddress %s -ErrorAction Stop", powerShellString(plan.PortName), powerShellString(plan.IPAddress))),
		powerShellCommand(fmt.Sprintf("Add-Printer -Name %s -DriverName %s -PortName %s -ErrorAction Stop", powerShellString(plan.PrinterName), powerShellString(plan.DriverName), powerShellString(plan.PortName))),
	}
	return plan, nil
}

// PreflightResult records which non-mutating safety checks completed.
type PreflightResult struct {
	Elevated      bool `json:"elevated"`
	DriverChecked bool `json:"driver_checked"`
	DriverPresent bool `json:"driver_present"`
}

// Preflight checks every condition that must hold before installation can mutate Windows.
func Preflight(ctx context.Context, env Environment, plan Plan) (PreflightResult, error) {
	var result PreflightResult
	if env == nil {
		return result, errors.New("install: environment is nil")
	}
	elevated, err := env.IsElevated(ctx)
	if err != nil {
		return result, fmt.Errorf("install: check administrator privileges: %w", err)
	}
	result.Elevated = elevated
	if !elevated {
		return result, ErrNotElevated
	}
	present, err := env.DriverPresent(ctx, plan.DriverName)
	result.DriverChecked = true
	if err != nil {
		return result, fmt.Errorf("install: driver presence check failed: %w; guidance: %v", err, ErrDriverNotPresent)
	}
	result.DriverPresent = present
	if !present {
		return result, ErrDriverNotPresent
	}
	return result, nil
}

// Install runs the fail-closed preflight, then executes the confirmed plan in order.
func Install(ctx context.Context, env Environment, plan Plan, confirmed bool) (Result, error) {
	result := Result{Plan: plan}
	if _, err := Preflight(ctx, env, plan); err != nil {
		return result, err
	}
	if !confirmed {
		return result, ErrNotConfirmed
	}

	for _, command := range plan.Commands {
		output, err := env.Run(ctx, command)
		result.Ran = append(result.Ran, CommandResult{Command: command, Output: output, Err: err})
		if err != nil {
			return result, fmt.Errorf("install: run %q: %w", command, err)
		}
	}
	return result, nil
}

// LookupPrinter reads the installed port and driver associated with printerName.
func LookupPrinter(ctx context.Context, env Environment, printerName string) (PrinterConfiguration, error) {
	if err := validatePlanValue("printer name", printerName); err != nil {
		return PrinterConfiguration{}, err
	}
	if strings.TrimSpace(printerName) == "" {
		return PrinterConfiguration{}, errors.New("install: printer name is empty")
	}
	lookup, ok := env.(printerLookupEnvironment)
	if !ok {
		return PrinterConfiguration{}, errors.New("install: environment does not support printer lookup")
	}
	return lookup.LookupPrinter(ctx, printerName)
}

// BuildUninstallPlan creates the exact removal commands for an installed configuration.
func BuildUninstallPlan(printerName, portName, driverName string, purgeDriver bool) (Plan, error) {
	for _, item := range []struct {
		field string
		value string
	}{
		{field: "printer name", value: printerName},
		{field: "port name", value: portName},
		{field: "driver name", value: driverName},
	} {
		if err := validatePlanValue(item.field, item.value); err != nil {
			return Plan{}, err
		}
	}
	if strings.TrimSpace(printerName) == "" || strings.TrimSpace(portName) == "" {
		return Plan{}, errors.New("install: printer name and port name are required for uninstall")
	}
	if purgeDriver && strings.TrimSpace(driverName) == "" {
		return Plan{}, errors.New("install: driver name is required when purging the driver")
	}

	plan := Plan{
		PrinterName: printerName,
		PortName:    portName,
		DriverName:  driverName,
		Commands: []string{
			powerShellCommand(fmt.Sprintf("Remove-Printer -Name %s -ErrorAction Stop", powerShellString(printerName))),
			powerShellCommand(fmt.Sprintf("Remove-PrinterPort -Name %s -ErrorAction Stop", powerShellString(portName))),
		},
	}
	if purgeDriver {
		plan.Commands = append(plan.Commands, powerShellCommand(fmt.Sprintf("Remove-PrinterDriver -Name %s -ErrorAction Stop", powerShellString(driverName))))
	}
	return plan, nil
}

// PreflightUninstall verifies that uninstall is running with administrator privileges.
func PreflightUninstall(ctx context.Context, env Environment) (PreflightResult, error) {
	var result PreflightResult
	if env == nil {
		return result, errors.New("install: environment is nil")
	}
	elevated, err := env.IsElevated(ctx)
	if err != nil {
		return result, fmt.Errorf("install: check administrator privileges: %w", err)
	}
	result.Elevated = elevated
	if !elevated {
		return result, ErrNotElevated
	}
	return result, nil
}

// Uninstall removes the named printer and port, and optionally its driver.
func Uninstall(ctx context.Context, env Environment, printerName, portName, driverName string, purgeDriver bool) (Result, error) {
	plan, err := BuildUninstallPlan(printerName, portName, driverName, purgeDriver)
	result := Result{Plan: plan}
	if err != nil {
		return result, err
	}
	if _, err := PreflightUninstall(ctx, env); err != nil {
		return result, err
	}
	for _, command := range plan.Commands {
		output, err := env.Run(ctx, command)
		result.Ran = append(result.Ran, CommandResult{Command: command, Output: output, Err: err})
		if err != nil {
			return result, fmt.Errorf("install: run %q: %w", command, err)
		}
	}
	return result, nil
}

func powerShellString(value string) string {
	replacer := strings.NewReplacer(
		"`", "``",
		"$", "`$",
		"\"", "`\"",
		"\u2018", "`\u2018",
		"\u2019", "`\u2019",
		"\u201c", "`\u201c",
		"\u201d", "`\u201d",
	)
	return "\"" + replacer.Replace(value) + "\""
}

func powerShellCommand(command string) string {
	return "$ErrorActionPreference = 'Stop'; try { " + command + " } catch { Write-Error $_.Exception.Message; exit 1 }"
}

func validatePlanValue(field, value string) error {
	for _, r := range value {
		if r < 0x20 {
			return fmt.Errorf("install: %s contains prohibited C0 control character U+%04X", field, r)
		}
		if r >= 0x202a && r <= 0x202e || r >= 0x2066 && r <= 0x2069 {
			return fmt.Errorf("install: %s contains prohibited bidirectional control character U+%04X", field, r)
		}
	}
	return nil
}

func validateCatalogValues(family catalog.Family, driver catalog.DriverPackage) error {
	values := []struct {
		field string
		value string
	}{
		{field: "family ID", value: family.ID},
		{field: "family manufacturer", value: family.Manufacturer},
		{field: "driver family ID", value: driver.FamilyID},
		{field: "driver label", value: driver.Name},
		{field: "Windows driver name", value: driver.WindowsDriverName},
		{field: "driver source", value: driver.Source},
		{field: "driver version", value: driver.Version},
		{field: "driver SHA-256", value: driver.SHA256},
		{field: "driver strategy", value: driver.Strategy},
	}
	for index, alias := range family.Aliases {
		values = append(values, struct {
			field string
			value string
		}{field: fmt.Sprintf("family alias %d", index+1), value: alias})
	}
	for _, item := range values {
		if err := validatePlanValue(item.field, item.value); err != nil {
			return err
		}
	}
	return nil
}
