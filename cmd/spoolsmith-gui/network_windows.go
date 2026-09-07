//go:build windows

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// Read adapter types and physical/virtual flags from Windows, never from the
// user-editable connection name. ActiveStore excludes merely persisted routes.
// The query is read-only and emits one JSON array even for zero/one adapter.
const currentNetworksCommand = `
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
[Console]::OutputEncoding = [System.Text.UTF8Encoding]::new($false)
$addresses = @(Get-NetIPAddress -AddressFamily IPv4 -PolicyStore ActiveStore)
$routes = @(Get-NetRoute -AddressFamily IPv4 -PolicyStore ActiveStore | Where-Object { $_.DestinationPrefix -eq '0.0.0.0/0' -and $_.State -ne 'Dead' })
$networks = @(Get-NetAdapter -Physical | ForEach-Object {
    $adapter = $_
    $defaultRoutes = @($routes | Where-Object { $_.InterfaceIndex -eq $adapter.ifIndex })
    $metric = $null
    if ($defaultRoutes.Count -gt 0) {
        $metric = [long]($defaultRoutes | ForEach-Object { [long]$_.RouteMetric + [long]$_.InterfaceMetric } | Measure-Object -Minimum).Minimum
    }
    [pscustomobject]@{
        index = [int]$adapter.ifIndex
        name = [string]$adapter.Name
        interface_type = [int]$adapter.InterfaceType
        physical_medium = [int]$adapter.NdisPhysicalMedium
        hardware = [bool]$adapter.HardwareInterface
        virtual = [bool]$adapter.Virtual
        connected = ($adapter.Status -eq 'Up' -and $adapter.MediaConnectionState -eq 'Connected')
        default_metric = $metric
        addresses = @($addresses | Where-Object { $_.InterfaceIndex -eq $adapter.ifIndex } | ForEach-Object {
            [pscustomobject]@{
                address = [string]$_.IPAddress
                prefix_length = [int]$_.PrefixLength
                preferred = ($_.AddressState -eq 'Preferred')
                skip_as_source = [bool]$_.SkipAsSource
            }
        })
    }
})
ConvertTo-Json -InputObject $networks -Depth 4 -Compress
`

type localNetwork struct {
	Index          int            `json:"index"`
	Name           string         `json:"name"`
	InterfaceType  int            `json:"interface_type"`
	PhysicalMedium int            `json:"physical_medium"`
	Hardware       bool           `json:"hardware"`
	Virtual        bool           `json:"virtual"`
	Connected      bool           `json:"connected"`
	DefaultMetric  *int64         `json:"default_metric"`
	Addresses      []localAddress `json:"addresses"`
}

type localAddress struct {
	Address      string `json:"address"`
	PrefixLength int    `json:"prefix_length"`
	Preferred    bool   `json:"preferred"`
	SkipAsSource bool   `json:"skip_as_source"`
}

// recommendedNetwork is called off the UI thread. It only identifies a subnet;
// the caller decides whether to begin discovery or preserve an entered override.
func recommendedNetwork() (cidr string, description string, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "powershell.exe", "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", currentNetworksCommand)
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	output, runErr := command.Output()
	if ctx.Err() != nil {
		return "", "", fmt.Errorf("network detection timed out; enter a subnet or printer IP to continue")
	}
	if runErr != nil {
		return "", "", fmt.Errorf("could not read the current Windows network; enter a subnet or printer IP to continue")
	}
	var networks []localNetwork
	if err := json.Unmarshal(output, &networks); err != nil {
		return "", "", fmt.Errorf("could not read the current Windows network; enter a subnet or printer IP to continue")
	}
	return selectRecommendedNetwork(networks)
}

// selectRecommendedNetwork prefers connected physical Wi-Fi, including an
// isolated Wi-Fi network without internet access. Default routes and their total
// metrics disambiguate multiple adapters of that type. Ethernet is a fallback
// only when it has a default route. Equally ranked adapters or multiple distinct
// address subnets are left for the user to choose instead of guessing.
func selectRecommendedNetwork(networks []localNetwork) (string, string, error) {
	type candidate struct {
		network localNetwork
		kind    string
		subnets map[string]bool // value records whether a large network was limited
	}
	var candidates []candidate
	for _, network := range networks {
		if !network.Hardware || network.Virtual || !network.Connected {
			continue
		}
		kind := ""
		switch {
		case network.InterfaceType == 71 || network.PhysicalMedium == 1 || network.PhysicalMedium == 9:
			kind = "Wi-Fi"
		case network.InterfaceType == 6 && (network.PhysicalMedium == 0 || network.PhysicalMedium == 14) && network.DefaultMetric != nil:
			kind = "Ethernet"
		default:
			continue
		}
		subnets := make(map[string]bool)
		for _, address := range network.Addresses {
			if !address.Preferred || address.SkipAsSource {
				continue
			}
			cidr, limited, ok := discoverySubnet(address.Address, address.PrefixLength)
			if ok {
				subnets[cidr] = subnets[cidr] || limited
			}
		}
		if len(subnets) != 0 {
			candidates = append(candidates, candidate{network, kind, subnets})
		}
	}
	if len(candidates) == 0 {
		return "", "", fmt.Errorf("no connected Wi-Fi or primary Ethernet network found; enter a subnet or printer IP to continue")
	}
	// Negative means left is preferred; zero is deliberately ambiguous.
	compare := func(left, right candidate) int {
		if left.kind != right.kind {
			if left.kind == "Wi-Fi" {
				return -1
			}
			return 1
		}
		lm, rm := left.network.DefaultMetric, right.network.DefaultMetric
		if lm == nil && rm != nil {
			return 1
		}
		if lm != nil && rm == nil {
			return -1
		}
		if lm != nil && rm != nil {
			if *lm < *rm {
				return -1
			}
			if *lm > *rm {
				return 1
			}
		}
		return 0
	}
	best := candidates[0]
	ambiguous := false
	for _, next := range candidates[1:] {
		switch compare(next, best) {
		case -1:
			best, ambiguous = next, false
		case 0:
			ambiguous = true
		}
	}
	if ambiguous || len(best.subnets) != 1 {
		return "", "", fmt.Errorf("more than one current network is available; enter the subnet you want to scan")
	}
	for cidr, limited := range best.subnets {
		description := best.kind
		if label := strings.TrimSpace(best.network.Name); label != "" && !strings.EqualFold(label, description) {
			description += " (" + label + ")"
		}
		description += " · " + cidr
		if limited {
			description += " · limited to 256 addresses"
		}
		return cidr, description, nil
	}
	panic("selected network has no subnet")
}

func discoverySubnet(address string, prefixLength int) (cidr string, limited bool, ok bool) {
	ip, err := netip.ParseAddr(address)
	if err != nil || !ip.Is4() || !ip.IsGlobalUnicast() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || prefixLength < 0 || prefixLength > 32 {
		return "", false, false
	}
	if prefixLength < 24 {
		prefixLength, limited = 24, true
	}
	return netip.PrefixFrom(ip, prefixLength).Masked().String(), limited, true
}
