package profileset

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/spilloid/spoolsmith/internal/bundle"
	"github.com/spilloid/spoolsmith/internal/evidence"
	"github.com/spilloid/spoolsmith/internal/install"
)

func sample() install.Profile {
	return install.Profile{Version: 1, Target: "192.0.2.10", PrinterName: "Office", DriverName: "Brother HL-L2315D series", Evidence: evidence.Evidence{IP: "192.0.2.10", Provenance: "captured", HTTPTitle: "Brother HL-L2315D", PJLID: "Brother HL-L2315D"}, DriverPackage: &install.PackageSelection{ID: "brother-y14a-c1-hostm-1110", Archive: ".packages/brother/driver.EXE"}}
}

func TestTransferPreservesAllPropertiesAndNeverOverwrites(t *testing.T) {
	source, dest := t.TempDir(), t.TempDir()
	p := sample()
	for _, name := range []string{"office.ssb", "second.ssb"} {
		if err := bundle.SaveProfile(filepath.Join(source, name), p); err != nil {
			t.Fatal(err)
		}
	}
	output := filepath.Join(t.TempDir(), "all.ssb")
	if n, err := Export(source, output); err != nil || n != 2 {
		t.Fatalf("export %d %v", n, err)
	}
	original, _ := os.ReadFile(output)
	if _, err := Export(source, output); err == nil {
		t.Fatal("overwrote export")
	}
	after, _ := os.ReadFile(output)
	if string(after) != string(original) {
		t.Fatal("changed export")
	}
	if n, err := Import(output, dest); err != nil || n != 2 {
		t.Fatalf("import %d %v", n, err)
	}
	got, err := bundle.LoadProfile(filepath.Join(dest, "office.ssb"))
	if err != nil || !reflect.DeepEqual(got, p) {
		t.Fatalf("properties changed: %#v %v", got, err)
	}
	if _, err := Import(output, dest); err == nil {
		t.Fatal("overwrote profiles")
	}
}

// TestExportRejectsEmbeddedDriverPayload is new behavior this format merge
// requires: a saved-setup file can now carry an embedded driver payload
// (from `copy --include-driver`) the old bare-JSON format never could. Saved-
// setup transfer only ever carried settings, so it refuses one explicitly
// rather than silently dropping or silently ballooning the collection with
// megabytes of driver files.
func TestExportRejectsEmbeddedDriverPayload(t *testing.T) {
	source := t.TempDir()
	payload := filepath.Join(source, "payload")
	if err := os.MkdirAll(payload, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(payload, "driver.inf"), []byte("[Version]\n"), 0600); err != nil {
		t.Fatal(err)
	}
	p := sample()
	p.DriverPackage = nil
	manifest := bundle.Manifest{Profile: p, Driver: &bundle.DriverPayload{WindowsDriverName: p.DriverName, INF: "driver.inf"}}
	if err := bundle.Write(filepath.Join(source, "office.ssb"), manifest, payload); err != nil {
		t.Fatal(err)
	}
	if _, err := PrepareExport(source, filepath.Join(t.TempDir(), "all.ssb")); err == nil || !strings.Contains(err.Error(), "embedded driver payload") {
		t.Fatalf("accepted a member carrying a driver payload: %v", err)
	}
}

func TestInvalidCollectionNeverPartiallyImports(t *testing.T) {
	office := readSaved(t, writeSaved(t, t.TempDir(), "office.ssb", sample()))
	for _, bad := range []string{"../outside.ssb", `..\outside.ssb`, "C:bad.ssb", "CON.ssb", "bad\n.ssb", "OFFICE.ssb"} {
		t.Run(bad, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "all.ssb")
			// A hand-built set with an unsafe or colliding member name never
			// comes from PrepareExport (it only ever lists real files it just
			// read); this models a hand-edited or corrupted collection.
			if err := bundle.WriteSet(path, bundle.SetIndex{}, []bundle.SetMember{{Name: "office.ssb", Data: office}, {Name: bad, Data: office}}); err == nil {
				dest := t.TempDir()
				if _, err := Import(path, dest); err == nil {
					t.Fatal("accepted unsafe/duplicate filename")
				}
				entries, _ := os.ReadDir(dest)
				if len(entries) != 0 {
					t.Fatal("partial import")
				}
				return
			}
			// WriteSet already refused the unsafe name at the container level
			// -- equally acceptable, since nothing reaches Import either way.
		})
	}
}

func TestCollisionIsCheckedBeforeWritingAnyProfile(t *testing.T) {
	source := t.TempDir()
	for _, name := range []string{"first.ssb", "taken.ssb"} {
		if err := bundle.SaveProfile(filepath.Join(source, name), sample()); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(t.TempDir(), "all.ssb")
	if _, err := Export(source, path); err != nil {
		t.Fatal(err)
	}
	dest := t.TempDir()
	if err := os.WriteFile(filepath.Join(dest, "TAKEN.ssb"), []byte("keep me"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Import(path, dest); err == nil {
		t.Fatal("accepted case-insensitive collision")
	}
	entries, _ := os.ReadDir(dest)
	if len(entries) != 1 {
		t.Fatal("partial import")
	}
	kept, _ := os.ReadFile(filepath.Join(dest, "TAKEN.ssb"))
	if string(kept) != "keep me" {
		t.Fatal("changed existing file")
	}
}

func TestExportCannotPolluteSourceFolder(t *testing.T) {
	source := t.TempDir()
	if err := bundle.SaveProfile(filepath.Join(source, "office.ssb"), sample()); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(source, "all.ssb")
	if _, err := Export(source, output); err == nil || !strings.Contains(err.Error(), "outside") {
		t.Fatalf("expected actionable folder error, got %v", err)
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatalf("collection was created: %v", err)
	}
	if n, err := Export(source, filepath.Join(t.TempDir(), "all.ssb")); err != nil || n != 1 {
		t.Fatalf("source must remain exportable: %d %v", n, err)
	}
}

func TestExportRejectsSourceDirectoryAlias(t *testing.T) {
	source := t.TempDir()
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(source, alias); err != nil {
		t.Skipf("directory symlinks unavailable: %v", err)
	}
	if _, err := Export(source, filepath.Join(alias, "all.ssb")); err == nil || !strings.Contains(err.Error(), "outside") {
		t.Fatalf("expected source-directory alias to be rejected, got %v", err)
	}
}

// writeSaved saves a profile as a .ssb under dir/name and returns its path.
func writeSaved(t *testing.T, dir, name string, p install.Profile) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := bundle.SaveProfile(path, p); err != nil {
		t.Fatal(err)
	}
	return path
}

// readSaved returns a saved profile file's raw bytes, for building a
// hand-crafted set the same way PrepareExport would have, without going
// through it.
func readSaved(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
