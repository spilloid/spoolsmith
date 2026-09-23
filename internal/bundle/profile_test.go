package bundle

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spilloid/spoolsmith/internal/evidence"
	"github.com/spilloid/spoolsmith/internal/install"
)

func sampleProfile() install.Profile {
	return install.Profile{Version: 1, Target: "192.0.2.10", PrinterName: "Accounts printer", DriverName: "Exact OEM driver", Evidence: evidence.Evidence{IP: "192.0.2.10", Provenance: "captured", HTTPTitle: "Example Model 123", PJLID: "Example Model 123:firmware1"}}
}

// TestSaveLoadProfileRoundTripAndNeverOverwrite exercises the one on-disk
// profile format directly: a saved printer is a zero-payload bundle, not a
// bare JSON file -- see profile.go's doc comment for why there is no second
// format any more.
func TestSaveLoadProfileRoundTripAndNeverOverwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "printer.ssb")
	p := sampleProfile()
	if err := SaveProfile(path, p); err != nil {
		t.Fatal(err)
	}
	if err := SaveProfile(path, p); err == nil {
		t.Fatal("overwrote existing profile")
	}
	loaded, err := LoadProfile(path)
	if err != nil || loaded.PrinterName != p.PrinterName || loaded.Evidence.PJLID != p.Evidence.PJLID {
		t.Fatalf("loaded %#v, %v", loaded, err)
	}
	// A saved profile is an ordinary bundle: it opens, verifies and carries
	// no driver payload, exactly like one `copy` writes without --include-driver.
	opened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	if err := opened.Verify(); err != nil {
		t.Fatal(err)
	}
	if opened.Manifest.Driver != nil {
		t.Fatalf("a plain saved profile carries a driver payload: %#v", opened.Manifest.Driver)
	}
}

func TestEditProfilePreservesPreviousVersionAndDriverPayload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "printer.ssb")
	p := sampleProfile()
	if err := SaveProfile(path, p); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"New queue", "Next queue"} {
		prior := p.PrinterName
		p.PrinterName = name
		backup, err := EditProfile(path, p)
		if err != nil {
			t.Fatal(err)
		}
		old, err := LoadProfile(backup)
		if err != nil || old.PrinterName != prior {
			t.Fatalf("backup=%#v %v", old, err)
		}
		current, err := LoadProfile(path)
		if err != nil || current.PrinterName != name {
			t.Fatalf("updated=%#v %v", current, err)
		}
	}
}

// TestEditProfilePreservesEmbeddedDriver guards against the one real edge
// case merging the two formats introduced: editing a profile that also
// carries a driver payload (from `copy --include-driver`) must not silently
// drop that payload.
func TestEditProfilePreservesEmbeddedDriver(t *testing.T) {
	dir := t.TempDir()
	payload := filepath.Join(dir, "payload")
	if err := os.MkdirAll(payload, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(payload, "driver.inf"), []byte("[Version]\n"), 0600); err != nil {
		t.Fatal(err)
	}
	p := sampleProfile()
	path := filepath.Join(dir, "printer.ssb")
	if err := Write(path, Manifest{Profile: p, Driver: &DriverPayload{WindowsDriverName: p.DriverName, INF: "driver.inf"}}, payload); err != nil {
		t.Fatal(err)
	}
	p.PrinterName = "Renamed queue"
	if _, err := EditProfile(path, p); err != nil {
		t.Fatal(err)
	}
	opened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	if opened.Manifest.Driver == nil || len(opened.Manifest.Driver.Files) == 0 {
		t.Fatalf("edit dropped the embedded driver payload: %#v", opened.Manifest.Driver)
	}
	if opened.Manifest.Profile.PrinterName != "Renamed queue" {
		t.Fatalf("edit did not apply: %#v", opened.Manifest.Profile)
	}
	if err := opened.Verify(); err != nil {
		t.Fatalf("re-written payload failed verification: %v", err)
	}
}
