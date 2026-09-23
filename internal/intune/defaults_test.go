package intune

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spilloid/spoolsmith/internal/bundle"
)

func TestProfileDefaultsKeepIdentityAcrossConfigurationChanges(t *testing.T) {
	o := testOptions(t)
	first, err := ProfileDefaults(o.ProfilePath)
	if err != nil {
		t.Fatal(err)
	}
	p, err := bundle.LoadProfile(o.ProfilePath)
	if err != nil {
		t.Fatal(err)
	}
	if first.DisplayName != p.PrinterName || first.Revision != 1 || first.Location != "" || first.Offline || first.Adopt || first.DriverPrerequisite {
		t.Fatalf("unexpected defaults: %+v", first)
	}
	if !strings.Contains(first.Description, p.Target) {
		t.Fatal("description omits printer address")
	}
	p.Target = "192.0.2.41"
	p.DriverName = "New driver"
	other := filepath.Join(t.TempDir(), "renamed.ssb")
	if err := bundle.SaveProfile(other, p); err != nil {
		t.Fatal(err)
	}
	next, err := ProfileDefaults(other)
	if err != nil || next.ID != first.ID {
		t.Fatalf("configuration changed identity: %+v %v", next, err)
	}
}

func TestProfileDefaultsProduceDistinctValidIDs(t *testing.T) {
	o := testOptions(t)
	p, _ := bundle.LoadProfile(o.ProfilePath)
	seen := make(map[string]bool)
	for _, name := range []string{"Office A", "Office-A", "OFFICE A", "打印机", "!!!", strings.Repeat("a", 150), strings.Repeat("a", 150) + "b"} {
		p.PrinterName = name
		o.ProfilePath = filepath.Join(t.TempDir(), "profile.ssb")
		if err := bundle.SaveProfile(o.ProfilePath, p); err != nil {
			t.Fatal(err)
		}
		d, err := ProfileDefaults(o.ProfilePath)
		if err != nil || !identifier.MatchString(d.ID) || seen[d.ID] {
			t.Fatalf("invalid or duplicate ID for %q: %+v %v", name, d, err)
		}
		seen[d.ID] = true
	}
}

func TestSuggestedOutputDoesNotCreateOrOverwrite(t *testing.T) {
	parent := t.TempDir()
	first, err := SuggestOutput(parent, "printer", 2)
	if err != nil || first != filepath.Join(parent, "printer-r2") {
		t.Fatalf("%s %v", first, err)
	}
	if _, err := os.Stat(first); !os.IsNotExist(err) {
		t.Fatal("suggestion created a folder")
	}
	if err := os.WriteFile(first, []byte("keep me"), 0600); err != nil {
		t.Fatal(err)
	}
	second, err := SuggestOutput(parent, "printer", 2)
	if err != nil || second != first+"-2" {
		t.Fatalf("%s %v", second, err)
	}
	if _, err := SuggestOutput(parent, "../escape", 1); err == nil {
		t.Fatal("accepted path traversal")
	}
	p, err := Prepare(testOptions(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(second, 0700); err != nil {
		t.Fatal(err)
	}
	if err := p.Export(second); err == nil {
		t.Fatal("export accepted a folder created after suggestion")
	}
	if data, err := os.ReadFile(first); err != nil || string(data) != "keep me" {
		t.Fatal("existing content changed")
	}
}

func TestAutomaticPinStillVerifiesCLIIdentity(t *testing.T) {
	original, err := os.ReadFile(testBinary)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, message string
		mutate        func([]byte) []byte
	}{
		{"not PE", "Windows CLI binary", func(b []byte) []byte { return []byte("not an executable") }},
		{"wrong architecture", "only Windows x64", func(b []byte) []byte {
			offset := binary.LittleEndian.Uint32(b[0x3c:])
			binary.LittleEndian.PutUint16(b[offset+4:], 0x14c)
			return b
		}},
		{"wrong command", "select the SpoolSmith CLI", func(b []byte) []byte {
			return bytes.ReplaceAll(b, []byte("/cmd/spoolsmith"), []byte("/cmd/otherxxxxx"))
		}},
		{"old CLI", "lacks offline/status", func(b []byte) []byte {
			return bytes.ReplaceAll(b, []byte(EndpointCapability), bytes.Repeat([]byte("x"), len(EndpointCapability)))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := testOptions(t)
			o.BinarySHA256 = ""
			o.BinaryPath = filepath.Join(t.TempDir(), "cli.exe")
			if err := os.WriteFile(o.BinaryPath, tc.mutate(bytes.Clone(original)), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := Prepare(o); err == nil || !strings.Contains(err.Error(), tc.message) {
				t.Fatalf("expected %q, got %v", tc.message, err)
			}
		})
	}
}

func TestAutomaticallyPinnedSourceCannotChangeAfterReview(t *testing.T) {
	o := testOptions(t)
	o.BinarySHA256 = ""
	o.BinaryPath = filepath.Join(t.TempDir(), "cli.exe")
	data, err := os.ReadFile(testBinary)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(o.BinaryPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	p, err := Prepare(o)
	if err != nil || p.Manifest.BinarySHA256 != testBinaryHash {
		t.Fatalf("automatic pin failed: %v", err)
	}
	if err := os.WriteFile(o.BinaryPath, []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "export")
	if err := p.Export(dest); err == nil {
		t.Fatal("exported a changed automatically pinned binary")
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatal("partial export remains")
	}
}
