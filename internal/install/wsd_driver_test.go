package install

import (
	"net"
	"testing"
)

func TestWSDRAWDriverRequiresExistingMatchingTransport(t *testing.T) {
	good := InstalledQueue{PrinterName: "Vendor RAW", DriverName: "Vendor OEM Driver", PortName: "RAW9100-192.0.2.10", HostAddress: "192.0.2.10", PortKnown: true, Protocol: 1, PortNumber: 9100}
	for _, tc := range []struct {
		name   string
		change func(*InstalledQueue)
		want   bool
	}{
		{"exact mapping", func(*InstalledQueue) {}, true},
		{"other IP", func(q *InstalledQueue) { q.HostAddress = "192.0.2.11" }, false},
		{"hostname not verified", func(q *InstalledQueue) { q.HostAddress = "printer.example" }, false},
		{"missing port", func(q *InstalledQueue) { q.PortKnown = false }, false},
		{"LPR", func(q *InstalledQueue) { q.Protocol = 2 }, false},
		{"other TCP port", func(q *InstalledQueue) { q.PortNumber = 631 }, false},
		{"IPP driver on RAW", func(q *InstalledQueue) { q.DriverName = "Microsoft IPP Class Driver" }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := good
			tc.change(&q)
			driver, source := selectWSDRAWDriver([]InstalledQueue{q}, net.ParseIP("192.0.2.10"))
			if (driver != "") != tc.want || (tc.want && source != good.PrinterName) {
				t.Fatalf("selection = %q, %q", driver, source)
			}
		})
	}
	other := good
	other.PrinterName, other.DriverName = "Other RAW", "Different OEM Driver"
	if driver, _ := selectWSDRAWDriver([]InstalledQueue{good, other}, net.ParseIP("192.0.2.10")); driver != "" {
		t.Fatal("guessed between conflicting RAW drivers")
	}
	other.DriverName = good.DriverName
	if driver, source := selectWSDRAWDriver([]InstalledQueue{good, other}, net.ParseIP("192.0.2.10")); driver != good.DriverName || source != other.PrinterName {
		t.Fatalf("duplicate driver selection must be deterministic: %q, %q", driver, source)
	}
}
