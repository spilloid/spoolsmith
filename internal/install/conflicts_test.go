package install

import (
	"context"
	"io"
	"strings"
	"testing"
)

// inventoryEnvironment models the Windows printers and ports a plan's own
// guards read at mutation time, so a preview can be checked against what the
// apply would actually find.
type inventoryEnvironment struct {
	*fakeEnvironment
	printers map[string]PrinterConfiguration // keyed by lower-case name
	ports    map[string]PortConfiguration
}

func (e *inventoryEnvironment) LookupPrinter(_ context.Context, name string) (PrinterConfiguration, error) {
	if configuration, ok := e.printers[strings.ToLower(name)]; ok {
		return configuration, nil
	}
	return PrinterConfiguration{}, ErrPrinterNotFound
}

func (e *inventoryEnvironment) LookupPort(_ context.Context, name string) (PortConfiguration, error) {
	if configuration, ok := e.ports[strings.ToLower(name)]; ok {
		return configuration, nil
	}
	return PortConfiguration{}, ErrPortNotFound
}

func newInventoryEnvironment() *inventoryEnvironment {
	return &inventoryEnvironment{fakeEnvironment: workflowEnvironment(true, true), printers: map[string]PrinterConfiguration{}, ports: map[string]PortConfiguration{}}
}

// A preview that says "Preview complete" for a plan whose own guard is certain
// to stop it at apply time is a false promise: the operator confirms, and the
// first command throws. The preview reads the same inventory the guard reads.
func TestDryRunPredictsTheConflictsTheApplyGuardsWouldStopOn(t *testing.T) {
	cases := []struct {
		name   string
		setup  func(*inventoryEnvironment)
		update bool
		want   string
	}{
		{"same-named queue with another driver", func(e *inventoryEnvironment) {
			e.printers["test model"] = PrinterConfiguration{PrinterName: "Test Model", PortName: "RAW9100-192.0.2.10", DriverName: "Some Other Driver"}
		}, false, "existing printer"},
		{"same-named queue on another port", func(e *inventoryEnvironment) {
			e.printers["test model"] = PrinterConfiguration{PrinterName: "Test Model", PortName: "IP_10.0.0.5", DriverName: "Verified Windows Driver"}
		}, false, "existing printer"},
		{"managed port name pointing elsewhere", func(e *inventoryEnvironment) {
			e.ports["raw9100-192.0.2.10"] = PortConfiguration{PortName: "RAW9100-192.0.2.10", HostAddress: "192.0.2.99", PortNumber: 9100, Protocol: 1}
		}, false, "existing port"},
		{"managed port name using LPR", func(e *inventoryEnvironment) {
			e.ports["raw9100-192.0.2.10"] = PortConfiguration{PortName: "RAW9100-192.0.2.10", HostAddress: "192.0.2.10", PortNumber: 515, Protocol: 2}
		}, true, "existing port"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			env := newInventoryEnvironment()
			c.setup(env)
			outcome, code := testWorkflow(true).RunInstall(context.Background(), env, panicReader{}, io.Discard, false, InstallOptions{Target: "192.0.2.10", DryRun: true, UpdateExisting: c.update})
			if code == ExitSuccess || outcome.Status == "dry-run" {
				t.Fatalf("preview promised success for a plan the apply guard would stop: code=%d %#v", code, outcome)
			}
			if code != ExitGeneralError || !strings.Contains(strings.ToLower(outcome.Error), c.want) {
				t.Fatalf("code=%d error=%q, want exit 1 naming the %s", code, outcome.Error, c.want)
			}
			if len(env.ran) != 0 {
				t.Fatalf("preview ran commands: %#v", env.ran)
			}
		})
	}
}

// The checks must not invent conflicts: a matching queue (re-apply), an update
// the operator asked for, and a clean machine all still preview successfully.
func TestDryRunConflictCheckAllowsReapplyAndRequestedUpdates(t *testing.T) {
	cases := []struct {
		name   string
		setup  func(*inventoryEnvironment)
		update bool
	}{
		{"clean machine", func(*inventoryEnvironment) {}, false},
		{"identical queue and port (re-apply)", func(e *inventoryEnvironment) {
			e.printers["test model"] = PrinterConfiguration{PrinterName: "Test Model", PortName: "raw9100-192.0.2.10", DriverName: "verified windows driver"}
			e.ports["raw9100-192.0.2.10"] = PortConfiguration{PortName: "RAW9100-192.0.2.10", HostAddress: "192.0.2.10", PortNumber: 9100, Protocol: 1}
		}, false},
		{"different queue with --update", func(e *inventoryEnvironment) {
			e.printers["test model"] = PrinterConfiguration{PrinterName: "Test Model", PortName: "IP_10.0.0.5", DriverName: "Old Driver"}
		}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			env := newInventoryEnvironment()
			c.setup(env)
			outcome, code := testWorkflow(true).RunInstall(context.Background(), env, panicReader{}, io.Discard, false, InstallOptions{Target: "192.0.2.10", DryRun: true, UpdateExisting: c.update})
			if code != ExitSuccess || outcome.Status != "dry-run" {
				t.Fatalf("code=%d %#v", code, outcome)
			}
		})
	}
}
