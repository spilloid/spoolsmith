package probe

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"net"
	"strings"
	"testing"
	"time"
)

const knownGoodSNMPResponse = "304a02010004067075626c6963a23d0201010201000201003032301806082b06010201010100040c54657374205072696e746572301606082b06010201010200060a2b060104010b02030901"

func TestSNMPRequestIDUsesRandom31BitValues(t *testing.T) {
	first := newSNMPRequestID()
	if first < 0 || uint64(first) > 0x7fffffff {
		t.Fatalf("newSNMPRequestID() = %d, want a non-negative 31-bit value", first)
	}
	for i := 0; i < 32; i++ {
		id := newSNMPRequestID()
		if id < 0 || uint64(id) > 0x7fffffff {
			t.Fatalf("newSNMPRequestID() = %d, want a non-negative 31-bit value", id)
		}
		if id != first {
			return
		}
	}
	t.Fatal("newSNMPRequestID() returned the same value 33 times; want randomized IDs")
}

func TestEncodeSNMPGetRequestKnownBytes(t *testing.T) {
	got, err := encodeSNMPGetRequest(1, []string{sysDescrOID, sysObjectIDOID})
	if err != nil {
		t.Fatalf("encodeSNMPGetRequest() error = %v", err)
	}
	// Hand-constructed from the RFC 1157 message, GetRequest-PDU, and
	// VarBindList shapes. This expectation is not produced by the encoder.
	want := mustDecodeHex(t, "303402010004067075626c6963a027020101020100020100301c300c06082b060102010101000500300c06082b060102010102000500")
	if !bytes.Equal(got, want) {
		t.Fatalf("encodeSNMPGetRequest() = %x, want %x", got, want)
	}
}

func TestDecodeSNMPGetResponseKnownGoodBytes(t *testing.T) {
	// Independently hand-constructed SNMP v1 GetResponse. It contains request
	// ID 1, a sysDescr OCTET STRING, and a sysObjectID OBJECT IDENTIFIER.
	response := mustDecodeHex(t, knownGoodSNMPResponse)
	got, err := decodeSNMPGetResponse(response, 1)
	if err != nil {
		t.Fatalf("decodeSNMPGetResponse() error = %v", err)
	}
	if got[sysDescrOID] != "Test Printer" {
		t.Fatalf("sysDescr = %q, want %q", got[sysDescrOID], "Test Printer")
	}
	if got[sysObjectIDOID] != "1.3.6.1.4.1.11.2.3.9.1" {
		t.Fatalf("sysObjectID = %q, want %q", got[sysObjectIDOID], "1.3.6.1.4.1.11.2.3.9.1")
	}
}

func TestBERRejectsOversizedLongFormLengthWithoutPanic(t *testing.T) {
	input := mustDecodeHex(t, "3084ffffffff")
	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("readAny() panicked: %v", recovered)
		}
	}()
	if _, _, err := newBERReader(input).readAny(); err == nil {
		t.Fatal("readAny() error = nil, want oversized BER length error")
	}
}

func TestDecodeOIDRejectsNonMinimalSubidentifier(t *testing.T) {
	if _, err := decodeOID([]byte{0x2b, 0x80, 0x00}); err == nil {
		t.Fatal("decodeOID() error = nil, want non-minimal subidentifier error")
	}
}

func TestDecodeSNMPGetResponseEnforcesValueTypeByOID(t *testing.T) {
	tests := []struct {
		name    string
		oid     string
		badTag  byte
		wantErr string
	}{
		{name: "sysDescr must be an OCTET STRING", oid: sysDescrOID, badTag: 0x06, wantErr: "sysDescr has tag"},
		{name: "sysObjectID must be an OBJECT IDENTIFIER", oid: sysObjectIDOID, badTag: 0x04, wantErr: "sysObjectID has tag"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := mustDecodeHex(t, knownGoodSNMPResponse)
			encodedOID, err := encodeOID(test.oid)
			if err != nil {
				t.Fatal(err)
			}
			bindingOID := append([]byte{0x06, byte(len(encodedOID))}, encodedOID...)
			position := bytes.Index(response, bindingOID)
			if position < 0 {
				t.Fatalf("test response does not contain OID %s", test.oid)
			}
			response[position+len(bindingOID)] = test.badTag
			_, err = decodeSNMPGetResponse(response, 1)
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("decodeSNMPGetResponse() error = %v, want error containing %q", err, test.wantErr)
			}
		})
	}
}

