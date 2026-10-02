//go:build windows

package install

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	wsdSpoolDLL     = windows.NewLazySystemDLL("winspool.drv")
	wsdOpenPrinter  = wsdSpoolDLL.NewProc("OpenPrinterW")
	wsdClosePrinter = wsdSpoolDLL.NewProc("ClosePrinter")
	wsdXcvData      = wsdSpoolDLL.NewProc("XcvDataW")
)

type wsdPrinterDefaults struct {
	Datatype      *uint16
	DevMode       uintptr
	DesiredAccess uint32
}

// queryWSDPortString asks the installed Windows WSD monitor for the identity
// bound to this exact port. DeviceID and ServiceID are read-only Xcv commands.
func queryWSDPortString(portName, command string) (string, error) {
	if err := validatePlanValue("WSD port name", portName); err != nil {
		return "", err
	}
	if strings.TrimSpace(portName) == "" {
		return "", errors.New("WSD port name is empty")
	}
	name, err := windows.UTF16PtrFromString(",XcvPort " + portName)
	if err != nil {
		return "", err
	}
	operation, err := windows.UTF16PtrFromString(command)
	if err != nil {
		return "", err
	}
	var handle uintptr
	defaults := wsdPrinterDefaults{DesiredAccess: 0x00000001} // SERVER_ACCESS_ADMINISTER, required by WSDMON XcvData.
	ok, _, callErr := wsdOpenPrinter.Call(uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(&handle)), uintptr(unsafe.Pointer(&defaults)))
	if ok == 0 {
		return "", fmt.Errorf("open WSD printer port %q: %w", portName, callErr)
	}
	defer wsdClosePrinter.Call(handle)
	buffer := make([]uint16, 2048)
	var needed, status uint32
	ok, _, callErr = wsdXcvData.Call(handle, uintptr(unsafe.Pointer(operation)), 0, 0, uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)*2), uintptr(unsafe.Pointer(&needed)), uintptr(unsafe.Pointer(&status)))
	if ok == 0 {
		return "", fmt.Errorf("WSD port %q %s query failed: %w", portName, command, callErr)
	}
	if status != 0 {
		return "", fmt.Errorf("WSD port %q %s query returned Windows error %d", portName, command, status)
	}
	if needed > uint32(len(buffer)*2) {
		return "", fmt.Errorf("WSD port %q %s response exceeded buffer", portName, command)
	}
	value := strings.TrimSpace(string(utf16.Decode(buffer)))
	value = strings.TrimRight(value, "\x00")
	if value == "" {
		return "", fmt.Errorf("WSD port %q returned an empty %s", portName, command)
	}
	return value, nil
}

// ResolveWSDPort converts an installed WSD port into an IP print endpoint.
// Prefer an existing RAW mapping for this verified IP when a WSD queue uses
// the IPP class driver. IPP is the fallback when no unambiguous RAW driver is known.
func (windowsEnvironment) ResolveWSDPort(ctx context.Context, portName, driverName string) (WSDResolution, error) {
	deviceValue, err := queryWSDPortString(portName, "DeviceID")
	if err != nil {
		return WSDResolution{}, err
	}
	deviceID, err := normalizeWSDDeviceID(deviceValue)
	if err != nil {
		return WSDResolution{}, err
	}
	serviceID, _ := queryWSDPortString(portName, "ServiceID")
	ip, err := installedWSDDeviceAddress(ctx, deviceID)
	if err != nil {
		var discoveryErr error
		ip, discoveryErr = discoverWSDAddress(ctx, deviceID)
		if discoveryErr != nil {
			return WSDResolution{}, fmt.Errorf("Windows device address: %v; live WSD discovery: %w", err, discoveryErr)
		}
	}
	resolution := WSDResolution{IP: ip.String(), DeviceID: deviceID, ServiceID: serviceID, DriverName: driverName}
	if isIPPClassDriver(driverName) {
		queues, err := ListPrinters(ctx, windowsEnvironment{})
		if err != nil {
			return WSDResolution{}, fmt.Errorf("inspect existing RAW printer mappings: %w", err)
		}
		if driver, source := selectWSDRAWDriver(queues, ip); driver != "" {
			if err := verifyWSDRaw9100(ctx, ip); err == nil {
				resolution.DriverName, resolution.DriverSourceQueue = driver, source
				return resolution, nil
			} else if ctx.Err() != nil {
				return WSDResolution{}, ctx.Err()
			}
		}
		url, _, err := DiscoverIPPEndpointForDevice(ctx, ip.String(), deviceID)
		if err != nil {
			return WSDResolution{}, fmt.Errorf("verify IPP printer for WSD device %s: %w", deviceID, err)
		}
		resolution.IPPURL = url
	} else if err := verifyWSDRaw9100(ctx, ip); err != nil {
		return WSDResolution{}, err
	}
	return resolution, nil
}

