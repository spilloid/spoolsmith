package bundle

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spilloid/spoolsmith/internal/install"
	"github.com/spilloid/spoolsmith/internal/probe"
)

// TestCreatePreflightsExistingDestination guards against a single copy paying
// for a network probe (and, by default, an elevated driver
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

func TestCreateUSBFileCarriesDriverWithoutNetworkProbe(t *testing.T) {
	isolateTemp(t)
	env := newBatchEnvironment(t, "Front Desk")
	env.queues[0].PortName = "USB001"
	env.queues[0].HostAddress = ""
	env.queues[0].PortNumber = 0
	env.queues[0].Protocol = 0
	path := filepath.Join(t.TempDir(), FileNameForQueue(env.queues[0]))
	probes := 0
	result, err := Create(context.Background(), env, func(context.Context, string) (probe.Result, error) {
		probes++
		return probe.Result{}, errors.New("network should not be probed")
	}, CreateOptions{QueueName: "Front Desk", Path: path})
	if err != nil {
		t.Fatal(err)
	}
	if probes != 0 || result.Manifest.Profile.PortType != "usb" || result.Manifest.Profile.SourcePort != "USB001" || result.Manifest.Driver == nil {
		t.Fatalf("USB copy = %+v; probes = %d", result, probes)
	}
	if filepath.Base(path) != "Front-Desk.usb.ssb" {
		t.Fatalf("USB filename = %q", path)
	}
	opened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	if err := opened.Verify(); err != nil {
		t.Fatal(err)
	}
}

