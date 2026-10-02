package install

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// IPP discovery is deliberately limited to requests that read printer
// attributes. A successful TCP connection alone says nothing about whether
// the queue's IPP driver can use the target or which printer answered.
const ippProbeResponseLimit = 256 * 1024

var ippProbePaths = []string{"/ipp/print", "/ipp/printer", "/ipp/port1", "/printers/print", "/ipp", "/"}

type ippProbeAttribute struct {
	tag   byte
	value []byte
}

type ippProbeResult struct {
	uri   string
	model string
	uuid  string
}

// DiscoverIPPEndpoint finds a verified IPP URL at a literal IP address.
// It confirms that the device itself advertises the returned URL, and that
// the URL still points at the same address and endpoint that answered.
func DiscoverIPPEndpoint(ctx context.Context, ip string) (uri, model string, err error) {
	return discoverIPPEndpoint(ctx, ip, "", "631", newIPPProbeClient())
}

// DiscoverIPPEndpointForDevice also requires the IPP printer UUID to match a
// WSDMON device ID. Both protocols publish this UUID for a WSD/IPP printer;
// matching it prevents an old WSD address from silently selecting a different
// printer after DHCP reassigns the address.
func DiscoverIPPEndpointForDevice(ctx context.Context, ip, expectedUUID string) (uri, model string, err error) {
	return discoverIPPEndpoint(ctx, ip, expectedUUID, "631", newIPPProbeClient())
}

