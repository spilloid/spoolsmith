package install

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
)

// PortConfiguration is an installed standard TCP/IP port as Windows reports it.
type PortConfiguration struct {
	PortName    string `json:"port_name"`
	HostAddress string `json:"host_address"`
	PortNumber  int    `json:"port_number"`
	// Protocol is Windows' own encoding: 1 is RAW, 2 is LPR.
	Protocol int `json:"protocol"`
}

// DriverExport describes driver files copied out of the Windows driver store.
type DriverExport struct {
	// PublishedName is the driver store's oem#.inf name.
	PublishedName string `json:"published_name"`
	// OriginalName is the vendor's own INF file name, which is also the INF's
	// name inside the export directory.
	OriginalName string `json:"original_name"`
	Provider     string `json:"provider,omitempty"`
	ClassName    string `json:"class_name,omitempty"`
}

// portLookupEnvironment and driverExportEnvironment are optional capabilities,
// asserted rather than added to Environment, so non-Windows builds and the
// existing fake environments in the test suite stay valid.
type portLookupEnvironment interface {
	LookupPort(ctx context.Context, portName string) (PortConfiguration, error)
}

type driverExportEnvironment interface {
	ExportDriver(ctx context.Context, driverName, destDir string) (DriverExport, error)
}

// ErrPortNotFound reports that a queue names a port Windows does not have.
var ErrPortNotFound = errors.New("install: printer port not found")

// ClonedQueue is one installed queue read back off this machine, in the terms
// SpoolSmith needs to reproduce it somewhere else.
type ClonedQueue struct {
	PrinterName string `json:"printer_name"`
	DriverName  string `json:"driver_name"`
	PortName    string `json:"port_name"`
	HostAddress string `json:"host_address"`
}

// CloneQueue reads an installed queue and its port, and reports the target
// address SpoolSmith would need to recreate it.
//
// It fails closed on any queue SpoolSmith could not faithfully reproduce —
// a non-RAW protocol, a port that is not 9100, or a port whose address is a
// name rather than a literal IP — rather than emitting a bundle that would
// quietly build a different queue on the next machine.
func CloneQueue(ctx context.Context, env Environment, printerName string) (ClonedQueue, error) {
	configuration, err := LookupPrinter(ctx, env, printerName)
	if err != nil {
		return ClonedQueue{}, err
	}
	lookup, ok := env.(portLookupEnvironment)
	if !ok {
		return ClonedQueue{}, errors.New("install: this platform cannot read printer ports")
	}
	port, err := lookup.LookupPort(ctx, configuration.PortName)
	if err != nil {
		return ClonedQueue{}, err
	}
	if port.Protocol != 0 && port.Protocol != 1 {
		return ClonedQueue{}, fmt.Errorf("clone: queue %q uses port %q with protocol %d (not RAW); SpoolSmith only maps RAW TCP queues, so cloning it would produce a different queue", printerName, port.PortName, port.Protocol)
	}
	if port.PortNumber != 0 && port.PortNumber != 9100 {
		return ClonedQueue{}, fmt.Errorf("clone: queue %q uses TCP port %d; SpoolSmith only maps the RAW 9100 default, so cloning it would produce a different queue", printerName, port.PortNumber)
	}
	address := strings.TrimSpace(port.HostAddress)
	if address == "" {
		return ClonedQueue{}, fmt.Errorf("clone: port %q reports no printer host address; this is not a standard TCP/IP port SpoolSmith can reproduce", port.PortName)
	}
	if net.ParseIP(address) == nil {
		return ClonedQueue{}, fmt.Errorf("clone: port %q points at %q, which is a host name rather than a literal IP address; recreate the queue against the printer's IP, or capture a profile directly with `profile capture`", port.PortName, address)
	}
	return ClonedQueue{
		PrinterName: configuration.PrinterName,
		DriverName:  configuration.DriverName,
		PortName:    configuration.PortName,
		HostAddress: net.ParseIP(address).String(),
	}, nil
}

// ExportDriver copies the named registered driver's files into destDir.
func ExportDriver(ctx context.Context, env Environment, driverName, destDir string) (DriverExport, error) {
	exporter, ok := env.(driverExportEnvironment)
	if !ok {
		return DriverExport{}, errors.New("install: this platform cannot export drivers")
	}
	return exporter.ExportDriver(ctx, driverName, destDir)
}

func lookupPortCommand(portName string) (string, error) {
	if err := validatePlanValue("port name", portName); err != nil {
		return "", err
	}
	if strings.TrimSpace(portName) == "" {
		return "", errors.New("install: port name is empty")
	}
	command := "$ports = @(Get-PrinterPort -ErrorAction Stop | Where-Object { $_.Name -eq " + powerShellString(portName) + " }); " +
		"if ($ports.Count -eq 0) { 'null' } elseif ($ports.Count -ne 1) { throw 'Multiple matching ports' } else { $port = $ports[0]; " +
		"[PSCustomObject]@{port_name=$port.Name;host_address=[string]$port.PrinterHostAddress;port_number=[int]$port.PortNumber;protocol=[int]$port.Protocol} | ConvertTo-Json -Compress }"
	return powerShellCommand(command), nil
}

