package probe

import (
	"strings"
	"testing"
)

func TestParsePJLInfoID(t *testing.T) {
	response := []byte("\x1b%-12345X@PJL INFO ID\r\n\"HP LaserJet Pro M404dn\"\r\n\f")
	got, err := parsePJLInfoID(response)
	if err != nil {
		t.Fatalf("parsePJLInfoID() error = %v", err)
	}
	if got != "HP LaserJet Pro M404dn" {
		t.Fatalf("parsePJLInfoID() = %q, want %q", got, "HP LaserJet Pro M404dn")
	}
}

func TestParsePJLInfoIDRejectsMalformedTruncatedResponse(t *testing.T) {
	response := []byte("@PJL INFO ID\r\n\"HP LaserJet Pro M404")
	_, err := parsePJLInfoID(response)
	if err == nil {
		t.Fatal("parsePJLInfoID() error = nil, want malformed response error")
	}
	if !strings.Contains(err.Error(), "malformed or truncated") {
		t.Fatalf("parsePJLInfoID() error = %q, want malformed or truncated detail", err)
	}
}
