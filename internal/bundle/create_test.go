package bundle

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spilloid/spoolsmith/internal/probe"
)

// TestCreatePreflightsExistingDestination guards against a single copy paying
// for a network probe (and, with --include-driver, an elevated driver
// export) before discovering the destination file already exists -- the same
// problem CreateAll's batch preflight (TestCreateAllPreflightsExistingFilesAndBatchNameCollisions)
// solves for a batch of queues.
func TestCreatePreflightsExistingDestination(t *testing.T) {
	env := newBatchEnvironment(t, "Front Desk")
	dir := t.TempDir()
	path := filepath.Join(dir, "existing.ssb")
	if err := os.WriteFile(path, []byte("keep this file"), 0600); err != nil {
		t.Fatal(err)
	}
	var probes int
	_, err := Create(context.Background(), env, func(ctx context.Context, address string) (probe.Result, error) {
		probes++
		return batchCollect(ctx, address)
	}, CreateOptions{QueueName: "Front Desk", Path: path})
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("Create() over an existing bundle = %v, want an already-exists error", err)
	}
	if probes != 0 || len(env.lookups) != 0 {
		t.Fatalf("Create() did work before checking the destination: probes = %d, lookups = %v", probes, env.lookups)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "keep this file" {
		t.Fatalf("existing file changed: %q, %v", data, err)
	}
}
