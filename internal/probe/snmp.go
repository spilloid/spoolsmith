package probe

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net"
	"time"
)

const (
	sysDescrOID    = "1.3.6.1.2.1.1.1.0"
	sysObjectIDOID = "1.3.6.1.2.1.1.2.0"
)

func probeSNMP(ctx context.Context, host string) (string, string, error) {
	probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(probeCtx, "udp", net.JoinHostPort(host, "161"))
	if err != nil {
		return "", "", fmt.Errorf("SNMP connect: %w", err)
	}
	defer conn.Close()

	// A random ID removes wall-clock predictability, but cannot authenticate
	// SNMPv1; catalog.Resolve still applies its existing fail-closed policy.
	requestID := newSNMPRequestID()
	request, err := encodeSNMPGetRequest(requestID, []string{sysDescrOID, sysObjectIDOID})
	if err != nil {
		return "", "", fmt.Errorf("SNMP request: %w", err)
	}
	buffer := make([]byte, 8192)
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		deadline := time.Now().Add(1400 * time.Millisecond)
		if parentDeadline, ok := probeCtx.Deadline(); ok && parentDeadline.Before(deadline) {
			deadline = parentDeadline
		}
		if err := conn.SetDeadline(deadline); err != nil {
			return "", "", fmt.Errorf("SNMP deadline: %w", err)
		}
		if _, err := conn.Write(request); err != nil {
			return "", "", fmt.Errorf("SNMP write: %w", err)
		}
		n, err := conn.Read(buffer)
		if err == nil {
			values, err := decodeSNMPGetResponse(buffer[:n], requestID)
			if err != nil {
				return "", "", fmt.Errorf("SNMP response: %w", err)
			}
			return values[sysDescrOID], values[sysObjectIDOID], nil
		}
		lastErr = err
		var netErr net.Error
		if !errors.As(err, &netErr) || !netErr.Timeout() || probeCtx.Err() != nil {
			break
		}
	}
	return "", "", fmt.Errorf("SNMP read after one retry: %w", lastErr)
}

func newSNMPRequestID() int {
	return int(rand.Uint32() & 0x7fffffff)
}

// RFC 1157 section 4.1 defines an SNMP message as version, community, and
// data; section 4.1.2 defines GetRequest/GetResponse PDUs and VarBindList.
func encodeSNMPGetRequest(requestID int, oids []string) ([]byte, error) {
	var varBinds []byte
	for _, oid := range oids {
		encodedOID, err := encodeOID(oid)
		if err != nil {
			return nil, err
		}
		varBinds = append(varBinds, tlv(0x30, append(tlv(0x06, encodedOID), tlv(0x05, nil)...))...)
	}
	pduContent := append(encodeInteger(requestID), encodeInteger(0)...)
	pduContent = append(pduContent, encodeInteger(0)...)
	pduContent = append(pduContent, tlv(0x30, varBinds)...)
	message := append(encodeInteger(0), tlv(0x04, []byte("public"))...)
	message = append(message, tlv(0xa0, pduContent)...)
	return tlv(0x30, message), nil
}

func decodeSNMPGetResponse(data []byte, expectedRequestID int) (map[string]string, error) {
	outer := newBERReader(data)
	message, err := outer.readConstructed(0x30)
	if err != nil || !outer.empty() {
		return nil, fmt.Errorf("invalid message sequence")
	}
	version, err := message.readInteger()
	if err != nil || version != 0 {
		return nil, fmt.Errorf("invalid SNMP v1 version")
	}
	community, err := message.readValue(0x04)
	if err != nil || string(community) != "public" {
		return nil, fmt.Errorf("invalid community")
	}
	pdu, err := message.readConstructed(0xa2)
	if err != nil || !message.empty() {
		return nil, fmt.Errorf("invalid GetResponse PDU")
	}
	requestID, err := pdu.readInteger()
	if err != nil || requestID != expectedRequestID {
		return nil, fmt.Errorf("unexpected request ID")
	}
	errorStatus, err := pdu.readInteger()
	if err != nil {
		return nil, fmt.Errorf("invalid error-status")
	}
	if _, err := pdu.readInteger(); err != nil {
		return nil, fmt.Errorf("invalid error-index")
	}
	if errorStatus != 0 {
		return nil, fmt.Errorf("agent returned error-status %d", errorStatus)
	}
	list, err := pdu.readConstructed(0x30)
	if err != nil || !pdu.empty() {
		return nil, fmt.Errorf("invalid variable-bind list")
	}
	values := make(map[string]string)
	for !list.empty() {
		binding, err := list.readConstructed(0x30)
		if err != nil {
			return nil, fmt.Errorf("invalid variable binding")
		}
		oidBytes, err := binding.readValue(0x06)
		if err != nil {
			return nil, fmt.Errorf("invalid variable OID")
		}
		oid, err := decodeOID(oidBytes)
		if err != nil {
			return nil, fmt.Errorf("invalid variable OID: %w", err)
		}
		tag, value, err := binding.readAny()
		if err != nil || !binding.empty() {
			return nil, fmt.Errorf("invalid variable value")
		}
		switch oid {
		case sysDescrOID:
			if tag != 0x04 {
				return nil, fmt.Errorf("sysDescr has tag 0x%02x, want OCTET STRING", tag)
			}
			values[oid] = string(value)
		case sysObjectIDOID:
			if tag != 0x06 {
				return nil, fmt.Errorf("sysObjectID has tag 0x%02x, want OBJECT IDENTIFIER", tag)
			}
			decoded, err := decodeOID(value)
			if err != nil {
				return nil, fmt.Errorf("invalid OID value: %w", err)
			}
			values[oid] = decoded
		default:
			return nil, fmt.Errorf("unexpected variable OID %s", oid)
		}
	}
	if _, ok := values[sysDescrOID]; !ok {
		return nil, fmt.Errorf("response omitted sysDescr")
	}
	if _, ok := values[sysObjectIDOID]; !ok {
		return nil, fmt.Errorf("response omitted sysObjectID")
	}
	return values, nil
}
