package profileset

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/spilloid/spoolsmith/internal/evidence"
	"github.com/spilloid/spoolsmith/internal/install"
)

func sample() install.Profile {
	return install.Profile{Version: 1, Target: "192.0.2.10", PrinterName: "Office", DriverName: "Brother HL-L2315D series", Evidence: evidence.Evidence{IP: "192.0.2.10", Provenance: "captured", HTTPTitle: "Brother HL-L2315D", PJLID: "Brother HL-L2315D"}, DriverPackage: &install.PackageSelection{ID: "brother-y14a-c1-hostm-1110", Archive: ".packages/brother/driver.EXE"}}
}

func TestTransferPreservesAllPropertiesAndNeverOverwrites(t *testing.T) {
	source, dest := t.TempDir(), t.TempDir()
	p := sample()
	for _, name := range []string{"office.json", "second.json"} {
		if err := install.SaveProfile(filepath.Join(source, name), p); err != nil {
			t.Fatal(err)
		}
	}
	output := filepath.Join(t.TempDir(), "all.json")
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
	got, err := install.LoadProfile(filepath.Join(dest, "office.json"))
	if err != nil || !reflect.DeepEqual(got, p) {
		t.Fatalf("properties changed: %#v %v", got, err)
	}
	if _, err := Import(output, dest); err == nil {
		t.Fatal("overwrote profiles")
	}
}

func TestInvalidCollectionNeverPartiallyImports(t *testing.T) {
	for _, bad := range []string{"../outside.json", `..\outside.json`, "C:bad.json", "CON.json", "bad\n.json", "OFFICE.json"} {
		t.Run(bad, func(t *testing.T) {
			c := Collection{Version: 1, Profiles: []Entry{{"office.json", sample()}, {bad, sample()}}}
			data, _ := json.Marshal(c)
			path := filepath.Join(t.TempDir(), "all.json")
			os.WriteFile(path, data, 0600)
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

func TestLoadRejectsUnknownFieldsTrailingDataAndInvalidProfile(t *testing.T) {
	data, _ := json.Marshal(Collection{Version: 1, Profiles: []Entry{{"office.json", sample()}}})
	for _, bad := range []string{string(data) + " {}", strings.Replace(string(data), `"version":1`, `"version":2`, 1), strings.Replace(string(data), `"file":`, `"unknown":1,"file":`, 1), strings.Replace(string(data), `"captured"`, `"synthetic"`, 1)} {
		path := filepath.Join(t.TempDir(), "all.json")
		os.WriteFile(path, []byte(bad), 0600)
		if _, err := Load(path); err == nil {
			t.Fatalf("accepted %s", bad)
		}
	}
}

func TestCollisionIsCheckedBeforeWritingAnyProfile(t *testing.T) {
	c := Collection{Version: 1, Profiles: []Entry{{"first.json", sample()}, {"taken.json", sample()}}}
	data, _ := json.Marshal(c)
	path := filepath.Join(t.TempDir(), "all.json")
	os.WriteFile(path, data, 0600)
	dest := t.TempDir()
	os.WriteFile(filepath.Join(dest, "TAKEN.json"), []byte("keep me"), 0600)
	if _, err := Import(path, dest); err == nil {
		t.Fatal("accepted case-insensitive collision")
	}
	entries, _ := os.ReadDir(dest)
	if len(entries) != 1 {
		t.Fatal("partial import")
	}
	kept, _ := os.ReadFile(filepath.Join(dest, "TAKEN.json"))
	if string(kept) != "keep me" {
		t.Fatal("changed existing file")
	}
}
