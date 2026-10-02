package install

import (
	"context"
	"crypto/rand"
	"encoding/xml"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"
)

const wsdMulticast = "239.255.255.250:3702"

// normalizeWSDDeviceID accepts Windows' UUID spellings but never an arbitrary
// network address. The ID is taken from WSDMON, not from a queue name.
func normalizeWSDDeviceID(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.TrimPrefix(value, "urn:uuid:")
	value = strings.TrimPrefix(value, "uuid:")
	value = strings.Trim(value, "{}")
	if len(value) != 36 {
		return "", fmt.Errorf("WSD device ID %q is not a UUID", value)
	}
	for i, r := range value {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if r != '-' {
				return "", fmt.Errorf("WSD device ID %q is not a UUID", value)
			}
		} else if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
			return "", fmt.Errorf("WSD device ID %q is not a UUID", value)
		}
	}
	return "urn:uuid:" + value, nil
}

func wsdMessageID() (string, error) {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	bytes[6] = bytes[6]&0x0f | 0x40
	bytes[8] = bytes[8]&0x3f | 0x80
	return fmt.Sprintf("urn:uuid:%x-%x-%x-%x-%x", bytes[:4], bytes[4:6], bytes[6:8], bytes[8:10], bytes[10:]), nil
}

func wsdResolveRequest(deviceID, messageID string) []byte {
	return []byte(fmt.Sprintf(`<?xml version="1.0" encoding="utf-8"?><s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope" xmlns:a="http://schemas.xmlsoap.org/ws/2004/08/addressing" xmlns:d="http://schemas.xmlsoap.org/ws/2005/04/discovery"><s:Header><a:To>urn:schemas-xmlsoap-org:ws:2005:04:discovery</a:To><a:Action>http://schemas.xmlsoap.org/ws/2005/04/discovery/Resolve</a:Action><a:MessageID>%s</a:MessageID><a:ReplyTo><a:Address>http://schemas.xmlsoap.org/ws/2004/08/addressing/role/anonymous</a:Address></a:ReplyTo></s:Header><s:Body><d:Resolve><a:EndpointReference><a:Address>%s</a:Address></a:EndpointReference></d:Resolve></s:Body></s:Envelope>`, messageID, deviceID))
}

type wsdResolveEnvelope struct {
	XMLName xml.Name `xml:"http://www.w3.org/2003/05/soap-envelope Envelope"`
	Header  struct {
		RelatesTo string `xml:"http://schemas.xmlsoap.org/ws/2004/08/addressing RelatesTo"`
	} `xml:"http://www.w3.org/2003/05/soap-envelope Header"`
	Body struct {
		Matches struct {
			Items []struct {
				Endpoint struct {
					Address string `xml:"http://schemas.xmlsoap.org/ws/2004/08/addressing Address"`
				} `xml:"http://schemas.xmlsoap.org/ws/2004/08/addressing EndpointReference"`
				XAddrs string `xml:"http://schemas.xmlsoap.org/ws/2005/04/discovery XAddrs"`
			} `xml:"http://schemas.xmlsoap.org/ws/2005/04/discovery ResolveMatch"`
		} `xml:"http://schemas.xmlsoap.org/ws/2005/04/discovery ResolveMatches"`
	} `xml:"http://www.w3.org/2003/05/soap-envelope Body"`
}

