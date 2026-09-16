package winres

import (
	"bytes"
	"debug/pe"
	"encoding/binary"
	"testing"
)

const testManifest = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><assembly/>`

func TestManifestObjectParsesAsACOFFObject(t *testing.T) {
	object, err := ManifestObject([]byte(testManifest), "amd64")
	if err != nil {
		t.Fatalf("ManifestObject: %v", err)
	}

	file, err := pe.NewFile(bytes.NewReader(object))
	if err != nil {
		t.Fatalf("parsing generated object: %v", err)
	}
	defer file.Close()

	if file.FileHeader.Machine != pe.IMAGE_FILE_MACHINE_AMD64 {
		t.Errorf("machine = %#x, want %#x", file.FileHeader.Machine, pe.IMAGE_FILE_MACHINE_AMD64)
	}
	if len(file.Sections) != 1 {
		t.Fatalf("section count = %d, want 1", len(file.Sections))
	}

	section := file.Sections[0]
	if section.Name != ".rsrc" {
		t.Errorf("section name = %q, want .rsrc", section.Name)
	}
	// The Go linker skips sections that claim to hold no content at all.
	if section.Characteristics&pe.IMAGE_SCN_CNT_INITIALIZED_DATA == 0 {
		t.Error("section is not marked as initialized data; the linker would ignore it")
	}
	if len(section.Relocs) != 1 {
		t.Fatalf("relocation count = %d, want 1", len(section.Relocs))
	}

	reloc := section.Relocs[0]
	if reloc.VirtualAddress != dataEntryOff {
		t.Errorf("relocation offset = %d, want %d (the leaf's OffsetToData field)", reloc.VirtualAddress, dataEntryOff)
	}
	if reloc.Type != 0x0003 {
		t.Errorf("relocation type = %#x, want %#x (IMAGE_REL_AMD64_ADDR32NB)", reloc.Type, 0x0003)
	}
	if int(reloc.SymbolTableIndex) >= len(file.COFFSymbols) {
		t.Fatalf("relocation references symbol %d, but the table holds %d", reloc.SymbolTableIndex, len(file.COFFSymbols))
	}

	symbol := file.COFFSymbols[reloc.SymbolTableIndex]
	if got := string(bytes.TrimRight(symbol.Name[:], "\x00")); got != ".rsrc" {
		t.Errorf("relocation symbol = %q, want .rsrc", got)
	}
	if symbol.SectionNumber != 1 {
		t.Errorf("relocation symbol section = %d, want 1", symbol.SectionNumber)
	}
	if symbol.StorageClass != imageSymClassStatic {
		t.Errorf("relocation symbol storage class = %d, want %d", symbol.StorageClass, imageSymClassStatic)
	}
}

func TestManifestObjectBuildsTheResourceTreeWindowsExpects(t *testing.T) {
	object, err := ManifestObject([]byte(testManifest), "amd64")
	if err != nil {
		t.Fatalf("ManifestObject: %v", err)
	}
	file, err := pe.NewFile(bytes.NewReader(object))
	if err != nil {
		t.Fatalf("parsing generated object: %v", err)
	}
	defer file.Close()

	data, err := file.Sections[0].Data()
	if err != nil {
		t.Fatalf("reading section data: %v", err)
	}

	// Walk type -> name -> language, the three levels Windows requires.
	typeID, typeOffset := readDirEntry(t, data, typeDirOffset+dirSize)
	if typeID != rtManifest {
		t.Errorf("resource type = %d, want %d (RT_MANIFEST)", typeID, rtManifest)
	}
	if typeOffset&0x80000000 == 0 {
		t.Fatal("type entry is not marked as a subdirectory")
	}
	nameID, nameOffset := readDirEntry(t, data, typeOffset&0x7fffffff+dirSize)
	if nameID != createProcessManifestID {
		t.Errorf("resource name = %d, want %d (CREATEPROCESS_MANIFEST_RESOURCE_ID)", nameID, createProcessManifestID)
	}
	langID, leafOffset := readDirEntry(t, data, nameOffset&0x7fffffff+dirSize)
	if langID != langUSEnglish {
		t.Errorf("resource language = %d, want %d", langID, langUSEnglish)
	}
	if leafOffset&0x80000000 != 0 {
		t.Fatal("language entry points at a subdirectory, not a leaf")
	}

	// The leaf's OffsetToData is the relocation's addend: the linker adds the
	// section's virtual address to it, so it must be the manifest's offset
	// within this section and not a placeholder.
	offsetToData := binary.LittleEndian.Uint32(data[leafOffset:])
	size := binary.LittleEndian.Uint32(data[leafOffset+4:])
	if offsetToData != manifestOffset {
		t.Errorf("leaf OffsetToData = %d, want %d (the in-place relocation addend)", offsetToData, manifestOffset)
	}
	if size != uint32(len(testManifest)) {
		t.Errorf("leaf size = %d, want %d", size, len(testManifest))
	}
	if got := string(data[offsetToData : offsetToData+size]); got != testManifest {
		t.Errorf("embedded manifest = %q, want %q", got, testManifest)
	}
}

func TestManifestObjectIsDeterministic(t *testing.T) {
	first, err := ManifestObject([]byte(testManifest), "amd64")
	if err != nil {
		t.Fatalf("ManifestObject: %v", err)
	}
	second, err := ManifestObject([]byte(testManifest), "amd64")
	if err != nil {
		t.Fatalf("ManifestObject: %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Error("two identical calls produced different bytes; the committed .syso could not be drift-checked")
	}
}

func TestManifestObjectUsesEachArchitecturesImageRelativeRelocation(t *testing.T) {
	for _, tc := range []struct {
		arch      string
		machine   uint16
		relocType uint16
	}{
		{"386", 0x014c, 0x0007},
		{"amd64", 0x8664, 0x0003},
		{"arm64", 0xaa64, 0x0002},
	} {
		t.Run(tc.arch, func(t *testing.T) {
			object, err := ManifestObject([]byte(testManifest), tc.arch)
			if err != nil {
				t.Fatalf("ManifestObject: %v", err)
			}
			file, err := pe.NewFile(bytes.NewReader(object))
			if err != nil {
				t.Fatalf("parsing generated object: %v", err)
			}
			defer file.Close()
			if file.FileHeader.Machine != tc.machine {
				t.Errorf("machine = %#x, want %#x", file.FileHeader.Machine, tc.machine)
			}
			if got := file.Sections[0].Relocs[0].Type; got != tc.relocType {
				t.Errorf("relocation type = %#x, want %#x", got, tc.relocType)
			}
		})
	}
}

func TestManifestObjectRejectsUnusableInput(t *testing.T) {
	if _, err := ManifestObject([]byte(testManifest), "riscv64"); err == nil {
		t.Error("expected an error for an unsupported architecture")
	}
	if _, err := ManifestObject(nil, "amd64"); err == nil {
		t.Error("expected an error for an empty manifest")
	}
}

// readDirEntry reads the single IMAGE_RESOURCE_DIRECTORY_ENTRY that follows a
// directory header at the given offset.
func readDirEntry(t *testing.T, data []byte, offset uint32) (id, target uint32) {
	t.Helper()
	if int(offset)+dirEntrySize > len(data) {
		t.Fatalf("directory entry at %d runs past the section", offset)
	}
	return binary.LittleEndian.Uint32(data[offset:]), binary.LittleEndian.Uint32(data[offset+4:])
}
