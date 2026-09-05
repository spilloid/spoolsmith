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
