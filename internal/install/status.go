package install

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
)

// LocalConfiguration contains only Windows inventory, never network evidence.
type LocalConfiguration struct {
	QueuePresent  bool   `json:"queue_present"`
	PrinterName   string `json:"printer_name"`
	DriverName    string `json:"driver_name"`
	DriverPresent bool   `json:"driver_present"`
	PortName      string `json:"port_name"`
	PortPresent   bool   `json:"port_present"`
	PortMonitor   string `json:"port_monitor,omitempty"`
	IPPVerified   bool   `json:"ipp_verified,omitempty"`
	Address       string `json:"address"`
	Protocol      int    `json:"protocol"`
	PortNumber    int    `json:"port_number"`
}

type localInventoryEnvironment interface {
	LocalConfiguration(context.Context, string) (LocalConfiguration, error)
}

type LocalStatus struct {
	Compliant  bool               `json:"compliant"`
	Scope      string             `json:"scope"`
	Actual     LocalConfiguration `json:"actual"`
	Mismatches []string           `json:"mismatches"`
}

// CheckStatus verifies the actual local queue, driver registration and TCP/IP
// port. Captured evidence and reachability cannot make this check pass.
func CheckStatus(ctx context.Context, env Environment, profile Profile) (LocalStatus, error) {
	status := LocalStatus{Scope: "local-configuration-only", Mismatches: []string{}}
	if err := profile.Validate(); err != nil {
		return status, err
	}
	if profile.PortType == "usb" {
		return status, errors.New("status: USB printer files do not have a fixed IP endpoint; inspect the local USB queue instead")
	}
	reader, ok := env.(localInventoryEnvironment)
	if !ok {
		return status, errors.New("status: environment does not support local inventory")
	}
	actual, err := reader.LocalConfiguration(ctx, profile.PrinterName)
	status.Actual = actual
	if err != nil {
		return status, err
	}
	if profile.PortType == "ipp" {
		checks := []struct {
			match  bool
			reason string
		}{
			{actual.QueuePresent && strings.EqualFold(actual.PrinterName, profile.PrinterName), "queue is absent or name differs"},
			{actual.DriverPresent, "queue driver is not registered"},
			// Windows may have upgraded the class driver to the printer's own
			// driver; that is accepted only when the endpoint is verified.
			{strings.EqualFold(actual.DriverName, profile.DriverName) || (actual.IPPVerified && sameDriverModel(actual.DriverName, profile.Evidence.IPPModel)), "driver differs"},
			{actual.PortPresent, "IPP port is absent"},
			{isIPPMonitor(actual.PortMonitor) || (actual.IPPVerified && strings.EqualFold(actual.PortMonitor, "WSD Port Monitor")), "port is not an IPP port"},
			{sameIPPEndpoint(actual.PortName, profile.IPPURL) || sameIPPEndpoint(actual.Address, profile.IPPURL), "IPP port endpoint differs or is not visible"},
		}
		for _, check := range checks {
			if !check.match {
				status.Mismatches = append(status.Mismatches, check.reason)
			}
		}
		status.Compliant = len(status.Mismatches) == 0
		return status, nil
	}
	checks := []struct {
		match  bool
		reason string
	}{
		{actual.QueuePresent && strings.EqualFold(actual.PrinterName, profile.PrinterName), "queue is absent or name differs"},
		{actual.DriverPresent, "queue driver is not registered"},
		{strings.EqualFold(actual.DriverName, profile.DriverName), "driver differs"},
		{actual.PortPresent && strings.EqualFold(actual.PortName, managedPortPrefix+net.ParseIP(profile.Target).String()), "managed port is absent or differs"},
		{net.ParseIP(profile.Target).Equal(net.ParseIP(actual.Address)), "target address differs"},
		{actual.Protocol == 1, "port protocol is not RAW"},
		{actual.PortNumber == 9100, "port number is not 9100"},
	}
	for _, check := range checks {
		if !check.match {
			status.Mismatches = append(status.Mismatches, check.reason)
		}
	}
	status.Compliant = len(status.Mismatches) == 0
	return status, nil
}

func readLocalConfiguration(ctx context.Context, runner powerShellRunner, name string) (LocalConfiguration, error) {
	var actual LocalConfiguration
	if strings.TrimSpace(name) == "" {
		return actual, errors.New("status: printer name is required")
	}
	if err := validatePlanValue("printer name", name); err != nil {
		return actual, err
	}
	command := powerShellCommand(ippConnectionFunctions + `$q = @(Get-Printer -ErrorAction Stop | Where-Object { $_.Name -eq ` + powerShellString(name) + ` });
if ($q.Count -gt 1) { throw 'Multiple matching queues' };
if ($q.Count -eq 0) { ConvertTo-Json -InputObject @{queue_present=$false}; exit 0 };
$p = @(Get-PrinterPort -ErrorAction Stop | Where-Object { $_.Name -eq $q[0].PortName });
$d = @(Get-PrinterDriver -ErrorAction Stop | Where-Object { $_.Name -eq $q[0].DriverName });
if ($p.Count -gt 1) { throw 'Multiple matching ports' };
$state = @{queue_present=$true; printer_name=$q[0].Name; driver_name=$q[0].DriverName; driver_present=($d.Count -gt 0); port_name=$q[0].PortName; port_present=($p.Count -eq 1)};
if ($p.Count -eq 1) { $state.address=[string]$p[0].PrinterHostAddress; $state.port_monitor=[string]$p[0].PortMonitor; $state.protocol=[int]$p[0].Protocol; $state.port_number=[int]$p[0].PortNumber };
if ($p.Count -eq 1 -and $p[0].PortMonitor -in @('Internet Port','IPP Port Monitor','WSD Port Monitor')) { $connection = Get-SpoolSmithIPPConnection $q[0] $p[0]; if ($connection.Verified) { $state.ipp_verified=$true; $state.address=[string]$connection.Endpoint } };
ConvertTo-Json -InputObject $state`)
	output, err := runner(ctx, command)
	if err != nil {
		return actual, fmt.Errorf("status: read local inventory: %w: %s", err, strings.TrimSpace(output))
	}
	if strings.TrimSpace(output) == "null" {
		return actual, errors.New("status: unexpected null inventory")
	}
	if err := json.Unmarshal([]byte(output), &actual); err != nil {
		return actual, fmt.Errorf("status: decode local inventory: %w", err)
	}
	return actual, nil
}

func isIPPMonitor(monitor string) bool {
	return strings.EqualFold(monitor, "Internet Port") || strings.EqualFold(monitor, "IPP Port Monitor")
}
