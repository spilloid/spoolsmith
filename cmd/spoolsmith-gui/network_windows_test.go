//go:build windows

package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDiscoverySubnet(t *testing.T) {
	tests := []struct {
		name    string
		address string
		prefix  int
		want    string
		limited bool
	}{
		{"large network uses local slice", "10.18.42.193", 16, "10.18.42.0/24", true},
		{"standard subnet", "192.168.5.17", 24, "192.168.5.0/24", false},
		{"small subnet preserved", "192.168.5.193", 25, "192.168.5.128/25", false},
		{"single host preserved", "192.168.5.193", 32, "192.168.5.193/32", false},
		{"loopback excluded", "127.0.0.1", 8, "", false},
		{"link local excluded", "169.254.4.10", 16, "", false},
		{"unspecified excluded", "0.0.0.0", 0, "", false},
		{"multicast excluded", "224.0.0.5", 24, "", false},
		{"broadcast excluded", "255.255.255.255", 24, "", false},
		{"IPv6 excluded", "2001:db8::1", 64, "", false},
		{"invalid prefix excluded", "192.168.5.193", 33, "", false},
		{"negative prefix excluded", "192.168.5.193", -1, "", false},
		{"malformed excluded", "printer.local", 24, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cidr, limited, ok := discoverySubnet(tt.address, tt.prefix)
			if cidr != tt.want || limited != tt.limited || ok != (tt.want != "") {
				t.Fatalf("got (%q, %v, %v), want (%q, %v, %v)", cidr, limited, ok, tt.want, tt.limited, tt.want != "")
			}
		})
	}
}