// installedWSDDeviceAddress correlates the monitor's DeviceID with the exact
// present PnP-X root device, then reads Windows' WSD provider location. The
// location must answer live with the same device identity before it is used.
func installedWSDDeviceAddress(ctx context.Context, deviceID string) (net.IP, error) {
	instance := `SWD\DAFWSDPROVIDER\` + strings.ToUpper(deviceID)
	command := `$id = ` + powerShellString(instance) + `; $devices = @(Get-PnpDevice -PresentOnly -ErrorAction Stop | Where-Object { $_.InstanceId -ieq $id }); if ($devices.Count -ne 1 -or $devices[0].Status -ne 'OK') { throw 'Matching WSD PnP device is not present and healthy' }; $properties = @(Get-PnpDeviceProperty -InstanceId $devices[0].InstanceId -KeyName 'DEVPKEY_Device_LocationInfo' -ErrorAction Stop); if ($properties.Count -ne 1) { throw 'Matching WSD device has no unique location' }; [PSCustomObject]@{instance_id=[string]$devices[0].InstanceId;location=[string]$properties[0].Data} | ConvertTo-Json -Compress`
	output, err := runPowerShell(ctx, powerShellCommand(command))
	if err != nil {
		return nil, fmt.Errorf("read WSD PnP device: %w: %s", err, strings.TrimSpace(output))
	}
	var found struct {
		InstanceID string `json:"instance_id"`
		Location   string `json:"location"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(output)), &found); err != nil {
		return nil, fmt.Errorf("decode WSD PnP location: %w", err)
	}
	if !strings.EqualFold(found.InstanceID, instance) {
		return nil, errors.New("WSD PnP identity differs from the printer port")
	}
	location, err := url.Parse(found.Location)
	if err != nil || (location.Scheme != "http" && location.Scheme != "https") || location.Hostname() == "" || location.User != nil {
		return nil, fmt.Errorf("WSD PnP location %q is not an HTTP device address", found.Location)
	}
	if ip := net.ParseIP(location.Hostname()); ip != nil {
		if err := verifyWSDMetadata(ctx, found.Location, deviceID); err != nil {
			return nil, fmt.Errorf("verify WSD device at Windows location: %w", err)
		}
		return ip, nil
	}
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, location.Hostname())
	if err != nil {
		return nil, fmt.Errorf("resolve WSD PnP hostname %q: %w", location.Hostname(), err)
	}
	var verified net.IP
	for _, address := range addresses {
		if address.Zone != "" || address.IP.To16() == nil {
			continue
		}
		candidate := *location
		if location.Port() == "" {
			candidate.Host = address.IP.String()
			if address.IP.To4() == nil {
				candidate.Host = "[" + address.IP.String() + "]"
			}
		} else {
			candidate.Host = net.JoinHostPort(address.IP.String(), location.Port())
		}
		if verifyWSDMetadata(ctx, candidate.String(), deviceID) != nil {
			continue
		}
		if verified != nil && !verified.Equal(address.IP) {
			return nil, errors.New("WSD PnP hostname identifies more than one printer address")
		}
		verified = address.IP
	}
	if verified == nil {
		return nil, errors.New("WSD PnP hostname resolved to no address with matching live device metadata")
	}
	return verified, nil
}
