package main

import (
	"strings"
	"testing"

	"github.com/spilloid/spoolsmith/internal/install"
)

func TestOperationSummaryNamesWhatWillHappen(t *testing.T) {
	tests := []struct {
		name string
		op   operation
		want []string
	}{
		{
			name: "install from a profile",
			op:   operation{Kind: opInstall, PrinterName: "Office", Target: "192.0.2.10"},
			want: []string{"Add printer", "Office", "192.0.2.10"},
		},
		{
			name: "remove with driver purge",
			op:   operation{Kind: opRemove, PrinterName: "Office", PurgeDriver: true},
			want: []string{"Remove printer", "Office", "driver is also removed"},
		},
		{
			name: "apply a bundle",
			op:   operation{Kind: opApply, PrinterName: "Office", BundlePath: `C:\tmp\office.ssb`},
			want: []string{"Office", "office.ssb"},
		},
		{
			name: "repoint keeps the name and driver",
			op:   operation{Kind: opRepoint, PrinterName: "Office", NewAddress: "192.0.2.50"},
			want: []string{"Office", "192.0.2.50", "name and driver stay the same"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			summary := tt.op.Summary()
			for _, want := range tt.want {
				if !strings.Contains(summary, want) {
					t.Fatalf("Summary() = %q, want it to contain %q", summary, want)
				}
			}
		})
	}
}

// Offline means the printer is never contacted, so its identity is not checked.
// The review screen must say that wherever the operation is described, not bury
// it in an advanced panel.
func TestOperationSummaryAlwaysDisclosesOffline(t *testing.T) {
	for _, kind := range []operationKind{opInstall, opConfigure, opApply} {
		op := operation{Kind: kind, PrinterName: "Office", Target: "192.0.2.10", BundlePath: "office.ssb", Offline: true}
		if !strings.Contains(op.Summary(), "identity cannot be checked") {
			t.Fatalf("%s summary hides offline mode: %q", kind, op.Summary())
		}
	}
}

func TestOperationValidateExplainsWhatIsMissing(t *testing.T) {
	tests := []struct {
		name string
		op   operation
		want string
	}{
		{name: "install with nothing", op: operation{Kind: opInstall}, want: "saved printer or enter"},
		{name: "configure without a profile", op: operation{Kind: opConfigure}, want: "saved settings"},
		{name: "remove without a printer", op: operation{Kind: opRemove}, want: "printer to remove"},
		{name: "apply without a file", op: operation{Kind: opApply}, want: "printer file"},
		{name: "repoint without an address", op: operation{Kind: opRepoint, PrinterName: "Office"}, want: "new IP address"},
		{name: "repoint without a printer", op: operation{Kind: opRepoint, NewAddress: "192.0.2.50"}, want: "printer to move"},
		{name: "no kind at all", op: operation{}, want: "what you want to do"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.op.Validate()
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Validate() = %v, want an error containing %q", err, tt.want)
			}
		})
	}
	valid := []operation{
		{Kind: opInstall, Target: "192.0.2.10"},
		{Kind: opInstall, ProfilePath: "office.json"},
		{Kind: opConfigure, ProfilePath: "office.json"},
		{Kind: opRemove, PrinterName: "Office"},
		{Kind: opApply, BundlePath: "office.ssb"},
		{Kind: opRepoint, PrinterName: "Office", NewAddress: "192.0.2.50"},
	}
	for _, op := range valid {
		if err := op.Validate(); err != nil {
			t.Fatalf("Validate() rejected a complete %s operation: %v", op.Kind, err)
		}
	}
}

// An advanced option chosen for one operation must not survive into another.
// The old screen kept these as independent widgets, so a family override set
// for an install stayed set when the operator switched to a removal.
func TestOperationNormalizedClearsInapplicableInputs(t *testing.T) {
	op := operation{
		Kind:        opRemove,
		PrinterName: "Office",
		ForceFamily: "hp-laserjet-m4xx",
		NewAddress:  "192.0.2.50",
		BundlePath:  "office.ssb",
		PurgeDriver: true,
	}.normalized()
	if op.ForceFamily != "" || op.NewAddress != "" || op.BundlePath != "" {
		t.Fatalf("normalized() kept inapplicable inputs: %#v", op)
	}
	if !op.PurgeDriver {
		t.Fatal("normalized() dropped an option that does apply to a removal")
	}

	install := operation{Kind: opInstall, ProfilePath: "office.json", ForceFamily: "hp-laserjet-m4xx"}.normalized()
	if install.ForceFamily != "" {
		t.Fatal("a profile supplies the family; a catalog override must not also apply")
	}
}

func TestQueueRowAndDetailStateCopyability(t *testing.T) {
	ok := install.InstalledQueue{
		PrinterName: "Office", DriverName: "Brother HL-L2315D series",
		PortName: "SpoolSmith-192.0.2.10", HostAddress: "192.0.2.10",
		PortNumber: 9100, Protocol: 1, ProtocolName: "RAW", PortKnown: true,
	}
	row := queueRow(ok)
	if strings.HasPrefix(strings.TrimSpace(row), "!") {
		t.Fatalf("a copyable queue is marked as blocked: %q", row)
	}
	for _, want := range []string{"Office", "192.0.2.10", "RAW/9100", "Brother HL-L2315D series"} {
		if !strings.Contains(row, want) {
			t.Fatalf("queueRow() = %q, want it to contain %q", row, want)
		}
	}
	if !strings.Contains(queueDetail(ok), "can be copied") {
		t.Fatalf("queueDetail() does not say the queue is copyable: %q", queueDetail(ok))
	}

	blocked := install.InstalledQueue{PrinterName: "Microsoft Print to PDF", DriverName: "Microsoft Print To PDF", PortName: "PORTPROMPT:", PortKnown: true}
	if !strings.HasPrefix(strings.TrimSpace(queueRow(blocked)), "!") {
		t.Fatalf("a blocked queue is not marked: %q", queueRow(blocked))
	}
	detail := queueDetail(blocked)
	if !strings.Contains(detail, "cannot be copied") || !strings.Contains(detail, "standard TCP/IP port") {
		t.Fatalf("queueDetail() does not explain why: %q", detail)
	}
}

// The GUI and the CLI must suggest the same file name for the same printer, or
// an operator who uses both ends up with two bundles for one queue.
func TestBundleFileNameMatchesTheCLI(t *testing.T) {
	for _, queue := range []string{"Office", "Accounting HL-L2315D", "Front Desk / Reception", ""} {
		if got, want := bundleFileName(queue), cliDefaultBundleName(queue); got != want {
			t.Fatalf("bundleFileName(%q) = %q, CLI produces %q", queue, got, want)
		}
	}
}

// cliDefaultBundleName mirrors cmd/spoolsmith's defaultBundleName. It is
// duplicated rather than imported because both live in package main; this test
// is what keeps the two from drifting.
func cliDefaultBundleName(queueName string) string {
	var builder strings.Builder
	for _, r := range queueName {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			builder.WriteRune(r)
		default:
			builder.WriteRune('-')
		}
	}
	name := strings.Trim(builder.String(), "-")
	if name == "" {
		name = "printer"
	}
	return name + ".ssb"
}
