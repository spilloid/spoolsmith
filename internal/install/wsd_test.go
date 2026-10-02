package install

import (
	"context"
	"net"
	"strings"
	"testing"
)

func TestWSDIdentityNormalization(t *testing.T) {
	for _, input := range []string{"urn:uuid:37F86D35-E6AC-4241-964F-1D9AE46FB366", "{37f86d35-e6ac-4241-964f-1d9ae46fb366}", "37f86d35-e6ac-4241-964f-1d9ae46fb366"} {
		got, err := normalizeWSDDeviceID(input)
		if err != nil || got != "urn:uuid:37f86d35-e6ac-4241-964f-1d9ae46fb366" {
			t.Fatalf("normalizeWSDDeviceID(%q) = %q, %v", input, got, err)
		}
	}
	if _, err := normalizeWSDDeviceID("not-a-device"); err == nil {
		t.Fatal("accepted invalid WSD device ID")
	}
}

func TestWSDResolveResponseMustMatchDeviceRequestAndSender(t *testing.T) {
	id := "urn:uuid:37f86d35-e6ac-4241-964f-1d9ae46fb366"
	message := "urn:uuid:38d1c3d9-8d73-4424-8861-6b7ee2af24d3"
	response := `<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope" xmlns:a="http://schemas.xmlsoap.org/ws/2004/08/addressing" xmlns:d="http://schemas.xmlsoap.org/ws/2005/04/discovery"><s:Header><a:RelatesTo>` + message + `</a:RelatesTo></s:Header><s:Body><d:ResolveMatches><d:ResolveMatch><a:EndpointReference><a:Address>` + id + `</a:Address></a:EndpointReference><d:XAddrs>http://192.0.2.10:5357/print</d:XAddrs></d:ResolveMatch></d:ResolveMatches></s:Body></s:Envelope>`
	if ip, ok := wsdResponseAddress(context.Background(), []byte(response), net.ParseIP("192.0.2.10"), id, message); !ok || !ip.Equal(net.ParseIP("192.0.2.10")) {
		t.Fatalf("matching ResolveMatches = %s, %t", ip, ok)
	}
	for _, change := range []struct{ name, response, sender, deviceID, messageID string }{
		{"wrong device", strings.Replace(response, id, "urn:uuid:aaaaaaaa-aaaa-4aaa-aaaa-aaaaaaaaaaaa", 1), "192.0.2.10", id, message},
		{"wrong message", response, "192.0.2.10", id, "urn:uuid:bbbbbbbb-bbbb-4bbb-bbbb-bbbbbbbbbbbb"},
		{"redirected address", response, "192.0.2.11", id, message},
	} {
		t.Run(change.name, func(t *testing.T) {
			if _, ok := wsdResponseAddress(context.Background(), []byte(change.response), net.ParseIP(change.sender), change.deviceID, change.messageID); ok {
				t.Fatal("accepted unrelated WSD response")
			}
		})
	}
}

func TestCloneWSDQueueAsIPPrinter(t *testing.T) {
	env := cloneEnv(PortConfiguration{PortName: "WSD-abc", Monitor: "WSD Port"})
	env.configuration.PortName = "WSD-abc"
	env.wsd = WSDResolution{IP: "192.0.2.10", DeviceID: "urn:uuid:37f86d35-e6ac-4241-964f-1d9ae46fb366"}
	cloned, err := CloneQueue(context.Background(), env, "Test Printer")
	if err != nil {
		t.Fatal(err)
	}
	if cloned.HostAddress != "192.0.2.10" || cloned.SourceWSDPort != "WSD-abc" || cloned.USB {
		t.Fatalf("WSD clone = %+v", cloned)
	}
	env.wsd.IP = "invalid"
	if _, err := CloneQueue(context.Background(), env, "Test Printer"); err == nil {
		t.Fatal("accepted WSD resolution without an IP")
	}
	env.wsd.IP = "192.0.2.10"
	env.configuration.DriverName = "Microsoft IPP Class Driver"
	if _, err := CloneQueue(context.Background(), env, "Test Printer"); err == nil {
		t.Fatal("accepted IPP class driver without a verified IPP endpoint")
	}
	env.wsd.IPPURL = "ipp://192.0.2.10/ipp/print"
	cloned, err = CloneQueue(context.Background(), env, "Test Printer")
	if err != nil || cloned.IPPURL != env.wsd.IPPURL {
		t.Fatalf("IPP WSD clone = %+v, %v", cloned, err)
	}
	env.wsd.IPPURL = ""
	env.wsd.DriverName = "Vendor OEM Driver"
	env.wsd.DriverSourceQueue = "Known RAW mapping"
	cloned, err = CloneQueue(context.Background(), env, "Test Printer")
	if err != nil || cloned.DriverName != "Vendor OEM Driver" || cloned.SourceDriverName != "Microsoft IPP Class Driver" || cloned.SourceDriverQueue != "Known RAW mapping" || cloned.IPPURL != "" {
		t.Fatalf("preferred RAW WSD clone = %+v, %v", cloned, err)
	}
}

func TestWSDMonitorClassification(t *testing.T) {
	for _, port := range []PortConfiguration{{PortName: "WSD-abc"}, {PortName: "custom", Monitor: "WSD Port"}} {
		if !isWSDConfiguration(port) || copyBlockedReason(port) != "" {
			t.Fatalf("WSD port %v not copyable", port)
		}
	}
	port := PortConfiguration{PortName: "WSD-abc", Monitor: "Standard TCP/IP Port", HostAddress: "192.0.2.10", PortNumber: 515, Protocol: 2}
	if isWSDConfiguration(port) || copyBlockedReason(port) == "" {
		t.Fatalf("non-WSD monitor must remain authoritative: %+v", port)
	}
}