func TestSelectRecommendedNetwork(t *testing.T) {
	adapter := func(name string, kind int, address string, metric int64) localNetwork {
		return localNetwork{
			Name: name, InterfaceType: kind, Hardware: true, Connected: true, DefaultMetric: &metric,
			Addresses: []localAddress{{Address: address, PrefixLength: 24, Preferred: true}},
		}
	}
	wifi := adapter("Renamed connection", 71, "192.168.4.23", 50)
	ethernet := adapter("Wi-Fi", 6, "10.0.5.10", 10)
	virtual := adapter("Wi-Fi VPN", 71, "10.44.0.2", 0)
	virtual.Virtual = true
	vpn := adapter("Ethernet VPN", 6, "10.99.0.2", 0)
	vpn.Hardware = false
	disconnected := wifi
	disconnected.Connected = false
	isolatedWifi := wifi
	isolatedWifi.DefaultMetric = nil
	secondaryWifi := adapter("Second wireless adapter", 71, "192.168.8.23", 70)
	tiedWifi := secondaryWifi
	tiedWifi.DefaultMetric = wifi.DefaultMetric
	secondaryEthernet := adapter("USB Ethernet", 6, "10.0.8.10", 30)
	noDefaultEthernet := ethernet
	noDefaultEthernet.DefaultMetric = nil
	multipleAddresses := wifi
	multipleAddresses.Addresses = append([]localAddress{}, wifi.Addresses...)
	multipleAddresses.Addresses = append(multipleAddresses.Addresses, localAddress{Address: "192.168.8.24", PrefixLength: 24, Preferred: true})
	sameSubnetAddresses := wifi
	sameSubnetAddresses.Addresses = append([]localAddress{}, wifi.Addresses...)
	sameSubnetAddresses.Addresses = append(sameSubnetAddresses.Addresses, localAddress{Address: "192.168.4.24", PrefixLength: 24, Preferred: true})
	secondarySource := multipleAddresses
	secondarySource.Addresses = append([]localAddress{}, multipleAddresses.Addresses...)
	secondarySource.Addresses[1].SkipAsSource = true
	tentative := wifi
	tentative.Addresses = []localAddress{{Address: "192.168.4.23", PrefixLength: 24}}
	linkLocal := adapter("Wireless", 71, "169.254.4.23", 50)
	legacyWifi := adapter("Legacy wireless", 6, "192.168.4.23", 50)
	legacyWifi.PhysicalMedium = 9
	legacyWifi.DefaultMetric = nil
	bluetooth := adapter("Bluetooth", 6, "192.168.9.1", 0)
	bluetooth.PhysicalMedium = 10
	largeNetwork := wifi
	largeNetwork.Addresses = []localAddress{{Address: "10.4.72.13", PrefixLength: 16, Preferred: true}}
	tests := []struct {
		name     string
		networks []localNetwork
		wantCIDR string
		wantText string
	}{
		{"prefer actual Wi-Fi over renamed Ethernet", []localNetwork{ethernet, wifi}, "192.168.4.0/24", "Wi-Fi (Renamed connection)"},
		{"exclude VPN and virtual adapters", []localNetwork{virtual, vpn, ethernet}, "10.0.5.0/24", "Ethernet"},
		{"ignore disconnected Wi-Fi", []localNetwork{disconnected, ethernet}, "10.0.5.0/24", "Ethernet"},
		{"isolated Wi-Fi remains usable", []localNetwork{ethernet, isolatedWifi}, "192.168.4.0/24", "Wi-Fi"},
		{"prefer wireless default route", []localNetwork{isolatedWifi, secondaryWifi}, "192.168.8.0/24", "Wi-Fi"},
		{"prefer lower wireless route metric", []localNetwork{secondaryWifi, wifi}, "192.168.4.0/24", "Wi-Fi"},
		{"prefer primary Ethernet", []localNetwork{secondaryEthernet, ethernet}, "10.0.5.0/24", "Ethernet"},
		{"ambiguous wireless interfaces", []localNetwork{wifi, tiedWifi}, "", "more than one"},
		{"ambiguous address subnets", []localNetwork{multipleAddresses}, "", "more than one"},
		{"multiple addresses on same subnet", []localNetwork{sameSubnetAddresses}, "192.168.4.0/24", "Wi-Fi"},
		{"skip alternate source address", []localNetwork{secondarySource}, "192.168.4.0/24", "Wi-Fi"},
		{"ignore tentative address", []localNetwork{tentative}, "", "no connected"},
		{"ignore link local", []localNetwork{linkLocal}, "", "no connected"},
		{"ignore nonprimary Ethernet", []localNetwork{noDefaultEthernet}, "", "no connected"},
		{"recognize NDIS wireless medium", []localNetwork{legacyWifi}, "192.168.4.0/24", "Wi-Fi"},
		{"exclude Bluetooth PAN", []localNetwork{bluetooth}, "", "no connected"},
		{"explain limited network", []localNetwork{largeNetwork}, "10.4.72.0/24", "limited to 256 addresses"},
		{"empty result", nil, "", "no connected"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cidr, description, err := selectRecommendedNetwork(tt.networks)
			if cidr != tt.wantCIDR {
				t.Fatalf("got subnet %q, want %q (description %q, error %v)", cidr, tt.wantCIDR, description, err)
			}
			if tt.wantCIDR == "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantText) {
					t.Fatalf("got error %v, want containing %q", err, tt.wantText)
				}
			} else if err != nil || !strings.Contains(description, tt.wantText) {
				t.Fatalf("got description %q, error %v; want containing %q", description, err, tt.wantText)
			}
		})
	}
}

func TestWindowsNetworkJSON(t *testing.T) {
	// Shape emitted by ConvertTo-Json, including an isolated network's null
	// route metric and arrays for a single adapter/address.
	const input = `[{"index":12,"name":"Wireless","interface_type":71,"physical_medium":9,"hardware":true,"virtual":false,"connected":true,"default_metric":null,"addresses":[{"address":"192.168.4.23","prefix_length":25,"preferred":true,"skip_as_source":false}]}]`
	var networks []localNetwork
	if err := json.Unmarshal([]byte(input), &networks); err != nil {
		t.Fatal(err)
	}
	cidr, _, err := selectRecommendedNetwork(networks)
	if err != nil || cidr != "192.168.4.0/25" {
		t.Fatalf("got subnet %q, error %v", cidr, err)
	}
}
