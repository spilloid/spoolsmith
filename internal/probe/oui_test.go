package probe

import (
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

func TestOUITableCitedEntries(t *testing.T) {
	tests := []struct {
		mac  string
		want string
	}{
		{mac: "00:1b:78:12:34:56", want: "HP"},
		{mac: "00:80:77:ab:cd:ef", want: "Brother"},
	}
	for _, test := range tests {
		mac, err := net.ParseMAC(test.mac)
		if err != nil {
			t.Fatalf("ParseMAC(%q) error = %v", test.mac, err)
		}
		got, ok := lookupOUI(mac)
		if !ok || got != test.want {
			t.Errorf("lookupOUI(%q) = %q, %v; want %q, true", test.mac, got, ok, test.want)
		}
	}
}

func TestOUITableDoesNotGuess(t *testing.T) {
	mac, err := net.ParseMAC("02:00:00:00:00:01")
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := lookupOUI(mac); ok {
		t.Fatalf("lookupOUI() = %q, true; want no match", got)
	}
}

func TestProbeOUIPrimesNeighborAndRetriesColdARPCache(t *testing.T) {
	const target = "192.0.2.10"
	events := make([]string, 0, 6)
	reads := 0
	prime := func(ip string) {
		events = append(events, "prime "+ip)
	}
	open := func() (io.ReadCloser, error) {
		reads++
		events = append(events, fmt.Sprintf("read %d", reads))
		contents := "IP address HW type Flags HW address Mask Device\n"
		if reads == 3 {
			contents += target + " 0x1 0x2 00:1b:78:12:34:56 * eth0\n"
		}
		return io.NopCloser(strings.NewReader(contents)), nil
	}
	sleep := func(delay time.Duration) {
		events = append(events, "sleep "+delay.String())
	}

	vendor, err := probeOUIWith(target, prime, open, sleep)
	if err != nil {
		t.Fatalf("probeOUIWith() error = %v", err)
	}
	if vendor != "HP" {
		t.Fatalf("probeOUIWith() = %q, want HP", vendor)
	}
	want := []string{"prime " + target, "read 1", "sleep 150ms", "read 2", "sleep 150ms", "read 3"}
	if strings.Join(events, ",") != strings.Join(want, ",") {
		t.Fatalf("events = %v, want %v", events, want)
	}
}