func TestCreateHostnamePortWarnsAndSavesResolvedIP(t *testing.T) {
	env := newBatchEnvironment(t, "Front Desk")
	env.queues[0].HostAddress = "localhost"
	path := filepath.Join(t.TempDir(), "front-desk.ssb")
	result, err := Create(context.Background(), env, batchCollect, CreateOptions{QueueName: "Front Desk", Path: path, SettingsOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.Manifest.Profile.Target == "localhost" || !strings.Contains(result.Warning, "localhost") || !strings.Contains(result.Manifest.Profile.Evidence.ProvenanceNote, "localhost") {
		t.Fatalf("hostname copy = %+v", result)
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

// TestCreatePrefersTheDriverAndFallsBackToSettingsOnly pins the operator's
// direction: the driver rides along whenever it can, and the copy only
// degrades to settings-only -- still succeeding, and saying why -- when it
// cannot.
func TestCreatePrefersTheDriverAndFallsBackToSettingsOnly(t *testing.T) {
	isolateTemp(t)
	for _, tc := range []struct {
		name         string
		elevated     bool
		exportErr    error
		settingsOnly bool
		wantDriver   bool
		wantReason   string
		wantExports  int
	}{
		{name: "elevated", elevated: true, wantDriver: true, wantExports: 1},
		{name: "not elevated", elevated: false, wantReason: "administrator rights", wantExports: 0},
		{name: "export fails", elevated: true, exportErr: errors.New("pnputil exploded"), wantReason: "pnputil exploded", wantExports: 1},
		{name: "settings only", elevated: true, settingsOnly: true, wantReason: "settings only", wantExports: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := newBatchEnvironment(t, "Front Desk")
			env.elevated, env.exportErr = tc.elevated, tc.exportErr
			path := filepath.Join(t.TempDir(), "front-desk.ssb")
			result, err := Create(context.Background(), env, batchCollect, CreateOptions{QueueName: "Front Desk", Path: path, SettingsOnly: tc.settingsOnly})
			if err != nil {
				t.Fatalf("Create() = %v, want success", err)
			}
			if (result.Manifest.Driver != nil) != tc.wantDriver || len(env.exports) != tc.wantExports {
				t.Fatalf("driver = %+v; exports = %v", result.Manifest.Driver, env.exports)
			}
			if tc.wantDriver {
				if result.DriverNotIncluded != "" {
					t.Fatalf("DriverNotIncluded = %q with the driver embedded", result.DriverNotIncluded)
				}
			} else if !strings.Contains(result.DriverNotIncluded, tc.wantReason) || !strings.Contains(result.DriverNotIncluded, `"Driver for Front Desk"`) {
				t.Fatalf("DriverNotIncluded = %q, want it to mention %q and the driver name", result.DriverNotIncluded, tc.wantReason)
			}
			if tc.settingsOnly && env.elevationRead != 0 {
				t.Fatal("settings-only copy checked elevation")
			}
			opened, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer opened.Close()
			if err := opened.Verify(); err != nil || (opened.Manifest.Driver != nil) != tc.wantDriver {
				t.Fatalf("written bundle driver = %+v; verify = %v", opened.Manifest.Driver, err)
			}
		})
	}
}

// TestCreateRemovesItsDriverExport guards against a single-queue copy leaving
// its spoolsmith-export-* working directory (up to a bundle's worth of driver
// files) in the temp folder: the payload lives inside the bundle once it is
// written, so nothing outside it is worth keeping, on success or failure.
func TestCreateRemovesItsDriverExport(t *testing.T) {
	for _, tc := range []struct {
		name    string
		prepare func(env *batchEnvironment, path string)
		wantErr bool
	}{
		{name: "written"},
		{name: "write fails", wantErr: true, prepare: func(_ *batchEnvironment, path string) {
			// Another file takes the name after the preflight, so both the
			// driver write and the settings-only fallback fail.
			if err := os.WriteFile(path, []byte("raced"), 0600); err != nil {
				panic(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			temp := isolateTemp(t)
			env := newBatchEnvironment(t, "Front Desk")
			path := filepath.Join(t.TempDir(), "front-desk.ssb")
			collect := batchCollect
			if tc.prepare != nil {
				collect = func(ctx context.Context, address string) (probe.Result, error) {
					tc.prepare(env, path)
					return batchCollect(ctx, address)
				}
			}
			result, err := Create(context.Background(), env, collect, CreateOptions{QueueName: "Front Desk", Path: path})
			if (err != nil) != tc.wantErr {
				t.Fatalf("Create() = %v, wantErr %v", err, tc.wantErr)
			}
			if !tc.wantErr && result.Manifest.Driver == nil {
				t.Fatal("driver was not embedded; the test would not exercise an export")
			}
			if len(env.exports) != 1 {
				t.Fatalf("exports = %v, want one", env.exports)
			}
			entries, err := os.ReadDir(temp)
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries {
				if strings.HasPrefix(entry.Name(), "spoolsmith-export-") {
					t.Fatalf("Create() left %s behind in the temp folder", entry.Name())
				}
			}
		})
	}
}

func TestCopyWarningsKeepsEveryWarning(t *testing.T) {
	cloned := install.ClonedQueue{
		USB: true, DriverName: "Brother HL-L2315D series", HostAddress: "192.0.2.10",
		SourceHostname: "printer.example", SourceWSDPort: "WSD-1", SourceWSDDeviceID: "uuid:1",
		SourceDriverQueue: "Office RAW", SourceDriverName: "Microsoft IPP Class Driver",
	}
	warnings := copyWarnings(cloned)
	if len(warnings) != 3 || warnings[0].provenance || !warnings[1].provenance || !warnings[2].provenance {
		t.Fatalf("copyWarnings() = %+v, want USB, hostname and WSD warnings", warnings)
	}
	joined := joinWarnings(warnings)
	for _, want := range []string{"USB printer file", `hostname "printer.example"`, `WSD port "WSD-1"`, `RAW mapping "Office RAW"`} {
		if !strings.Contains(joined, want) {
			t.Errorf("joined warning lost %q: %s", want, joined)
		}
	}
	if !strings.Contains(joined, "review the mapping. Windows port uses hostname") {
		t.Fatalf("warnings are not joined as sentences: %s", joined)
	}
	if joinWarnings(nil) != "" {
		t.Fatal("no warnings must join to an empty Warning")
	}
}
