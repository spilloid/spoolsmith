package install

import (
	"bytes"
	"context"
	"encoding/binary"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
)

const testWSDUUID = "urn:uuid:e3248000-80ce-11db-8000-30c9ab962e73"

func testIPPResponse(uri, uuid, model string) []byte {
	var response bytes.Buffer
	response.Write([]byte{1, 1, 0, 0, 0, 0, 0, 1, 4})
	ippWriteAttribute(&response, 0x45, "printer-uri-supported", uri)
	if uuid != "" {
		ippWriteAttribute(&response, 0x45, "printer-uuid", uuid)
	}
	ippWriteAttribute(&response, 0x41, "printer-make-and-model", model)
	response.WriteByte(3)
	return response.Bytes()
}

func testIPPServer(t *testing.T, handler func(http.ResponseWriter, *http.Request, string)) (ip, port string, closeServer func()) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handler(w, r, r.URL.Path)
	}))
	u, err := url.Parse(server.URL)
	if err != nil {
		server.Close()
		t.Fatal(err)
	}
	return u.Hostname(), u.Port(), server.Close
}

func TestIPPProbeBindsURLAndWSDDeviceID(t *testing.T) {
	var requests int
	ip, port, closeServer := testIPPServer(t, func(w http.ResponseWriter, r *http.Request, path string) {
		requests++
		if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/ipp" {
			t.Errorf("unexpected IPP request %s, %q", r.Method, r.Header.Get("Content-Type"))
		}
		if path != "/ipp/print" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/ipp")
		w.Write(testIPPResponse("ipp://"+r.Host+"/ipp/print", testWSDUUID, "Brother HL-L2315D series"))
	})
	defer closeServer()
	uri, model, err := discoverIPPEndpoint(context.Background(), ip, strings.ToUpper(testWSDUUID), port, newIPPProbeClient())
	if err != nil {
		t.Fatal(err)
	}
	if uri != "ipp://127.0.0.1:"+port+"/ipp/print" || model != "Brother HL-L2315D series" || requests != 1 {
		t.Fatalf("IPP discovery = %q, %q, requests=%d", uri, model, requests)
	}
}

func TestIPPProbeRejectsWrongDeviceAndRedirect(t *testing.T) {
	ip, port, closeServer := testIPPServer(t, func(w http.ResponseWriter, r *http.Request, path string) {
		w.Header().Set("Content-Type", "application/ipp")
		w.Write(testIPPResponse("ipp://"+r.Host+"/ipp/print", "urn:uuid:aaaaaaaa-aaaa-4aaa-aaaa-aaaaaaaaaaaa", "Other printer"))
	})
	defer closeServer()
	if _, _, err := discoverIPPEndpoint(context.Background(), ip, testWSDUUID, port, newIPPProbeClient()); err == nil {
		t.Fatal("accepted a printer whose UUID does not match the WSD port")
	}
	if uri, _, err := discoverIPPEndpoint(context.Background(), ip, "", port, newIPPProbeClient()); err != nil || uri == "" {
		t.Fatalf("ordinary IPP discovery should accept its own UUID: %q, %v", uri, err)
	}
	for _, candidate := range []string{"ipp://127.0.0.2:" + port + "/ipp/print", "ipp://user@127.0.0.1:" + port + "/ipp/print", "ipp://127.0.0.1:9/ipp/print", "https://127.0.0.1:" + port + "/ipp/print"} {
		if _, ok := verifiedIPPURI(candidate, net.ParseIP(ip), port); ok {
			t.Errorf("accepted redirect URI %q", candidate)
		}
	}
}

func TestIPPProbeVerifiesAdvertisedCanonicalPath(t *testing.T) {
	var canonicalRequests int
	ip, port, closeServer := testIPPServer(t, func(w http.ResponseWriter, r *http.Request, path string) {
		w.Header().Set("Content-Type", "application/ipp")
		if path == "/real-printer" {
			canonicalRequests++
		}
		w.Write(testIPPResponse("ipp://"+r.Host+"/real-printer", testWSDUUID, "Brother HL-L2315D series"))
	})
	defer closeServer()
	uri, _, err := discoverIPPEndpoint(context.Background(), ip, testWSDUUID, port, newIPPProbeClient())
	if err != nil || uri != "ipp://127.0.0.1:"+port+"/real-printer" || canonicalRequests != 1 {
		t.Fatalf("canonical endpoint = %q, requests=%d, %v", uri, canonicalRequests, err)
	}
}

func TestIPPProbeRejectsMalformedOrFailedResponse(t *testing.T) {
	good := testIPPResponse("ipp://127.0.0.1/ipp/print", testWSDUUID, "Brother")
	if _, err := parseIPPGetPrinterAttributes(good); err != nil {
		t.Fatal(err)
	}
	for _, payload := range [][]byte{good[:len(good)-2], append([]byte(nil), good[:8]...), append([]byte{1, 1, 4, 0}, good[4:]...)} {
		if _, err := parseIPPGetPrinterAttributes(payload); err == nil {
			t.Fatalf("accepted malformed IPP response %x", payload[:8])
		}
	}
	request := ippGetPrinterAttributesRequest("ipp://127.0.0.1/ipp/print")
	if binary.BigEndian.Uint16(request[2:4]) != 0x000b || !bytes.Contains(request, []byte("printer-uri")) {
		t.Fatalf("not a Get-Printer-Attributes request: %x", request[:8])
	}
}

func TestIPPProbeRejectsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := DiscoverIPPEndpoint(ctx, "127.0.0.1"); err != context.Canceled {
		t.Fatalf("canceled probe = %v", err)
	}
}

func TestHardwareIPPMatchesWSDDevice(t *testing.T) {
	if os.Getenv("SPOOLSMITH_TEST_WSD_HARDWARE") != "1" {
		t.Skip("set SPOOLSMITH_TEST_WSD_HARDWARE=1 on the mapped Brother test machine")
	}
	uri, model, err := DiscoverIPPEndpointForDevice(context.Background(), "192.168.68.108", testWSDUUID)
	if err != nil {
		t.Fatal(err)
	}
	if uri != "ipp://192.168.68.108/ipp/print" || model != "Brother HL-L2315D series" {
		t.Fatalf("Brother IPP endpoint = %q, model %q", uri, model)
	}
	t.Logf("WSD identity %s maps to IPP endpoint %s (%s)", testWSDUUID, uri, model)
}