func TestDecodeSNMPGetResponseRejectsTruncatedInput(t *testing.T) {
	response := mustDecodeHex(t, "304a02010004067075626c6963a23d020101")
	if _, err := decodeSNMPGetResponse(response, 1); err == nil {
		t.Fatal("decodeSNMPGetResponse() error = nil, want malformed response error")
	}
}

func mustDecodeHex(t *testing.T, value string) []byte {
	t.Helper()
	decoded, err := hex.DecodeString(value)
	if err != nil {
		t.Fatalf("DecodeString() error = %v", err)
	}
	return decoded
}

// snmpTestResponse encodes a GetResponse for requestID with the given
// error-status, carrying sysDescr "Test Printer".
func snmpTestResponse(t *testing.T, requestID, errorStatus int) []byte {
	t.Helper()
	descrOID, err := encodeOID(sysDescrOID)
	if err != nil {
		t.Fatal(err)
	}
	objectOID, err := encodeOID(sysObjectIDOID)
	if err != nil {
		t.Fatal(err)
	}
	objectValue, err := encodeOID("1.3.6.1.4.1.11.2.3.9.1")
	if err != nil {
		t.Fatal(err)
	}
	varBinds := append(tlv(0x30, append(tlv(0x06, descrOID), tlv(0x04, []byte("Test Printer"))...)),
		tlv(0x30, append(tlv(0x06, objectOID), tlv(0x06, objectValue)...))...)
	pdu := append(encodeInteger(requestID), encodeInteger(errorStatus)...)
	pdu = append(pdu, encodeInteger(0)...)
	pdu = append(pdu, tlv(0x30, varBinds)...)
	message := append(encodeInteger(0), tlv(0x04, []byte("public"))...)
	message = append(message, tlv(0xa2, pdu)...)
	return tlv(0x30, message)
}

// fakeSNMPAgent answers the first request it receives with replies(requestID).
func fakeSNMPAgent(t *testing.T, replies func(requestID int) [][]byte) string {
	t.Helper()
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	go func() {
		buffer := make([]byte, 8192)
		n, from, err := conn.ReadFrom(buffer)
		if err != nil {
			return
		}
		message, err := newBERReader(buffer[:n]).readConstructed(0x30)
		if err != nil {
			return
		}
		message.readInteger()
		message.readValue(0x04)
		pdu, err := message.readConstructed(0xa0)
		if err != nil {
			return
		}
		requestID, err := pdu.readInteger()
		if err != nil {
			return
		}
		for _, reply := range replies(requestID) {
			conn.WriteTo(reply, from)
		}
	}()
	return conn.LocalAddr().String()
}

func TestSNMPKeepsListeningPastForeignDatagrams(t *testing.T) {
	address := fakeSNMPAgent(t, func(requestID int) [][]byte {
		return [][]byte{
			[]byte("not BER at all"),
			snmpTestResponse(t, requestID+1, 0), // a stale reply to another request
			snmpTestResponse(t, requestID, 0),
		}
	})
	started := time.Now()
	descr, objectID, err := snmpGet(context.Background(), address)
	if err != nil {
		t.Fatalf("snmpGet() error = %v", err)
	}
	if descr != "Test Printer" || objectID != "1.3.6.1.4.1.11.2.3.9.1" {
		t.Fatalf("snmpGet() = %q, %q", descr, objectID)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("snmpGet() took %v; it should not have waited for a retry", elapsed)
	}
}

func TestSNMPStopsAtItsOwnErrorReply(t *testing.T) {
	address := fakeSNMPAgent(t, func(requestID int) [][]byte {
		return [][]byte{snmpTestResponse(t, requestID, 2)}
	})
	started := time.Now()
	if _, _, err := snmpGet(context.Background(), address); err == nil || !strings.Contains(err.Error(), "error-status 2") {
		t.Fatalf("snmpGet() error = %v, want the agent's error-status", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("snmpGet() kept listening after its own reply: %v", elapsed)
	}
}

func TestDecodeSNMPGetResponseMarksForeignDatagrams(t *testing.T) {
	if _, err := decodeSNMPGetResponse([]byte("garbage"), 1); !errors.Is(err, errForeignDatagram) {
		t.Fatalf("garbage: error = %v, want errForeignDatagram", err)
	}
	if _, err := decodeSNMPGetResponse(mustDecodeHex(t, knownGoodSNMPResponse), 2); !errors.Is(err, errForeignDatagram) {
		t.Fatalf("other request ID: error = %v, want errForeignDatagram", err)
	}
	if _, err := decodeSNMPGetResponse(snmpTestResponse(t, 1, 2), 1); err == nil || errors.Is(err, errForeignDatagram) {
		t.Fatalf("own error reply: error = %v, want a non-foreign error", err)
	}
}
