package install

import (
	"strings"
	"testing"
)

func TestCopyBlockedReasonMatchesWhatCloneWouldDo(t *testing.T) {
	tests := []struct {
		name     string
		queue    InstalledQueue
		copyable bool
		want     string
	}{
		{
			name:     "RAW 9100 with a literal address",
			queue:    InstalledQueue{PortName: "SpoolSmith-192.0.2.10", HostAddress: "192.0.2.10", PortNumber: 9100, Protocol: 1, PortKnown: true},
			copyable: true,
		},
		{
			name:     "port Windows no longer reports",
			queue:    InstalledQueue{PortName: "GONE", PortKnown: false},
			copyable: false,
			want:     "no longer exists",
		},
		{
			name:     "LPR",
			queue:    InstalledQueue{PortName: "IP_192.0.2.10", HostAddress: "192.0.2.10", PortNumber: 515, Protocol: 2, PortKnown: true},
			copyable: false,
			want:     "not RAW",
		},
		{
			name:     "non-default TCP port",
			queue:    InstalledQueue{PortName: "IP_192.0.2.10", HostAddress: "192.0.2.10", PortNumber: 9101, Protocol: 1, PortKnown: true},
			copyable: false,
			want:     "only maps the RAW 9100 default",
		},
		{
			name:     "host name rather than an address",
			queue:    InstalledQueue{PortName: "IP_printer.local", HostAddress: "printer.local", PortNumber: 9100, Protocol: 1, PortKnown: true},
			copyable: false,
			want:     "host name rather than a literal IP",
		},
		{
			name:     "not a TCP/IP port at all",
			queue:    InstalledQueue{PortName: "PORTPROMPT:", PortKnown: true},
			copyable: false,
			want:     "not a standard TCP/IP port",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.queue.Copyable(); got != tt.copyable {
				t.Fatalf("Copyable() = %t, want %t (reason %q)", got, tt.copyable, tt.queue.CopyBlockedReason())
			}
			reason := tt.queue.CopyBlockedReason()
			if tt.want == "" {
				if reason != "" {
					t.Fatalf("CopyBlockedReason() = %q, want empty", reason)
				}
				return
			}
			if !strings.Contains(reason, tt.want) {
				t.Fatalf("CopyBlockedReason() = %q, want it to contain %q", reason, tt.want)
			}
		})
	}
}

// TestListingAgreesWithCloneQueue is the property that matters: the listing
// must never advertise a queue as copyable that CloneQueue would then refuse,
// and never hide one it would have accepted. Both read the same rule, and this
// test is what keeps that true if either side is edited.
func TestListingAgreesWithCloneQueue(t *testing.T) {
	ports := []PortConfiguration{
		{PortName: "SpoolSmith-192.0.2.10", HostAddress: "192.0.2.10", PortNumber: 9100, Protocol: 1},
		{PortName: "IP_192.0.2.10", HostAddress: "192.0.2.10", PortNumber: 515, Protocol: 2},
		{PortName: "IP_192.0.2.10", HostAddress: "192.0.2.10", PortNumber: 9101, Protocol: 1},
		{PortName: "IP_printer.local", HostAddress: "printer.local", PortNumber: 9100, Protocol: 1},
		{PortName: "PORTPROMPT:", PortNumber: 9100, Protocol: 1},
	}
	for _, port := range ports {
		_, cloneErr := CloneQueue(t.Context(), cloneEnv(port), "Test Printer")
		queue := InstalledQueue{
			PortName:    port.PortName,
			HostAddress: port.HostAddress,
			PortNumber:  port.PortNumber,
			Protocol:    port.Protocol,
			PortKnown:   true,
		}
		if (cloneErr == nil) != queue.Copyable() {
			t.Fatalf("port %+v: CloneQueue error = %v but Copyable() = %t", port, cloneErr, queue.Copyable())
		}
	}
}

func TestDecodeInstalledQueues(t *testing.T) {
	queues, err := decodeInstalledQueues(`[{"printer_name":"Office","driver_name":"Brother HL-L2315D series","port_name":"SpoolSmith-192.0.2.10","host_address":"192.0.2.10","port_number":9100,"protocol":1,"port_known":true,"shared":false}]`)
	if err != nil {
		t.Fatal(err)
	}
	if len(queues) != 1 {
		t.Fatalf("decodeInstalledQueues() returned %d queues", len(queues))
	}
	// A PascalCase payload would decode to an empty struct with a nil error,
	// so assert the fields actually arrived rather than only the count.
	if queues[0].PrinterName != "Office" || queues[0].DriverName != "Brother HL-L2315D series" || queues[0].HostAddress != "192.0.2.10" {
		t.Fatalf("decodeInstalledQueues() = %#v", queues[0])
	}
	if queues[0].ProtocolName != "RAW" {
		t.Fatalf("protocol 1 rendered as %q, want RAW", queues[0].ProtocolName)
	}
	if !queues[0].Copyable() {
		t.Fatalf("expected a RAW 9100 queue to be copyable: %s", queues[0].CopyBlockedReason())
	}
}

func TestDecodeInstalledQueuesEmptyMachine(t *testing.T) {
	queues, err := decodeInstalledQueues("  ")
	if err != nil || queues != nil {
		t.Fatalf("decodeInstalledQueues() = %v, %v", queues, err)
	}
	if _, err := decodeInstalledQueues("[]"); err != nil {
		t.Fatal(err)
	}
}

// The listing command must force an array. ConvertTo-Json emits a bare object
// for a single queue, and Windows PowerShell 5.1 has no -AsArray.
func TestListPrintersCommandForcesAnArray(t *testing.T) {
	command := listPrintersCommand()
	for _, want := range []string{"Get-Printer", "Get-PrinterPort", "ConvertTo-Json -Compress", "'[' +", "-join ','"} {
		if !strings.Contains(command, want) {
			t.Fatalf("listPrintersCommand() is missing %q: %s", want, command)
		}
	}
	if strings.Contains(command, "-AsArray") {
		t.Fatal("listPrintersCommand() uses -AsArray, which Windows PowerShell 5.1 does not have")
	}
}

func TestProtocolName(t *testing.T) {
	for protocol, want := range map[int]string{1: "RAW", 2: "LPR", 0: "", 7: "unknown (7)"} {
		if got := protocolName(protocol); got != want {
			t.Fatalf("protocolName(%d) = %q, want %q", protocol, got, want)
		}
	}
}
