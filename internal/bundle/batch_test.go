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
	exportErr     error
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
	if e.exportErr != nil {
		return install.DriverExport{}, e.exportErr
	}
	if err := os.WriteFile(filepath.Join(dest, "printer.inf"), []byte("[Version]\n"), 0600); err != nil {
		return install.DriverExport{}, err
	}
	return install.DriverExport{PublishedName: "oem15.inf", OriginalName: "printer.inf"}, nil
}

func batchCollect(_ context.Context, address string) (probe.Result, error) {
	return probe.Result{Evidence: evidence.Evidence{IP: address, Provenance: "captured", HTTPTitle: "Office printer"}}, nil
}

// isolateTemp points every temp-directory lookup at a fresh directory, so a
// test can prove the batch left nothing behind.
func isolateTemp(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)
	t.Setenv("TMP", dir)
	t.Setenv("TEMP", dir)
	return dir
}

// setMemberManifest extracts one member of a set and returns its manifest.
func setMemberManifest(t *testing.T, set *Set, name string) Manifest {
	t.Helper()
	path, err := set.Extract(name, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	opened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	return opened.Manifest
}

func TestCreateAllWritesOneSetWithDriversAndMixedOutcomes(t *testing.T) {
	temp := isolateTemp(t)
	env := newBatchEnvironment(t, "Office", "PDF", "Gone", "Warehouse")
	env.queues[1].HostAddress = ""
	env.lookupErrors["Gone"] = errors.New("queue was removed")
	var probed, progressed []string
	setPath := filepath.Join(t.TempDir(), "printers.zip")
	result, err := CreateAll(context.Background(), env, func(ctx context.Context, address string) (probe.Result, error) {
		probed = append(probed, address)
		return batchCollect(ctx, address)
	}, AllOptions{
		SetPath: setPath, Note: "Site move", CreatedBy: "test build", SourceHost: "DESK-01",
		Progress: func(name, _ string) { progressed = append(progressed, name) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.SetPath != setPath || result.Requested != 4 || result.Written != 2 || result.Skipped != 1 || result.Failed != 1 || len(result.Queues) != 4 {
		t.Fatalf("result = %+v", result)
	}
	if !slices.Equal(env.lookups, []string{"Office", "Gone", "Warehouse"}) || !slices.Equal(probed, []string{"192.0.2.10", "192.0.2.13"}) {
		t.Fatalf("lookups = %v; probes = %v", env.lookups, probed)
	}
	if result.Queues[1].Status != "skipped" || !strings.Contains(result.Queues[1].Reason, "no printer host address") || result.Queues[2].Status != "error" || !strings.Contains(result.Queues[2].Reason, "queue was removed") {
		t.Fatalf("missing failure reasons: %+v", result.Queues)
	}

	set, err := OpenSet(setPath)
	if err != nil {
		t.Fatal(err)
	}
	defer set.Close()
	if set.Note != "Site move" || !slices.Equal(set.Members, []string{"Office.ssb", "Warehouse.ssb"}) {
		t.Fatalf("set = note %q members %v", set.Note, set.Members)
	}
	for _, index := range []int{0, 3} {
		outcome := result.Queues[index]
		queue := env.queues[index]
		if outcome.Status != "written" || !outcome.DriverIncluded || outcome.Reason != "" {
			t.Fatalf("queue %q outcome = %+v", queue.PrinterName, outcome)
		}
		manifest := setMemberManifest(t, set, outcome.Member)
		if manifest.Profile.PrinterName != queue.PrinterName || manifest.Profile.Target != queue.HostAddress || manifest.Profile.DriverName != queue.DriverName {
			t.Fatalf("queue %q has the wrong member: %+v", queue.PrinterName, manifest)
		}
		if manifest.Note != "Site move" || manifest.CreatedBy != "test build" || manifest.SourceHost != "DESK-01" {
			t.Fatalf("manifest options were not preserved: %+v", manifest)
		}
		if manifest.Driver == nil || manifest.Driver.WindowsDriverName != queue.DriverName {
			t.Fatalf("driver was not embedded by default: %+v", manifest.Driver)
		}
		if !slices.Contains(progressed, queue.PrinterName) {
			t.Fatalf("no progress for %q: %v", queue.PrinterName, progressed)
		}
	}
	if !slices.Equal(env.exports, []string{"Driver for Office", "Driver for Warehouse"}) {
		t.Fatalf("exports = %v", env.exports)
	}
	leftovers, _ := filepath.Glob(filepath.Join(temp, "spoolsmith-*"))
	if len(leftovers) != 0 {
		t.Fatalf("batch left working files behind: %v", leftovers)
	}
}

// TestCreateAllReportsDegradedWritesAlongsideConfirmedOnes checks that one
// unreachable printer in a batch still yields a written, applyable member for
// that queue -- clearly flagged -- while the rest of the batch is unaffected.
func TestCreateAllReportsDegradedWritesAlongsideConfirmedOnes(t *testing.T) {
	isolateTemp(t)
	env := newBatchEnvironment(t, "Office", "Front Desk")
	setPath := filepath.Join(t.TempDir(), "printers.zip")
	result, err := CreateAll(context.Background(), env, func(ctx context.Context, address string) (probe.Result, error) {
		if address == env.queues[1].HostAddress {
			return probe.Result{}, errors.New("connection refused")
		}
		return batchCollect(ctx, address)
	}, AllOptions{SetPath: setPath})
	if err != nil {
		t.Fatal(err)
	}
	if result.Written != 2 || result.Failed != 0 || result.Skipped != 0 {
		t.Fatalf("result = %+v", result)
	}
	if result.Queues[0].Reason != "" {
		t.Fatalf("confirmed queue unexpectedly flagged: %+v", result.Queues[0])
	}
	if result.Queues[1].Status != "written" || !strings.Contains(result.Queues[1].Reason, "identity was not confirmed") {
		t.Fatalf("degraded queue not reported: %+v", result.Queues[1])
	}
	set, err := OpenSet(setPath)
	if err != nil {
		t.Fatal(err)
	}
	defer set.Close()
	if got := setMemberManifest(t, set, result.Queues[1].Member).Profile.Evidence.Provenance; got != "unconfirmed" {
		t.Fatalf("member Provenance = %q", got)
	}
}

func TestCreateAllSuffixesCollidingMemberNames(t *testing.T) {
	isolateTemp(t)
	env := newBatchEnvironment(t, "Front Desk", "front/desk", "FRONT DESK", "CON")
	setPath := filepath.Join(t.TempDir(), "printers.zip")
	result, err := CreateAll(context.Background(), env, batchCollect, AllOptions{SetPath: setPath})
	if err != nil {
		t.Fatal(err)
	}
	var members []string
	for _, outcome := range result.Queues {
		members = append(members, outcome.Member)
	}
	want := []string{"Front-Desk.ssb", "front-desk-2.ssb", "FRONT-DESK-3.ssb", "printer-CON.ssb"}
	if result.Written != 4 || !slices.Equal(members, want) {
		t.Fatalf("result = %+v; members = %v", result, members)
	}
	set, err := OpenSet(setPath)
	if err != nil {
		t.Fatal(err)
	}
	defer set.Close()
	if !slices.Equal(set.Members, want) {
		t.Fatalf("set members = %v", set.Members)
	}
}

func TestCreateAllRefusesBadOrExistingSetPathUpFront(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "Printers.ZIP")
	if err := os.WriteFile(existing, []byte("keep this file"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"", filepath.Join(dir, "printers"), filepath.Join(dir, "printers.ssb"), existing} {
		env := newBatchEnvironment(t, "Office")
		_, err := CreateAll(context.Background(), env, batchCollect, AllOptions{SetPath: path})
		if err == nil {
			t.Fatalf("CreateAll(%q) accepted a bad destination", path)
		}
		if env.listed != 0 || len(env.lookups) != 0 {
			t.Fatalf("CreateAll(%q) did work before refusing", path)
		}
	}
	data, err := os.ReadFile(existing)
	if err != nil || string(data) != "keep this file" {
		t.Fatalf("existing file changed: %q, %v", data, err)
	}
	// The extension check is case-insensitive in the accepting direction too.
	env := newBatchEnvironment(t, "Office")
	if _, err := CreateAll(context.Background(), env, batchCollect, AllOptions{SetPath: filepath.Join(dir, "new.ZIP")}); err != nil {
		t.Fatalf("refused an upper-case .ZIP: %v", err)
	}
}

func TestCreateAllFallsBackToSettingsOnlyWhenNotElevated(t *testing.T) {
	isolateTemp(t)
	env := newBatchEnvironment(t, "Office")
	env.elevated = false
	setPath := filepath.Join(t.TempDir(), "printers.zip")
	result, err := CreateAll(context.Background(), env, batchCollect, AllOptions{SetPath: setPath})
	if err != nil {
		t.Fatal(err)
	}
	outcome := result.Queues[0]
	if result.Written != 1 || outcome.DriverIncluded || !strings.Contains(outcome.Reason, "administrator") || !strings.Contains(outcome.Reason, "Driver for Office") || len(env.exports) != 0 {
		t.Fatalf("result = %+v; exports = %v", result, env.exports)
	}
	set, err := OpenSet(setPath)
	if err != nil {
		t.Fatal(err)
	}
	defer set.Close()
	if setMemberManifest(t, set, outcome.Member).Driver != nil {
		t.Fatal("unelevated copy carried a driver payload")
	}
}

func TestCreateAllSettingsOnlyNeverTouchesTheDriverStore(t *testing.T) {
	isolateTemp(t)
	env := newBatchEnvironment(t, "Office")
	result, err := CreateAll(context.Background(), env, batchCollect, AllOptions{SetPath: filepath.Join(t.TempDir(), "printers.zip"), SettingsOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.Written != 1 || result.Queues[0].DriverIncluded || env.elevationRead != 0 || len(env.exports) != 0 {
		t.Fatalf("result = %+v; elevation reads = %d; exports = %v", result, env.elevationRead, env.exports)
	}
}

func TestCreateAllWritesNothingWhenEveryQueueIsSkipped(t *testing.T) {
	isolateTemp(t)
	env := newBatchEnvironment(t, "PDF", "Fax")
	for i := range env.queues {
		env.queues[i].HostAddress = ""
	}
	setPath := filepath.Join(t.TempDir(), "printers.zip")
	result, err := CreateAll(context.Background(), env, batchCollect, AllOptions{SetPath: setPath})
	if err != nil {
		t.Fatal(err)
	}
	if result.Written != 0 || result.Skipped != 2 || result.SetPath != "" {
		t.Fatalf("result = %+v", result)
	}
	if _, err := os.Stat(setPath); !os.IsNotExist(err) {
		t.Fatalf("wrote a set with no members: %v", err)
	}
}

func TestCreateAllCancellationWritesNoSetAndAccountsForEveryQueue(t *testing.T) {
	isolateTemp(t)
	env := newBatchEnvironment(t, "Office", "Warehouse", "Lobby")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	setPath := filepath.Join(t.TempDir(), "printers.zip")
	result, err := CreateAll(ctx, env, batchCollect, AllOptions{
		SetPath: setPath,
		Progress: func(_ string, step string) {
			if step == "Checking the file can be read back..." {
				cancel()
			}
		},
	})
	if !errors.Is(err, context.Canceled) || result.Requested != 3 || result.Written != 0 || result.Failed != 3 || len(result.Queues) != 3 || result.SetPath != "" {
		t.Fatalf("result = %+v; error = %v", result, err)
	}
	if !slices.Equal(env.lookups, []string{"Office"}) {
		t.Fatalf("unexpected work after cancellation: lookups = %v", env.lookups)
	}
	if result.Queues[0].Status != "error" || !strings.Contains(result.Queues[0].Reason, "not saved") {
		t.Fatalf("copied-but-unsaved queue = %+v", result.Queues[0])
	}
	for _, outcome := range result.Queues[1:] {
		if outcome.Status != "error" || !strings.Contains(outcome.Reason, "not copied: context canceled") {
			t.Fatalf("unstarted queue = %+v", outcome)
		}
	}
	if _, err := os.Stat(setPath); !os.IsNotExist(err) {
		t.Fatalf("canceled copy wrote a set: %v", err)
	}
}

func TestCreateAllAlreadyCanceledDoesNoWork(t *testing.T) {
	env := newBatchEnvironment(t, "Office")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	setPath := filepath.Join(t.TempDir(), "printers.zip")
	result, err := CreateAll(ctx, env, batchCollect, AllOptions{SetPath: setPath})
	if !errors.Is(err, context.Canceled) || env.listed != 0 || len(env.lookups) != 0 || result.Written != 0 {
		t.Fatalf("result = %+v; error = %v; inventory reads = %d", result, err, env.listed)
	}
	if _, err := os.Stat(setPath); !os.IsNotExist(err) {
		t.Fatalf("canceled copy created its set: %v", err)
	}
}
