package bundle

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/spilloid/spoolsmith/internal/evidence"
	"github.com/spilloid/spoolsmith/internal/install"
	"github.com/spilloid/spoolsmith/internal/probe"
)

type batchEnvironment struct {
	t             *testing.T
	queues        []install.InstalledQueue
	lookupErrors  map[string]error
	lookups       []string
	listed        int
	elevated      bool
	elevationRead int
	exports       []string
}

func newBatchEnvironment(t *testing.T, names ...string) *batchEnvironment {
	t.Helper()
	env := &batchEnvironment{t: t, elevated: true, lookupErrors: map[string]error{}}
	for i, name := range names {
		address := fmt.Sprintf("192.0.2.%d", i+10)
		env.queues = append(env.queues, install.InstalledQueue{
			PrinterName: name, DriverName: "Driver for " + name,
			PortName: "IP_" + address, HostAddress: address,
			PortNumber: 9100, Protocol: 1, PortKnown: true,
		})
	}
	return env
}

func (e *batchEnvironment) ListPrinters(context.Context) ([]install.InstalledQueue, error) {
	e.listed++
	return e.queues, nil
}

func (e *batchEnvironment) IsElevated(context.Context) (bool, error) {
	e.elevationRead++
	return e.elevated, nil
}

func (e *batchEnvironment) DriverPresent(context.Context, string) (bool, error) {
	e.t.Fatal("copy unexpectedly queried target driver installation")
	return false, nil
}

func (e *batchEnvironment) Run(context.Context, string) (string, error) {
	e.t.Fatal("copy unexpectedly ran a Windows mutation")
	return "", nil
}

func (e *batchEnvironment) LookupPrinter(_ context.Context, name string) (install.PrinterConfiguration, error) {
	e.lookups = append(e.lookups, name)
	if err := e.lookupErrors[name]; err != nil {
		return install.PrinterConfiguration{}, err
	}
	for _, queue := range e.queues {
		if queue.PrinterName == name {
			return install.PrinterConfiguration{PrinterName: name, DriverName: queue.DriverName, PortName: queue.PortName}, nil
		}
	}
	return install.PrinterConfiguration{}, fmt.Errorf("unknown queue %q", name)
}

func (e *batchEnvironment) LookupPort(_ context.Context, name string) (install.PortConfiguration, error) {
	for _, queue := range e.queues {
		if queue.PortName == name {
			return install.PortConfiguration{PortName: name, HostAddress: queue.HostAddress, PortNumber: queue.PortNumber, Protocol: queue.Protocol}, nil
		}
	}
	return install.PortConfiguration{}, fmt.Errorf("unknown port %q", name)
}

func (e *batchEnvironment) ExportDriver(_ context.Context, name, dest string) (install.DriverExport, error) {
	e.exports = append(e.exports, name)
	if err := os.WriteFile(filepath.Join(dest, "printer.inf"), []byte("[Version]\n"), 0600); err != nil {
		return install.DriverExport{}, err
	}
	return install.DriverExport{PublishedName: "oem15.inf", OriginalName: "printer.inf"}, nil
}

func batchCollect(_ context.Context, address string) (probe.Result, error) {
	return probe.Result{Evidence: evidence.Evidence{IP: address, Provenance: "captured", HTTPTitle: "Office printer"}}, nil
}

