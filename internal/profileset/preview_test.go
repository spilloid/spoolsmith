package profileset

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/spilloid/spoolsmith/internal/bundle"
)

func TestImportPreviewDoesNotCreateDestinationAndRetainsReviewedProfiles(t *testing.T) {
	source := t.TempDir()
	writeSaved(t, source, "office.ssb", sample())
	collection := filepath.Join(t.TempDir(), "all.ssb")
	if _, err := Export(source, collection); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "not-created")
	transfer, err := PrepareImport(collection, dest)
	if err != nil {
		t.Fatal(err)
	}
	preview := transfer.Preview()
	if preview.Count != 1 || preview.Profiles[0].PrinterName != sample().PrinterName || preview.Profiles[0].Archive != sample().DriverPackage.Archive {
		t.Fatalf("incomplete preview: %#v", preview)
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatalf("preview created destination: %v", err)
	}
	// Modifying the source collection or the returned summary after review
	// cannot change what the technician already reviewed and is about to import.
	if err := os.WriteFile(collection, []byte("corrupted after review"), 0600); err != nil {
		t.Fatal(err)
	}
	preview.Profiles[0].PrinterName = "Mutated preview"
	newDest := filepath.Join(t.TempDir(), "chosen-folder")
	transfer, err = transfer.WithDestination(newDest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(newDest); !os.IsNotExist(err) {
		t.Fatalf("destination review wrote files: %v", err)
	}
	if count, err := transfer.Execute(); err != nil || count != 1 {
		t.Fatalf("execute: %d %v", count, err)
	}
	got, err := bundle.LoadProfile(filepath.Join(newDest, "office.ssb"))
	if err != nil || !reflect.DeepEqual(got, sample()) {
		t.Fatalf("did not import reviewed profile: %#v %v", got, err)
	}
}

func TestPreviewListsEveryConflictAndExecutionRechecksDestination(t *testing.T) {
	source := t.TempDir()
	for _, name := range []string{"office.ssb", "second.ssb", "third.ssb"} {
		writeSaved(t, source, name, sample())
	}
	collection := filepath.Join(t.TempDir(), "all.ssb")
	if _, err := Export(source, collection); err != nil {
		t.Fatal(err)
	}
	dest := t.TempDir()
	for _, name := range []string{"OFFICE.ssb", "Second.ssb"} {
		if err := os.WriteFile(filepath.Join(dest, name), []byte("keep me"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	transfer, err := PrepareImport(collection, dest)
	if err != nil {
		t.Fatal(err)
	}
	if got := transfer.Preview().Conflicts; !reflect.DeepEqual(got, []string{"office.ssb", "second.ssb"}) {
		t.Fatalf("missing conflicts: %v", got)
	}
	if _, err := transfer.Execute(); err == nil || !strings.Contains(err.Error(), "office.ssb, second.ssb") {
		t.Fatalf("expected full collision error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "third.ssb")); !os.IsNotExist(err) {
		t.Fatalf("partial import: %v", err)
	}
	clean := t.TempDir()
	transfer, err = transfer.WithDestination(clean)
	if err != nil || len(transfer.Preview().Conflicts) != 0 {
		t.Fatalf("new destination: %v", err)
	}
	if err := os.Mkdir(filepath.Join(clean, "SECOND.ssb"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := transfer.Execute(); err == nil {
		t.Fatal("ignored collision introduced after preview")
	}
	entries, err := os.ReadDir(clean)
	if err != nil || len(entries) != 1 || entries[0].Name() != "SECOND.ssb" {
		t.Fatalf("changed destination despite conflict: %v %v", entries, err)
	}
}

func TestExportPreviewRetainsSourceAndRechecksOutput(t *testing.T) {
	source := t.TempDir()
	profile := writeSaved(t, source, "office.ssb", sample())
	output := filepath.Join(t.TempDir(), "all.ssb")
	transfer, err := PrepareExport(source, output)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatalf("preview created export: %v", err)
	}
	if err := os.WriteFile(output, []byte("keep me"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := transfer.Execute(); err == nil {
		t.Fatal("export replaced a file created after review")
	}
	// Corrupting the source after review must not change what gets exported:
	// the transfer already captured the reviewed bytes.
	if err := os.WriteFile(profile, []byte("broken source now"), 0600); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(t.TempDir(), "reviewed.ssb")
	transfer, err = transfer.WithDestination(other)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := transfer.Execute(); err != nil {
		t.Fatal(err)
	}
	imported := t.TempDir()
	if _, err := Import(other, imported); err != nil {
		t.Fatal(err)
	}
	got, err := bundle.LoadProfile(filepath.Join(imported, "office.ssb"))
	if err != nil || !reflect.DeepEqual(got, sample()) {
		t.Fatalf("did not export reviewed source: %#v %v", got, err)
	}
}

func TestEmptyExportHasActionableError(t *testing.T) {
	_, err := PrepareExport(t.TempDir(), filepath.Join(t.TempDir(), "all.ssb"))
	if err == nil || !strings.Contains(err.Error(), "no saved printers") || !strings.Contains(err.Error(), "save a printer") {
		t.Fatalf("unhelpful empty folder error: %v", err)
	}
}
