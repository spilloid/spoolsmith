package bundle

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func memberBytes(t *testing.T, name string) []byte {
	t.Helper()
	p := sampleProfile()
	p.PrinterName = name
	path := filepath.Join(t.TempDir(), "member.ssb")
	if err := SaveProfile(path, p); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestWriteOpenSetRoundTripAndNeverOverwrites(t *testing.T) {
	office, warehouse := memberBytes(t, "Office"), memberBytes(t, "Warehouse")
	path := filepath.Join(t.TempDir(), "printers.ssb")
	members := []SetMember{{Name: "Office.ssb", Data: office}, {Name: "Warehouse.ssb", Data: warehouse}}
	if err := WriteSet(path, SetIndex{CreatedBy: "test", Note: "site move"}, members); err != nil {
		t.Fatal(err)
	}
	if err := WriteSet(path, SetIndex{}, members); err == nil {
		t.Fatal("overwrote existing set")
	}

	opened, err := OpenSet(path)
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	if opened.Index.CreatedBy != "test" || opened.Index.Note != "site move" {
		t.Fatalf("provenance lost: %#v", opened.Index)
	}
	if len(opened.Index.Members) != 2 || opened.Index.Members[0] != "Office.ssb" || opened.Index.Members[1] != "Warehouse.ssb" {
		t.Fatalf("member list wrong: %v", opened.Index.Members)
	}
	for name, want := range map[string][]byte{"Office.ssb": office, "Warehouse.ssb": warehouse} {
		got, err := opened.MemberBytes(name)
		if err != nil || string(got) != string(want) {
			t.Fatalf("%s: bytes changed: %v", name, err)
		}
		member, err := opened.Open(name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		member.Close()
	}
	if _, err := opened.MemberBytes("Missing.ssb"); err == nil {
		t.Fatal("returned bytes for a member the set does not carry")
	}
}

func TestWriteSetRejectsUnsafeOrDuplicateMemberNames(t *testing.T) {
	data := memberBytes(t, "Office")
	for _, bad := range []string{
		"../outside.ssb", `..\outside.ssb`, "C:bad.ssb", "CON.ssb", "bad\n.ssb",
		"office.ssb",  // duplicates the other member, case-insensitively
		"office.json", // not a .ssb
		"",
	} {
		t.Run(bad, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "printers.ssb")
			err := WriteSet(path, SetIndex{}, []SetMember{{Name: "Office.ssb", Data: data}, {Name: bad, Data: data}})
			if err == nil {
				t.Fatalf("accepted unsafe/duplicate member name %q", bad)
			}
			if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
				t.Fatal("a rejected set was written to disk")
			}
		})
	}
}

func TestWriteSetRejectsEmptyOrOversizedMembership(t *testing.T) {
	if err := WriteSet(filepath.Join(t.TempDir(), "empty.ssb"), SetIndex{}, nil); err == nil {
		t.Fatal("accepted a set with no members")
	}
	data := memberBytes(t, "Office")
	members := make([]SetMember, 0, MaxSetMembers+1)
	for i := 0; i <= MaxSetMembers; i++ {
		members = append(members, SetMember{Name: fmt.Sprintf("printer-%d.ssb", i), Data: data})
	}
	if err := WriteSet(filepath.Join(t.TempDir(), "too-many.ssb"), SetIndex{}, members); err == nil {
		t.Fatal("accepted more members than the limit")
	}
}

// TestOpenSetRejectsIndexArchiveMismatch guards against a set whose archive
// contents disagree with what its own index claims -- built by hand with
// archive/zip directly, since WriteSet itself can never produce one. Neither
// an extra unlisted member nor an index entry with nothing behind it may be
// tolerated silently.
func TestOpenSetRejectsIndexArchiveMismatch(t *testing.T) {
	office := memberBytes(t, "Office")
	writeRawSet := func(t *testing.T, path string, index SetIndex, entries map[string][]byte) {
		t.Helper()
		f, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		zw := zip.NewWriter(f)
		indexJSON, err := json.Marshal(index)
		if err != nil {
			t.Fatal(err)
		}
		w, err := zw.Create(SetIndexName)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(indexJSON); err != nil {
			t.Fatal(err)
		}
		for name, data := range entries {
			w, err := zw.Create(name)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := w.Write(data); err != nil {
				t.Fatal(err)
			}
		}
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("unlisted extra member", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "printers.ssb")
		writeRawSet(t, path, SetIndex{Version: SetVersion, Members: []string{"Office.ssb"}}, map[string][]byte{
			SetMemberPath + "Office.ssb":    office,
			SetMemberPath + "Warehouse.ssb": office, // present in the archive but never listed
		})
		if _, err := OpenSet(path); err == nil {
			t.Fatal("accepted a set with an unlisted archive entry")
		}
	})
	t.Run("listed member missing from archive", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "printers.ssb")
		writeRawSet(t, path, SetIndex{Version: SetVersion, Members: []string{"Office.ssb", "Warehouse.ssb"}}, map[string][]byte{
			SetMemberPath + "Office.ssb": office, // Warehouse.ssb listed but never written
		})
		if _, err := OpenSet(path); err == nil {
			t.Fatal("accepted a set missing a listed member")
		}
	})
}