func newIPPProbeClient() *http.Client {
	transport := &http.Transport{
		Proxy:                 nil,
		DialContext:           (&net.Dialer{Timeout: 2 * time.Second}).DialContext,
		DisableKeepAlives:     true,
		ResponseHeaderTimeout: 3 * time.Second,
	}
	return &http.Client{
		Transport: transport,
		Timeout:   4 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func discoverIPPEndpoint(ctx context.Context, address, expectedUUID, port string, client *http.Client) (string, string, error) {
	ip := net.ParseIP(strings.TrimSpace(address))
	if ip == nil || strings.Contains(address, "%") {
		return "", "", fmt.Errorf("IPP discovery requires a literal IP address, got %q", address)
	}
	if client == nil {
		return "", "", errors.New("IPP discovery has no HTTP client")
	}
	if !validIPPProbePort(port) {
		return "", "", fmt.Errorf("invalid IPP probe port %q", port)
	}
	if expectedUUID != "" {
		var err error
		expectedUUID, err = normalizeWSDDeviceID(expectedUUID)
		if err != nil {
			return "", "", err
		}
	}
	for _, path := range ippProbePaths {
		if err := ctx.Err(); err != nil {
			return "", "", err
		}
		result, err := probeIPPEndpoint(ctx, client, ip, port, path)
		if err != nil || (expectedUUID != "" && result.uuid != expectedUUID) {
			continue
		}
		// A device may answer at an alias and advertise a canonical resource.
		// Query the canonical resource as well before recording it in a bundle.
		canonical, _ := url.Parse(result.uri)
		if canonical.EscapedPath() != path {
			verified, err := probeIPPEndpoint(ctx, client, ip, port, canonical.EscapedPath())
			if err != nil || verified.uri != result.uri || (expectedUUID != "" && verified.uuid != expectedUUID) {
				continue
			}
			result = verified
		}
		return result.uri, result.model, nil
	}
	if err := ctx.Err(); err != nil {
		return "", "", err
	}
	if expectedUUID != "" {
		return "", "", fmt.Errorf("printer at %s did not advertise a verified IPP endpoint with WSD device ID %s", ip, expectedUUID)
	}
	return "", "", fmt.Errorf("printer at %s did not advertise a verified IPP endpoint", ip)
}

func probeIPPEndpoint(ctx context.Context, client *http.Client, ip net.IP, port, path string) (ippProbeResult, error) {
	requestURI := "ipp://" + net.JoinHostPort(ip.String(), port) + path
	body := ippGetPrinterAttributesRequest(requestURI)
	endpoint := "http://" + net.JoinHostPort(ip.String(), port) + path
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return ippProbeResult{}, err
	}
	req.Header.Set("Content-Type", "application/ipp")
	response, err := client.Do(req)
	if err != nil {
		return ippProbeResult{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return ippProbeResult{}, fmt.Errorf("IPP HTTP status %d", response.StatusCode)
	}
	mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/ipp" {
		return ippProbeResult{}, errors.New("IPP response has the wrong content type")
	}
	payload, err := io.ReadAll(io.LimitReader(response.Body, ippProbeResponseLimit+1))
	if err != nil || len(payload) > ippProbeResponseLimit {
		return ippProbeResult{}, errors.New("IPP response was unreadable or too large")
	}
	attributes, err := parseIPPGetPrinterAttributes(payload)
	if err != nil {
		return ippProbeResult{}, err
	}
	result := ippProbeResult{}
	for _, candidate := range attributes["printer-uri-supported"] {
		if candidate.tag != 0x45 {
			continue
		}
		if uri, ok := verifiedIPPURI(string(candidate.value), ip, port); ok {
			result.uri = uri
			break
		}
	}
	if result.uri == "" {
		return ippProbeResult{}, errors.New("IPP printer did not advertise a URI at this IP address and port")
	}
	for _, candidate := range attributes["printer-uuid"] {
		if candidate.tag != 0x45 {
			continue
		}
		id, err := normalizeWSDDeviceID(string(candidate.value))
		if err != nil {
			continue
		}
		if result.uuid != "" && result.uuid != id {
			return ippProbeResult{}, errors.New("IPP printer advertised conflicting UUIDs")
		}
		result.uuid = id
	}
	for _, candidate := range attributes["printer-make-and-model"] {
		if candidate.tag == 0x41 && utf8.Valid(candidate.value) {
			result.model = strings.TrimSpace(string(candidate.value))
			break
		}
	}
	return result, nil
}

func verifiedIPPURI(raw string, ip net.IP, port string) (string, bool) {
	uri, err := url.Parse(raw)
	if err != nil || uri.Scheme != "ipp" || uri.Opaque != "" || uri.User != nil || uri.RawQuery != "" || uri.Fragment != "" {
		return "", false
	}
	address := net.ParseIP(uri.Hostname())
	if address == nil || !address.Equal(ip) {
		return "", false
	}
	uriPort := uri.Port()
	if uriPort == "" {
		uriPort = "631"
	}
	if uriPort != port || uri.EscapedPath() == "" || !strings.HasPrefix(uri.EscapedPath(), "/") {
		return "", false
	}
	if strings.Contains(uri.EscapedPath(), "\\") || strings.Contains(uri.EscapedPath(), "..") {
		return "", false
	}
	return uri.String(), true
}

func ippGetPrinterAttributesRequest(uri string) []byte {
	var request bytes.Buffer
	request.Write([]byte{1, 1, 0, 0x0b, 0, 0, 0, 1, 1}) // IPP 1.1, Get-Printer-Attributes, request 1, operation group.
	ippWriteAttribute(&request, 0x47, "attributes-charset", "utf-8")
	ippWriteAttribute(&request, 0x48, "attributes-natural-language", "en")
	ippWriteAttribute(&request, 0x45, "printer-uri", uri)
	for index, name := range []string{"printer-uri-supported", "printer-uuid", "printer-make-and-model"} {
		attributeName := ""
		if index == 0 {
			attributeName = "requested-attributes"
		}
		ippWriteAttribute(&request, 0x44, attributeName, name)
	}
	request.WriteByte(3)
	return request.Bytes()
}

func ippWriteAttribute(w io.Writer, tag byte, name, value string) {
	w.Write([]byte{tag})
	_ = binary.Write(w, binary.BigEndian, uint16(len(name)))
	w.Write([]byte(name))
	_ = binary.Write(w, binary.BigEndian, uint16(len(value)))
	w.Write([]byte(value))
}

func parseIPPGetPrinterAttributes(payload []byte) (map[string][]ippProbeAttribute, error) {
	if len(payload) < 9 || (payload[0] != 1 && payload[0] != 2) || binary.BigEndian.Uint16(payload[2:4]) >= 0x0100 || binary.BigEndian.Uint32(payload[4:8]) != 1 {
		return nil, errors.New("IPP printer-attributes response has an invalid header or failure status")
	}
	attributes := make(map[string][]ippProbeAttribute)
	group := byte(0)
	lastName := ""
	for cursor := 8; cursor < len(payload); {
		tag := payload[cursor]
		cursor++
		if tag == 3 {
			return attributes, nil
		}
		if tag <= 0x0f {
			group, lastName = tag, ""
			continue
		}
		if cursor+2 > len(payload) {
			break
		}
		nameLength := int(binary.BigEndian.Uint16(payload[cursor : cursor+2]))
		cursor += 2
		if cursor+nameLength+2 > len(payload) {
			break
		}
		name := string(payload[cursor : cursor+nameLength])
		cursor += nameLength
		valueLength := int(binary.BigEndian.Uint16(payload[cursor : cursor+2]))
		cursor += 2
		if cursor+valueLength > len(payload) {
			break
		}
		value := payload[cursor : cursor+valueLength]
		cursor += valueLength
		if name != "" {
			lastName = name
		} else {
			name = lastName
		}
		if group == 4 && name != "" {
			attributes[name] = append(attributes[name], ippProbeAttribute{tag: tag, value: value})
		}
	}
	return nil, errors.New("IPP printer-attributes response is truncated")
}

// Keep the port numeric before it is used in URLs. Production uses 631; the
// injectable port in tests exercises the same request/response path.
func validIPPProbePort(port string) bool {
	n, err := strconv.Atoi(port)
	return err == nil && n >= 1 && n <= 65535
}
