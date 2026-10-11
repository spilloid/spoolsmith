package probe

import (
	"context"
	"testing"
	"time"
)

func TestCollectRejectsEmptyTargetAsFatalPrecondition(t *testing.T) {
	if _, err := Collect(context.Background(), "  "); err == nil {
		t.Fatal("Collect() error = nil, want resolution precondition error")
	}
}

func TestCollectRunsConcurrentProbes(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := Collect(ctx, "127.0.0.1")
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if result.Evidence.IP != "127.0.0.1" {
		t.Fatalf("Collect() IP = %q, want 127.0.0.1", result.Evidence.IP)
	}
	if len(result.Probes) != 6 {
		t.Fatalf("Collect() returned %d probe outcomes, want 6", len(result.Probes))
	}
}

// TestUnansweredCountsOnlyProbesThatReachTheDevice: OUI and hostname come
// from the ARP cache and DNS, and the ports probe always "succeeds", so only
// SNMP/HTTP/PJL answers and open ports show the device itself answered.
func TestUnansweredCountsOnlyProbesThatReachTheDevice(t *testing.T) {
	dead := []ProbeOutcome{{Name: "snmp"}, {Name: "http"}, {Name: "pjl"}, {Name: "ports", Success: true}, {Name: "oui", Success: true}, {Name: "hostname", Success: true}}
	if !(Result{Probes: dead}).Unanswered() {
		t.Fatal("a printer that answered nothing reads as answered")
	}
	answered := append([]ProbeOutcome(nil), dead...)
	answered[1].Success = true
	if (Result{Probes: answered}).Unanswered() {
		t.Fatal("an HTTP answer reads as unanswered")
	}
	withPort := Result{Probes: dead}
	withPort.Evidence.OpenPorts = []int{9100}
	if withPort.Unanswered() {
		t.Fatal("an open port reads as unanswered")
	}
	if (Result{}).Unanswered() {
		t.Fatal("a result with no probe records (fixture) reads as unanswered")
	}
}