// wsdResponseAddress accepts only a matching device ID, a response to this
// request, and an advertised XAddr whose host is the UDP sender. This keeps
// a different device's discovery response from redirecting a copy elsewhere.
func wsdResponseAddress(ctx context.Context, payload []byte, sender net.IP, deviceID, messageID string) (net.IP, bool) {
	var envelope wsdResolveEnvelope
	if len(payload) > 64*1024 || xml.Unmarshal(payload, &envelope) != nil {
		return nil, false
	}
	if !strings.EqualFold(strings.TrimSpace(envelope.Header.RelatesTo), messageID) {
		return nil, false
	}
	for _, match := range envelope.Body.Matches.Items {
		id, err := normalizeWSDDeviceID(match.Endpoint.Address)
		if err != nil || id != deviceID {
			continue
		}
		for _, value := range strings.Fields(match.XAddrs) {
			u, err := url.Parse(value)
			if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
				continue
			}
			if ip := net.ParseIP(u.Hostname()); ip != nil {
				if ip.Equal(sender) {
					return sender, true
				}
				continue
			}
			addresses, err := net.DefaultResolver.LookupIPAddr(ctx, u.Hostname())
			if err != nil {
				continue
			}
			for _, address := range addresses {
				if address.IP.Equal(sender) {
					return sender, true
				}
			}
		}
	}
	return nil, false
}

// discoverWSDAddress sends a directed-by-ID WS-Discovery Resolve on each
// active IPv4 interface. The device must answer live; cached addresses are
// never silently used as a printer's current network location.
func discoverWSDAddress(ctx context.Context, deviceID string) (net.IP, error) {
	messageID, err := wsdMessageID()
	if err != nil {
		return nil, err
	}
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, fmt.Errorf("enumerate network interfaces: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	request := wsdResolveRequest(deviceID, messageID)
	remote, _ := net.ResolveUDPAddr("udp4", wsdMulticast)
	var wg sync.WaitGroup
	results := make(chan net.IP, 16)
	started := 0
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagMulticast == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addresses, addressErr := iface.Addrs()
		if addressErr != nil {
			continue
		}
		for _, address := range addresses {
			ip, _, parseErr := net.ParseCIDR(address.String())
			if parseErr != nil || ip.To4() == nil {
				continue
			}
			started++
			wg.Add(1)
			go func(local net.IP) {
				defer wg.Done()
				conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: local, Port: 0})
				if err != nil {
					return
				}
				defer conn.Close()
				for attempt := 0; attempt < 2; attempt++ {
					_, _ = conn.WriteToUDP(request, remote)
				}
				buffer := make([]byte, 64*1024)
				for {
					if ctx.Err() != nil {
						return
					}
					deadline := time.Now().Add(300 * time.Millisecond)
					if until, ok := ctx.Deadline(); ok && until.Before(deadline) {
						deadline = until
					}
					_ = conn.SetReadDeadline(deadline)
					n, sender, err := conn.ReadFromUDP(buffer)
					if err != nil {
						if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
							continue
						}
						return
					}
					if ip, ok := wsdResponseAddress(ctx, buffer[:n], sender.IP, deviceID, messageID); ok {
						select {
						case results <- ip:
						default:
						}
					}
				}
			}(ip.To4())
		}
	}
	if started == 0 {
		return nil, errors.New("no active multicast-capable IPv4 network interface")
	}
	<-ctx.Done()
	wg.Wait()
	close(results)
	unique := map[string]net.IP{}
	for ip := range results {
		unique[ip.String()] = ip
	}
	if len(unique) == 0 {
		return nil, fmt.Errorf("WSD device %s did not answer discovery on this network", deviceID)
	}
	if len(unique) > 1 {
		var addresses []string
		for address := range unique {
			addresses = append(addresses, address)
		}
		return nil, fmt.Errorf("WSD device %s answered from several IP addresses (%s); cannot choose one safely", deviceID, strings.Join(addresses, ", "))
	}
	for _, ip := range unique {
		return ip, nil
	}
	return nil, errors.New("WSD discovery returned no address")
}

func verifyWSDRaw9100(ctx context.Context, ip net.IP) error {
	connection, err := (&net.Dialer{Timeout: 2 * time.Second}).DialContext(ctx, "tcp", net.JoinHostPort(ip.String(), "9100"))
	if err != nil {
		return fmt.Errorf("WSD printer at %s does not accept RAW TCP 9100: %w", ip, err)
	}
	return connection.Close()
}
