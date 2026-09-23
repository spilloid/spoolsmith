package bundle

import (
	"context"
	"errors"
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

// TestCreateDegradesToOfflineWhenSourcePrinterUnreachable is the fix for the
// actual operator complaint: physically moving a printer routinely means it
// is unplugged, mid-move, or not yet reachable on the new network right when
// someone wants to copy its settings off the old PC. Copy used to fail
// outright in that case, leaving nothing to take to the other machine. It now
// writes a bundle from what Windows already knows about the queue and marks
// the printer's identity unconfirmed, matching the exact offline fallback
// RunInstall already uses when that bundle is later applied.
func TestCreateDegradesToOfflineWhenSourcePrinterUnreachable(t *testing.T) {
	env := newBatchEnvironment(t, "Front Desk")
	dir := t.TempDir()
	path := filepath.Join(dir, "front-desk.ssb")
	calls := 0
	unreachable := func(context.Context, string) (probe.Result, error) {
		calls++
		return probe.Result{}, errors.New("connection refused")
	}
	result, err := Create(context.Background(), env, unreachable, CreateOptions{QueueName: "Front Desk", Path: path})
	if err != nil {
		t.Fatalf("Create() = %v, want a degraded success", err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want the one built-in retry", calls)
	}
	profile := result.Manifest.Profile
	if profile.Evidence.Provenance != "unconfirmed" {
		t.Fatalf("Provenance = %q, want unconfirmed", profile.Evidence.Provenance)
	}
	if strings.TrimSpace(profile.Evidence.ProvenanceNote) == "" {
		t.Fatal("no ProvenanceNote explaining the degraded capture")
	}
	if profile.PrinterName != "Front Desk" || profile.Target != "192.0.2.10" {
		t.Fatalf("profile did not carry the locally known queue: %+v", profile)
	}

	// A degraded capture must still be a valid, ordinary bundle: applying it
	// later only knows how to read a bundle, never a special case.
	opened, err := Open(path)
	if err != nil {
		t.Fatalf("Open() = %v", err)
	}
	defer opened.Close()
	if err := opened.Verify(); err != nil {
		t.Fatalf("Verify() = %v", err)
	}
	if opened.Manifest.Profile.Evidence.Provenance != "unconfirmed" {
		t.Fatalf("round-tripped Provenance = %q", opened.Manifest.Profile.Evidence.Provenance)
	}
}

// TestCreateStillFailsOnCancellation guards the offline fallback above: a
// canceled copy must never be reported as a degraded success.
func TestCreateStillFailsOnCancellation(t *testing.T) {
	env := newBatchEnvironment(t, "Front Desk")
	dir := t.TempDir()
	path := filepath.Join(dir, "front-desk.ssb")
	ctx, cancel := context.WithCancel(context.Background())
	collect := func(context.Context, string) (probe.Result, error) {
		cancel()
		return probe.Result{}, errors.New("no answer")
	}
	_, err := Create(ctx, env, collect, CreateOptions{QueueName: "Front Desk", Path: path})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Create() = %v, want context.Canceled", err)
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Fatal("a canceled copy left a bundle file behind")
	}
}
