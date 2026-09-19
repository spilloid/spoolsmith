package profileset

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/spilloid/spoolsmith/internal/install"
)

func writeCollection(t *testing.T, path string, collection Collection) {
	t.Helper()
	data, err := json.Marshal(collection)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestImportPreviewDoesNotCreateDestinationAndRetainsReviewedProfiles(t *testing.T) {
	source := filepath.Join(t.TempDir(), "all.json")
	original := Collection{Version: 1, Profiles: []Entry{{"office.json", sample()}}}
	writeCollection(t, source, original)
	dest := filepath.Join(t.TempDir(), "not-created")
	transfer, err := PrepareImport(source, dest)
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
	// Source replacement and modification of the returned summary cannot change
	// the profile that the technician already reviewed.
	original.Profiles[0].Profile.PrinterName = "Unreviewed replacement"
	writeCollection(t, source, original)
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
	got, err := install.LoadProfile(filepath.Join(newDest, "office.json"))
	if err != nil || !reflect.DeepEqual(got, sample()) {
		t.Fatalf("did not import reviewed profile: %#v %v", got, err)
	}
}

func TestPreviewListsEveryConflictAndExecutionRechecksDestination(t *testing.T) {
	source := filepath.Join(t.TempDir(), "all.json")
	writeCollection(t, source, Collection{Version: 1, Profiles: []Entry{{"office.json", sample()}, {"second.json", sample()}, {"third.json", sample()}}})
	dest := t.TempDir()
	for _, name := range []string{"OFFICE.json", "Second.json"} {
		if err := os.WriteFile(filepath.Join(dest, name), []byte("keep me"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	transfer, err := PrepareImport(source, dest)
	if err != nil {
		t.Fatal(err)
	}
	if got := transfer.Preview().Conflicts; !reflect.DeepEqual(got, []string{"office.json", "second.json"}) {
		t.Fatalf("missing conflicts: %v", got)
	}
	if _, err := transfer.Execute(); err == nil || !strings.Contains(err.Error(), "office.json, second.json") {
		t.Fatalf("expected full collision error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "third.json")); !os.IsNotExist(err) {
		t.Fatalf("partial import: %v", err)
	}
	clean := t.TempDir()
	transfer, err = transfer.WithDestination(clean)
	if err != nil || len(transfer.Preview().Conflicts) != 0 {
		t.Fatalf("new destination: %v", err)
	}
	if err := os.Mkdir(filepath.Join(clean, "SECOND.json"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := transfer.Execute(); err == nil {
		t.Fatal("ignored collision introduced after preview")
	}
	entries, err := os.ReadDir(clean)
	if err != nil || len(entries) != 1 || entries[0].Name() != "SECOND.json" {
		t.Fatalf("changed destination despite conflict: %v %v", entries, err)
	}
}

func TestExportPreviewRetainsSourceAndRechecksOutput(t *testing.T) {
	source := t.TempDir()
	profile := filepath.Join(source, "office.json")
	if err := install.SaveProfile(profile, sample()); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "all.json")
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
	if err := os.WriteFile(profile, []byte("broken source now"), 0600); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(t.TempDir(), "reviewed.json")
	transfer, err = transfer.WithDestination(other)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := transfer.Execute(); err != nil {
		t.Fatal(err)
	}
	got, err := Load(other)
	if err != nil || !reflect.DeepEqual(got.Profiles[0].Profile, sample()) {
		t.Fatalf("did not export reviewed source: %#v %v", got, err)
	}
}

func TestEmptyExportHasActionableError(t *testing.T) {
	_, err := PrepareExport(t.TempDir(), filepath.Join(t.TempDir(), "all.json"))
	if err == nil || !strings.Contains(err.Error(), "no JSON profiles") || !strings.Contains(err.Error(), "save a printer") {
		t.Fatalf("unhelpful empty folder error: %v", err)
	}
}