// exportDriverCommand resolves a registered printer driver to its driver-store
// package and copies that package out with pnputil.
//
// Get-PrinterDriver reports the INF's path in the driver store, not its
// published oem#.inf name, and pnputil /export-driver needs the published name.
// pnputil /enum-drivers is the documented mapping between the two. An
// ambiguous or absent mapping stops the export rather than exporting a package
// that was not asked for.
func exportDriverCommand(driverName, destDir string) (string, error) {
	if err := validatePlanValue("Windows driver name", driverName); err != nil {
		return "", err
	}
	if err := validatePlanValue("export directory", destDir); err != nil {
		return "", err
	}
	if strings.TrimSpace(driverName) == "" {
		return "", errors.New("install: Windows driver name is empty")
	}
	if strings.TrimSpace(destDir) == "" {
		return "", errors.New("install: export directory is empty")
	}
	script := fmt.Sprintf(`$drivers = @(Get-PrinterDriver -ErrorAction Stop | Where-Object { $_.Name -eq %[1]s });
if ($drivers.Count -eq 0) { throw 'Driver is not registered on this machine' };
if ($drivers.Count -gt 1) { throw 'Multiple registered drivers share this name' };
$infPath = [string]$drivers[0].InfPath;
if ([string]::IsNullOrWhiteSpace($infPath)) { throw 'Windows reports no INF path for this driver; it cannot be exported' };
$original = [IO.Path]::GetFileName($infPath);
$enumerated = & pnputil.exe /enum-drivers; if ($LASTEXITCODE -ne 0) { throw 'Cannot enumerate the driver store' };
$published = $null; $provider = ''; $class = ''; $current = $null; $currentProvider = ''; $currentClass = ''; $matches = 0;
foreach ($line in $enumerated) {
  if ($line -match '^\s*Published Name\s*:\s*(.+?)\s*$') { $current = $Matches[1]; $currentProvider = ''; $currentClass = ''; continue }
  if ($line -match '^\s*Provider Name\s*:\s*(.+?)\s*$') { $currentProvider = $Matches[1]; continue }
  if ($line -match '^\s*Class Name\s*:\s*(.+?)\s*$') { $currentClass = $Matches[1]; continue }
  if ($line -match '^\s*Original Name\s*:\s*(.+?)\s*$') {
    if ($Matches[1] -ieq $original) { $matches++; $published = $current; $provider = $currentProvider; $class = $currentClass }
  }
}
if ($matches -eq 0) { throw ('No driver-store package publishes ' + $original + '; an inbox or Windows Update driver cannot be exported, and the target machine will need to obtain it the same way this one did') };
if ($matches -gt 1) { throw ('Driver store publishes ' + $original + ' more than once; refusing to guess which package to export') };
$dest = %[2]s;
New-Item -ItemType Directory -Path $dest -Force -ErrorAction Stop | Out-Null;
& pnputil.exe /export-driver $published $dest; if ($LASTEXITCODE -ne 0) { throw 'Driver export failed' };
$exported = @(Get-ChildItem -LiteralPath $dest -Filter $original -Recurse -File);
if ($exported.Count -ne 1) { throw 'Exported package does not contain exactly one copy of the expected INF' };
[PSCustomObject]@{published_name=$published;original_name=$original;provider=$provider;class_name=$class} | ConvertTo-Json -Compress`,
		powerShellString(driverName), powerShellString(destDir))
	return powerShellCommand(script), nil
}

func decodePortConfiguration(output string) (PortConfiguration, error) {
	trimmed := strings.TrimSpace(output)
	if trimmed == "null" {
		return PortConfiguration{}, ErrPortNotFound
	}
	var configuration PortConfiguration
	if err := json.Unmarshal([]byte(trimmed), &configuration); err != nil {
		return PortConfiguration{}, fmt.Errorf("install: decode port configuration: %w", err)
	}
	return configuration, nil
}

func decodeDriverExport(output string) (DriverExport, error) {
	trimmed := strings.TrimSpace(output)
	var export DriverExport
	if err := json.Unmarshal([]byte(trimmed), &export); err != nil {
		return DriverExport{}, fmt.Errorf("install: decode driver export: %w", err)
	}
	if strings.TrimSpace(export.PublishedName) == "" || strings.TrimSpace(export.OriginalName) == "" {
		return DriverExport{}, errors.New("install: driver export did not report a package name")
	}
	return export, nil
}

// FindExportedINF locates the exported INF beneath destDir and returns its
// path relative to destDir, using forward slashes so it can be recorded in a
// bundle manifest directly.
func FindExportedINF(destDir, originalName string) (string, error) {
	var found []string
	err := filepath.Walk(destDir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.EqualFold(info.Name(), originalName) {
			return nil
		}
		rel, relErr := filepath.Rel(destDir, p)
		if relErr != nil {
			return relErr
		}
		found = append(found, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return "", err
	}
	switch len(found) {
	case 1:
		return found[0], nil
	case 0:
		return "", fmt.Errorf("install: exported package does not contain %q", originalName)
	default:
		return "", fmt.Errorf("install: exported package contains %d copies of %q; refusing to guess which one to stage", len(found), originalName)
	}
}
