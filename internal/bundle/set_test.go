package bundle

import (
	"archive/zip"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// memberFile writes a settings-only printer file named after printerName and
// returns its path.
func memberFile(t *testing.T, printerName string) string {
	t.Helper()
	p := sampleProfile()
	p.PrinterName = printerName
	path := filepath.Join(t.TempDir(), "member.ssb")
	if err := SaveProfile(path, p); err != nil {
		t.Fatal(err)
	}
	return path
}

// writeRawZip builds a zip with plain archive/zip, the way Explorer's
// "Compress to ZIP folder" or any other tool would -- never through WriteSet.
func writeRawZip(t *testing.T, entries []string, data map[string][]byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "hand-made.zip")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for _, name := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if len(data[name]) == 0 {
			continue
		}
		if _, err := w.Write(data[name]); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestWriteOpenExtractSetRoundTripAndNeverOverwrites(t *testing.T) {
	office, warehouse := memberFile(t, "Office"), memberFile(t, "Warehouse")
	path := filepath.Join(t.TempDir(), "printers.zip")
	members := []SetMember{{Name: "Office.ssb", Path: office}, {Name: "Warehouse.ssb", Path: warehouse}}
	if err := WriteSet(path, "site move", members); err != nil {
		t.Fatal(err)
	}
	before := readFile(t, path)
	if err := WriteSet(path, "", members); err == nil {
		t.Fatal("overwrote existing set")
	}
	if !bytes.Equal(readFile(t, path), before) {
		t.Fatal("a refused WriteSet changed the existing set")
	}

	opened, err := OpenSet(path)
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	if opened.Note != "site move" || opened.Path != path {
		t.Fatalf("set = %+v", opened)
	}
	if !slices.Equal(opened.Members, []string{"Office.ssb", "Warehouse.ssb"}) {
		t.Fatalf("member list wrong: %v", opened.Members)
	}
	dir := t.TempDir()
	for name, source := range map[string]string{"Office.ssb": office, "Warehouse.ssb": warehouse} {
		got, err := opened.Extract(name, dir)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got != filepath.Join(dir, name) || !bytes.Equal(readFile(t, got), readFile(t, source)) {
			t.Fatalf("%s: bytes changed on the way through the set", name)
		}
		// Extract never overwrites what it (or anyone) already wrote.
		if _, err := opened.Extract(name, dir); err == nil {
			t.Fatalf("%s: extracted over an existing file", name)
		}
	}
	if _, err := opened.Extract("Missing.ssb", dir); err == nil {
		t.Fatal("extracted a member the set does not carry")
	}
	if isSet, err := IsSet(path); err != nil || !isSet {
		t.Fatalf("IsSet(set) = %v, %v", isSet, err)
	}
	if isSet, err := IsSet(office); err != nil || isSet {
		t.Fatalf("IsSet(single bundle) = %v, %v", isSet, err)
	}
}

// TestMemberWithEmbeddedDriverRoundTripsByteIdentical is the operator's
// direction made concrete: drivers are preferred, so a set carries a member's
// embedded driver payload verbatim rather than refusing or stripping it.
func TestMemberWithEmbeddedDriverRoundTripsByteIdentical(t *testing.T) {
	source := writeBundle(t, true)
	path := filepath.Join(t.TempDir(), "printers.zip")
	if err := WriteSet(path, "", []SetMember{{Name: "Accounting.ssb", Path: source}}); err != nil {
		t.Fatal(err)
	}
	opened, err := OpenSet(path)
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	extracted, err := opened.Extract("Accounting.ssb", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(readFile(t, extracted), readFile(t, source)) {
		t.Fatal("embedded-driver member changed on the way through the set")
	}
	member, err := Open(extracted)
	if err != nil {
		t.Fatal(err)
	}
	defer member.Close()
	if member.Manifest.Driver == nil || len(member.Manifest.Driver.Files) != 3 {
		t.Fatalf("driver payload lost: %+v", member.Manifest.Driver)
	}
}

// TestHandMadeZipOfPrinterFilesOpensAsASet: a set has no index, so a zip an
// operator made by selecting two .ssb files and compressing them is a set.
func TestHandMadeZipOfPrinterFilesOpensAsASet(t *testing.T) {
	office, lobby := readFile(t, memberFile(t, "Office")), readFile(t, memberFile(t, "Lobby"))
	path := writeRawZip(t, []string{"Office.ssb", "Lobby.ssb"}, map[string][]byte{"Office.ssb": office, "Lobby.ssb": lobby})
	if isSet, err := IsSet(path); err != nil || !isSet {
		t.Fatalf("IsSet = %v, %v", isSet, err)
	}
	opened, err := OpenSet(path)
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	if opened.Note != "" || !slices.Equal(opened.Members, []string{"Office.ssb", "Lobby.ssb"}) {
		t.Fatalf("set = %+v", opened)
	}
	extracted, err := opened.Extract("Lobby.ssb", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(readFile(t, extracted), lobby) {
		t.Fatal("member bytes changed")
	}
}

func TestOpenSetFailsClosedOnAnythingButTopLevelPrinterFiles(t *testing.T) {
	office := readFile(t, memberFile(t, "Office"))
	for _, tc := range []struct {
		name    string
		entries []string
		want    string
	}{
		{"nested path", []string{"Office.ssb", "site/Lobby.ssb"}, "inside a folder"},
		{"backslash path", []string{`site\Lobby.ssb`}, "inside a folder"},
		{"non-ssb file", []string{"Office.ssb", "readme.txt"}, "not a printer file"},
		{"empty zip", nil, "no printer files"},
		{"only a folder", []string{"site/"}, "no printer files"},
		{"case-only duplicate", []string{"Office.ssb", "OFFICE.ssb"}, "same file name"},
		{"reserved name", []string{"CON.ssb"}, "unsafe member filename"},
		{"single bundle", []string{ManifestName}, "single printer file"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := map[string][]byte{}
			for _, name := range tc.entries {
				if !strings.HasSuffix(name, "/") {
					data[name] = office
				}
			}
			path := writeRawZip(t, tc.entries, data)
			_, err := OpenSet(path)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("OpenSet = %v, want an error mentioning %q", err, tc.want)
			}
		})
	}
	t.Run("directory entries are ignored", func(t *testing.T) {
		path := writeRawZip(t, []string{"printers/", "Office.ssb"}, map[string][]byte{"Office.ssb": office})
		opened, err := OpenSet(path)
		if err != nil {
			t.Fatal(err)
		}
		defer opened.Close()
		if !slices.Equal(opened.Members, []string{"Office.ssb"}) {
			t.Fatalf("members = %v", opened.Members)
		}
	})
	t.Run("too many members", func(t *testing.T) {
		names := make([]string, 0, MaxSetMembers+1)
		for i := 0; i <= MaxSetMembers; i++ {
			names = append(names, fmt.Sprintf("printer-%d.ssb", i))
		}
		path := writeRawZip(t, names, map[string][]byte{})
		if _, err := OpenSet(path); err == nil || !strings.Contains(err.Error(), "more than") {
			t.Fatalf("OpenSet = %v", err)
		}
	})
}

func TestExtractRemovesAMemberThatIsNotAPrinterFile(t *testing.T) {
	path := writeRawZip(t, []string{"Broken.ssb"}, map[string][]byte{"Broken.ssb": []byte("not a zip")})
	opened, err := OpenSet(path)
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	dir := t.TempDir()
	if _, err := opened.Extract("Broken.ssb", dir); err == nil {
		t.Fatal("extracted an invalid member")
	}
	if _, err := os.Stat(filepath.Join(dir, "Broken.ssb")); !os.IsNotExist(err) {
		t.Fatalf("invalid member left on disk: %v", err)
	}
}

func TestIsSetDispatchesOnContent(t *testing.T) {
	single := memberFile(t, "Office")
	renamed := filepath.Join(t.TempDir(), "office.zip")
	if err := os.WriteFile(renamed, readFile(t, single), 0600); err != nil {
		t.Fatal(err)
	}
	if isSet, err := IsSet(renamed); err != nil || isSet {
		t.Fatalf("IsSet(single bundle named .zip) = %v, %v", isSet, err)
	}
	junk := filepath.Join(t.TempDir(), "junk.ssb")
	if err := os.WriteFile(junk, []byte("hello"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := IsSet(junk); err == nil {
		t.Fatal("IsSet accepted a file that is not a zip")
	}
	other := writeRawZip(t, []string{"photo.jpg"}, map[string][]byte{"photo.jpg": []byte("x")})
	if _, err := IsSet(other); err == nil {
		t.Fatal("IsSet accepted a zip of unrelated files")
	}
}

func TestWriteSetRejectsUnsafeDuplicateOrInvalidMembers(t *testing.T) {
	office := memberFile(t, "Office")
	junk := filepath.Join(t.TempDir(), "junk.ssb")
	if err := os.WriteFile(junk, []byte("not a bundle"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []SetMember{
		{Name: "../outside.ssb", Path: office},
		{Name: `..\outside.ssb`, Path: office},
		{Name: "C:bad.ssb", Path: office},
		{Name: "CON.ssb", Path: office},
		{Name: "bad\n.ssb", Path: office},
		{Name: "office.ssb", Path: office}, // duplicates the other member, case-insensitively
		{Name: "office.json", Path: office},
		{Name: "", Path: office},
		{Name: "Junk.ssb", Path: junk}, // not a valid bundle
		{Name: "Gone.ssb", Path: filepath.Join(t.TempDir(), "missing.ssb")},
	} {
		t.Run(bad.Name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "printers.zip")
			if err := WriteSet(path, "", []SetMember{{Name: "Office.ssb", Path: office}, bad}); err == nil {
				t.Fatalf("accepted bad member %+v", bad)
			}
			if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
				t.Fatal("a rejected set was left on disk")
			}
		})
	}
}

func TestWriteSetRejectsEmptyOrOversizedMembership(t *testing.T) {
	if err := WriteSet(filepath.Join(t.TempDir(), "empty.zip"), "", nil); err == nil {
		t.Fatal("accepted a set with no members")
	}
	office := memberFile(t, "Office")
	members := make([]SetMember, 0, MaxSetMembers+1)
	for i := 0; i <= MaxSetMembers; i++ {
		members = append(members, SetMember{Name: fmt.Sprintf("printer-%d.ssb", i), Path: office})
	}
	if err := WriteSet(filepath.Join(t.TempDir(), "too-many.zip"), "", members); err == nil {
		t.Fatal("accepted more members than the limit")
	}
}