func TestCreateAllReportsMixedOutcomesAndPreservesEachQueue(t *testing.T) {
	env := newBatchEnvironment(t, "Office", "PDF", "Gone", "Warehouse")
	env.queues[1].HostAddress = ""
	env.lookupErrors["Gone"] = errors.New("queue was removed")
	var probed, progressed []string
	result, err := CreateAll(context.Background(), env, func(ctx context.Context, address string) (probe.Result, error) {
		probed = append(probed, address)
		return batchCollect(ctx, address)
	}, AllOptions{
		OutputDir: filepath.Join(t.TempDir(), "bundles"), Note: "Site move", CreatedBy: "test build", SourceHost: "DESK-01",
		Progress: func(name, _ string) { progressed = append(progressed, name) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Requested != 4 || result.Written != 2 || result.Skipped != 1 || result.Failed != 1 || len(result.Queues) != 4 {
		t.Fatalf("result = %+v", result)
	}
	if !slices.Equal(env.lookups, []string{"Office", "Gone", "Warehouse"}) || !slices.Equal(probed, []string{"192.0.2.10", "192.0.2.13"}) {
		t.Fatalf("lookups = %v; probes = %v", env.lookups, probed)
	}
	if result.Queues[1].Status != "skipped" || !strings.Contains(result.Queues[1].Reason, "no printer host address") || result.Queues[2].Status != "error" || !strings.Contains(result.Queues[2].Reason, "queue was removed") {
		t.Fatalf("missing failure reasons: %+v", result.Queues)
	}
	for _, index := range []int{0, 3} {
		outcome := result.Queues[index]
		opened, err := Open(outcome.Bundle)
		if err != nil {
			t.Fatal(err)
		}
		if err := opened.Verify(); err != nil {
			t.Fatal(err)
		}
		manifest := opened.Manifest
		opened.Close()
		queue := env.queues[index]
		if outcome.Status != "written" || manifest.Profile.PrinterName != queue.PrinterName || manifest.Profile.Target != queue.HostAddress || manifest.Profile.DriverName != queue.DriverName {
			t.Fatalf("queue %q has the wrong bundle: %+v", queue.PrinterName, manifest)
		}
		if manifest.Note != "Site move" || manifest.CreatedBy != "test build" || manifest.SourceHost != "DESK-01" || manifest.Driver != nil {
			t.Fatalf("manifest options were not preserved: %+v", manifest)
		}
		if !slices.Contains(progressed, queue.PrinterName) {
			t.Fatalf("no progress for %q: %v", queue.PrinterName, progressed)
		}
	}
	if env.elevationRead != 0 || len(env.exports) != 0 {
		t.Fatal("settings-only copy touched the protected driver store")
	}
}

func TestCreateAllPreflightsExistingFilesAndBatchNameCollisions(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(fmt.Sprintf("existing=%t", existing), func(t *testing.T) {
			env := newBatchEnvironment(t, "Front Desk", "front/desk", "Warehouse")
			dir := t.TempDir()
			existingPath := filepath.Join(dir, "FRONT-DESK.ssb")
			if existing {
				if err := os.WriteFile(existingPath, []byte("keep this file"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			var probes int
			result, err := CreateAll(context.Background(), env, func(ctx context.Context, address string) (probe.Result, error) {
				probes++
				return batchCollect(ctx, address)
			}, AllOptions{OutputDir: dir})
			if err != nil {
				t.Fatal(err)
			}
			wantWritten := 2
			wantLookups := []string{"Front Desk", "Warehouse"}
			if existing {
				wantWritten, wantLookups = 1, []string{"Warehouse"}
				data, err := os.ReadFile(existingPath)
				if err != nil || string(data) != "keep this file" {
					t.Fatalf("existing file changed: %q, %v", data, err)
				}
			}
			if result.Written != wantWritten || result.Failed != 3-wantWritten || probes != wantWritten || !slices.Equal(env.lookups, wantLookups) {
				t.Fatalf("result = %+v; probes = %d; lookups = %v", result, probes, env.lookups)
			}
			if result.Queues[1].Status != "error" || !strings.Contains(result.Queues[1].Reason, "already used") {
				t.Fatalf("collision not explained: %+v", result.Queues[1])
			}
		})
	}
}

func TestCreateAllDriverPayloadRequiresOptInAndElevation(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	for _, elevated := range []bool{false, true} {
		t.Run(fmt.Sprintf("elevated=%t", elevated), func(t *testing.T) {
			env := newBatchEnvironment(t, "Office")
			env.elevated = elevated
			result, err := CreateAll(context.Background(), env, batchCollect, AllOptions{OutputDir: t.TempDir(), IncludeDriver: true})
			if err != nil {
				t.Fatal(err)
			}
			if !elevated {
				if result.Failed != 1 || len(env.exports) != 0 || len(env.lookups) != 0 || !strings.Contains(result.Queues[0].Reason, "administrator") {
					t.Fatalf("unelevated result = %+v; lookups = %v; exports = %v", result, env.lookups, env.exports)
				}
				return
			}
			if result.Written != 1 || !slices.Equal(env.exports, []string{"Driver for Office"}) {
				t.Fatalf("result = %+v; exports = %v", result, env.exports)
			}
			opened, err := Open(result.Queues[0].Bundle)
			if err != nil {
				t.Fatal(err)
			}
			defer opened.Close()
			if err := opened.Verify(); err != nil || opened.Manifest.Driver == nil || len(opened.Manifest.Driver.Files) != 1 {
				t.Fatalf("driver payload = %+v; verify = %v", opened.Manifest.Driver, err)
			}
		})
	}
}

func TestCreateAllCancellationKeepsCompletedAndAccountsForUnstartedQueues(t *testing.T) {
	env := newBatchEnvironment(t, "Office", "Warehouse", "Lobby")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result, err := CreateAll(ctx, env, batchCollect, AllOptions{
		OutputDir: t.TempDir(),
		Progress: func(_ string, step string) {
			if step == "Checking the file can be read back..." {
				cancel()
			}
		},
	})
	if !errors.Is(err, context.Canceled) || result.Requested != 3 || result.Written != 1 || result.Failed != 2 || len(result.Queues) != 3 {
		t.Fatalf("result = %+v; error = %v", result, err)
	}
	if !slices.Equal(env.lookups, []string{"Office"}) || result.Queues[0].Status != "written" {
		t.Fatalf("unexpected work after cancellation: %+v; lookups = %v", result, env.lookups)
	}
	for _, outcome := range result.Queues[1:] {
		if outcome.Status != "error" || !strings.Contains(outcome.Reason, "not copied: context canceled") {
			t.Fatalf("unstarted queue = %+v", outcome)
		}
	}
}

func TestCreateAllCancellationBeforeWriteDoesNotWriteOrStartNextQueue(t *testing.T) {
	env := newBatchEnvironment(t, "Office", "Warehouse")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dir := t.TempDir()
	result, err := CreateAll(ctx, env, batchCollect, AllOptions{
		OutputDir: dir,
		Progress: func(_ string, step string) {
			if step == "Writing the file..." {
				cancel()
			}
		},
	})
	if !errors.Is(err, context.Canceled) || result.Written != 0 || result.Failed != 2 || len(result.Queues) != 2 || !slices.Equal(env.lookups, []string{"Office"}) {
		t.Fatalf("result = %+v; error = %v; lookups = %v", result, err, env.lookups)
	}
	files, err := os.ReadDir(dir)
	if err != nil || len(files) != 0 {
		t.Fatalf("canceled copy wrote files: %v, %v", files, err)
	}
}

func TestCreateAllAlreadyCanceledDoesNoWork(t *testing.T) {
	env := newBatchEnvironment(t, "Office")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	dir := filepath.Join(t.TempDir(), "uncreated")
	result, err := CreateAll(ctx, env, batchCollect, AllOptions{OutputDir: dir})
	if !errors.Is(err, context.Canceled) || env.listed != 0 || len(env.lookups) != 0 || result.Written != 0 {
		t.Fatalf("result = %+v; error = %v; inventory reads = %d", result, err, env.listed)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("canceled copy created its output directory: %v", err)
	}
}
