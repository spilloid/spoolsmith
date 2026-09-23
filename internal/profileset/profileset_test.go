package profileset

import (
	"archive/zip"
	"bytes"
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
	output := filepath.Join(t.TempDir(), "all.zip")
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

// TestEmbeddedDriverTravelsVerbatim: drivers are preferred, so a saved
// printer carrying its own embedded driver is exported and imported
// byte-for-byte, and the review says it carries one.
func TestEmbeddedDriverTravelsVerbatim(t *testing.T) {
	source := t.TempDir()
	payload := t.TempDir()
	if err := os.WriteFile(filepath.Join(payload, "driver.inf"), []byte("[Version]\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(payload, "driver.dll"), make([]byte, 2<<20), 0600); err != nil {
		t.Fatal(err)
	}
	p := sample()
	p.DriverPackage = nil
	manifest := bundle.Manifest{Profile: p, Driver: &bundle.DriverPayload{WindowsDriverName: p.DriverName, INF: "driver.inf"}}
	original := filepath.Join(source, "office.ssb")
	if err := bundle.Write(original, manifest, payload); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "all.zip")
	transfer, err := PrepareExport(source, output)
	if err != nil {
		t.Fatal(err)
	}
	if preview := transfer.Preview(); !preview.Profiles[0].Driver {
		t.Fatalf("preview does not show the embedded driver: %#v", preview.Profiles[0])
	}
	if _, err := transfer.Execute(); err != nil {
		t.Fatal(err)
	}
	dest := t.TempDir()
	imported, err := PrepareImport(output, dest)
	if err != nil {
		t.Fatal(err)
	}
	if !imported.Preview().Profiles[0].Driver {
		t.Fatal("import preview does not show the embedded driver")
	}
	if _, err := imported.Execute(); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(readSaved(t, original), readSaved(t, filepath.Join(dest, "office.ssb"))) {
		t.Fatal("printer file with an embedded driver changed on the way through the set")
	}
}

func TestExportRequiresAZipDestination(t *testing.T) {
	source := t.TempDir()
	writeSaved(t, source, "office.ssb", sample())
	output := filepath.Join(t.TempDir(), "all.ssb")
	if _, err := PrepareExport(source, output); err == nil || !strings.Contains(err.Error(), ".zip") {
		t.Fatalf("accepted a non-.zip export destination: %v", err)
	}
}

func TestInvalidCollectionNeverPartiallyImports(t *testing.T) {
	office := readSaved(t, writeSaved(t, t.TempDir(), "office.ssb", sample()))
	for _, bad := range []string{"../outside.ssb", `..\outside.ssb`, "C:bad.ssb", "CON.ssb", "bad\n.ssb", "OFFICE.ssb", "notes.txt"} {
		t.Run(bad, func(t *testing.T) {
			// A hand-built zip with an unsafe or colliding member name never
			// comes from PrepareExport (it only ever lists real files it just
			// read); this models a hand-edited or corrupted set.
			path := filepath.Join(t.TempDir(), "all.zip")
			f, err := os.Create(path)
			if err != nil {
				t.Fatal(err)
			}
			zw := zip.NewWriter(f)
			for _, name := range []string{"office.ssb", bad} {
				w, err := zw.Create(name)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := w.Write(office); err != nil {
					t.Fatal(err)
				}
			}
			if err := zw.Close(); err != nil {
				t.Fatal(err)
			}
			f.Close()
			dest := t.TempDir()
			if _, err := Import(path, dest); err == nil {
				t.Fatal("accepted unsafe/duplicate filename")
			}
			entries, _ := os.ReadDir(dest)
			if len(entries) != 0 {
				t.Fatal("partial import")
			}
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
	path := filepath.Join(t.TempDir(), "all.zip")
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
	output := filepath.Join(source, "all.zip")
	if _, err := Export(source, output); err == nil || !strings.Contains(err.Error(), "outside") {
		t.Fatalf("expected actionable folder error, got %v", err)
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatalf("collection was created: %v", err)
	}
	if n, err := Export(source, filepath.Join(t.TempDir(), "all.zip")); err != nil || n != 1 {
		t.Fatalf("source must remain exportable: %d %v", n, err)
	}
}

func TestExportRejectsSourceDirectoryAlias(t *testing.T) {
	source := t.TempDir()
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(source, alias); err != nil {
		t.Skipf("directory symlinks unavailable: %v", err)
	}
	// A real printer to export, so the refusal is about the destination alias
	// rather than an empty source.
	writeSaved(t, source, "office.ssb", sample())
	if _, err := Export(source, filepath.Join(alias, "all.zip")); err == nil || !strings.Contains(err.Error(), "outside") {
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
