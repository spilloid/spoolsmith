package install

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
)

// InstalledQueue is one printer queue as Windows currently has it, joined to
// the port it prints through.
//
// This is a read-only view. It exists so an operator can see what a machine
// already has — and which of those queues SpoolSmith could reproduce
// elsewhere — without knowing any queue's exact name in advance, and without
// running Get-Printer by hand and reading the answer back into a command.
type InstalledQueue struct {
	PrinterName string `json:"printer_name"`
	DriverName  string `json:"driver_name"`
	PortName    string `json:"port_name"`
	// HostAddress, PortNumber and Protocol are the port's settings, and are
	// zero when PortKnown is false.
	HostAddress string `json:"host_address,omitempty"`
	PortNumber  int    `json:"port_number,omitempty"`
	Protocol    int    `json:"protocol,omitempty"`
	// ProtocolName is Protocol in the words Windows' own UI uses. The number
	// is kept alongside it because it is what Get-PrinterPort reports and what
	// a comparison should be written against; the name is for the human
	// reading the output, who should not have to know that 1 means RAW.
	ProtocolName string `json:"protocol_name,omitempty"`
	// PortKnown reports whether the queue's port resolved to a port Windows
	// still has. A queue naming a port that no longer exists is reported as-is
	// rather than dropped from the listing.
	PortKnown bool `json:"port_known"`
	Shared    bool `json:"shared"`
}

// CopyBlockedReason explains why this queue cannot be copied to another
// machine, or returns an empty string when it can.
func (q InstalledQueue) CopyBlockedReason() string {
	if !q.PortKnown {
		return fmt.Sprintf("its port %q is not one Windows still reports; the queue names a port that no longer exists", q.PortName)
	}
	return copyBlockedReason(PortConfiguration{
		PortName:    q.PortName,
		HostAddress: q.HostAddress,
		PortNumber:  q.PortNumber,
		Protocol:    q.Protocol,
	})
}

// Copyable reports whether `spoolsmith copy` could reproduce this queue.
func (q InstalledQueue) Copyable() bool { return q.CopyBlockedReason() == "" }

// copyBlockedReason is the single rule for whether SpoolSmith can faithfully
// reproduce a queue somewhere else.
//
// It lives here, shared by the listing and by CloneQueue itself, so the
// listing can never advertise a queue as copyable that the copy path would
// then refuse — or hide one it would have accepted.
func copyBlockedReason(port PortConfiguration) string {
	if port.Protocol != 0 && port.Protocol != 1 {
		return fmt.Sprintf("it uses port %q with protocol %d (not RAW); SpoolSmith only maps RAW TCP queues, so copying it would produce a different queue", port.PortName, port.Protocol)
	}
	if port.PortNumber != 0 && port.PortNumber != 9100 {
		return fmt.Sprintf("it uses TCP port %d; SpoolSmith only maps the RAW 9100 default, so copying it would produce a different queue", port.PortNumber)
	}
	address := strings.TrimSpace(port.HostAddress)
	if address == "" {
		return fmt.Sprintf("its port %q reports no printer host address; this is not a standard TCP/IP port SpoolSmith can reproduce", port.PortName)
	}
	if net.ParseIP(address) == nil {
		return fmt.Sprintf("its port %q points at %q, which is a host name rather than a literal IP address; recreate the queue against the printer's IP, or capture a profile directly with `profile capture`", port.PortName, address)
	}
	return ""
}

// protocolName renders Windows' own port protocol encoding.
func protocolName(protocol int) string {
	switch protocol {
	case 1:
		return "RAW"
	case 2:
		return "LPR"
	case 0:
		return ""
	default:
		return fmt.Sprintf("unknown (%d)", protocol)
	}
}

type printerInventoryEnvironment interface {
	ListPrinters(ctx context.Context) ([]InstalledQueue, error)
}

// ListPrinters reads every installed queue on this machine.
func ListPrinters(ctx context.Context, env Environment) ([]InstalledQueue, error) {
	lister, ok := env.(printerInventoryEnvironment)
	if !ok {
		return nil, errors.New("install: this platform cannot list installed printers")
	}
	return lister.ListPrinters(ctx)
}

// listPrintersCommand joins Get-Printer to Get-PrinterPort in one call.
//
// The emitted property names are the InstalledQueue JSON tags exactly. Go's
// decoder falls back to a case-insensitive field match, but that does not
// bridge an underscore, so PascalCase names would decode to empty structs with
// a nil error — silently losing every queue it just read.
//
// The result is assembled into a JSON array by hand rather than piped through
// ConvertTo-Json, because ConvertTo-Json emits a bare object rather than a
// one-element array when exactly one queue exists, and Windows PowerShell 5.1
// has no -AsArray to suppress that.
func listPrintersCommand() string {
	return powerShellCommand(`$ports = @{};
foreach ($port in @(Get-PrinterPort -ErrorAction SilentlyContinue)) { $ports[[string]$port.Name] = $port };
$items = @();
foreach ($printer in @(Get-Printer -ErrorAction Stop)) {
  $name = [string]$printer.PortName;
  $port = $null; if ($name -and $ports.ContainsKey($name)) { $port = $ports[$name] };
  $address = ''; $number = 0; $protocol = 0;
  if ($port -ne $null) {
    $address = [string]$port.PrinterHostAddress;
    if ($port.PortNumber -ne $null) { $number = [int]$port.PortNumber };
    if ($port.Protocol -ne $null) { $protocol = [int]$port.Protocol };
  }
  $items += [PSCustomObject]@{
    printer_name=[string]$printer.Name;
    driver_name=[string]$printer.DriverName;
    port_name=$name;
    host_address=$address;
    port_number=$number;
    protocol=$protocol;
    port_known=($port -ne $null);
    shared=[bool]$printer.Shared
  }
};
'[' + (@($items | ForEach-Object { $_ | ConvertTo-Json -Compress }) -join ',') + ']'`)
}

func decodeInstalledQueues(output string) ([]InstalledQueue, error) {
	trimmed := strings.TrimSpace(output)
	if trimmed == "" {
		return nil, nil
	}
	var queues []InstalledQueue
	if err := json.Unmarshal([]byte(trimmed), &queues); err != nil {
		return nil, fmt.Errorf("install: decode installed printers: %w", err)
	}
	// Derived in Go rather than in PowerShell so the name and the number can
	// never disagree about the same port.
	for index := range queues {
		queues[index].ProtocolName = protocolName(queues[index].Protocol)
	}
	return queues, nil
}
