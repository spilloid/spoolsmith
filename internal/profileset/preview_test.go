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
	collection := filepath.Join(t.TempDir(), "all.zip")
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
	// Modifying the returned summary after review cannot change what the
	// technician reviewed; replacing the set with different content makes
	// Execute refuse rather than import something nobody reviewed.
	preview.Profiles[0].PrinterName = "Mutated preview"
	newDest := filepath.Join(t.TempDir(), "chosen-folder")
	transfer, err = transfer.WithDestination(newDest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(newDest); !os.IsNotExist(err) {
		t.Fatalf("destination review wrote files: %v", err)
	}
	reviewed, err := os.ReadFile(collection)
	if err != nil {
		t.Fatal(err)
	}
	other := sample()
	other.Target = "192.0.2.99"
	otherSource := t.TempDir()
	writeSaved(t, otherSource, "office.ssb", other)
	if err := os.Remove(collection); err != nil {
		t.Fatal(err)
	}
	if _, err := Export(otherSource, collection); err != nil {
		t.Fatal(err)
	}
	if _, err := transfer.Execute(); err == nil || !strings.Contains(err.Error(), "changed after it was reviewed") {
		t.Fatalf("imported a set that changed after review: %v", err)
	}
	if entries, _ := os.ReadDir(newDest); len(entries) != 0 {
		t.Fatalf("refused import left files behind: %v", entries)
	}
	if err := os.WriteFile(collection, reviewed, 0600); err != nil {
		t.Fatal(err)
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
	collection := filepath.Join(t.TempDir(), "all.zip")
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
	output := filepath.Join(t.TempDir(), "all.zip")
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
	// Changing the source after review must not change what gets exported:
	// Execute refuses a file whose bytes differ from the reviewed ones.
	reviewed, err := os.ReadFile(profile)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(profile, []byte("broken source now"), 0600); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(t.TempDir(), "reviewed.zip")
	transfer, err = transfer.WithDestination(other)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := transfer.Execute(); err == nil || !strings.Contains(err.Error(), "changed after it was reviewed") {
		t.Fatalf("exported a file that changed after review: %v", err)
	}
	if _, err := os.Stat(other); !os.IsNotExist(err) {
		t.Fatalf("refused export left a set behind: %v", err)
	}
	if err := os.WriteFile(profile, reviewed, 0600); err != nil {
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
	_, err := PrepareExport(t.TempDir(), filepath.Join(t.TempDir(), "all.zip"))
	if err == nil || !strings.Contains(err.Error(), "no saved printers") || !strings.Contains(err.Error(), "save a printer") {
		t.Fatalf("unhelpful empty folder error: %v", err)
	}
}
