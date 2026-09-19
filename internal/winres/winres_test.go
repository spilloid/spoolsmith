package winres

import (
	"bytes"
	"debug/pe"
	"encoding/binary"
	"image"
	"image/color"
	"testing"

	"github.com/spilloid/spoolsmith/internal/icon"
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

	data := sectionData(t, file)
	leaves := parseResources(t, data)
	reloc := section.Relocs[0]
	if reloc.VirtualAddress != leaves[0].dataEntry {
		t.Errorf("relocation offset = %d, want %d (the leaf's OffsetToData field)", reloc.VirtualAddress, leaves[0].dataEntry)
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

	data := sectionData(t, file)
	leaves := parseResources(t, data)
	if len(leaves) != 1 {
		t.Fatalf("resource count = %d, want 1", len(leaves))
	}
	leaf := leaves[0]
	if leaf.typ != rtManifest {
		t.Errorf("resource type = %d, want %d (RT_MANIFEST)", leaf.typ, rtManifest)
	}
	if leaf.id != createProcessManifestID {
		t.Errorf("resource name = %d, want %d (CREATEPROCESS_MANIFEST_RESOURCE_ID)", leaf.id, createProcessManifestID)
	}
	if leaf.lang != langUSEnglish {
		t.Errorf("resource language = %d, want %d", leaf.lang, langUSEnglish)
	}

	// The leaf's OffsetToData is the relocation's addend: the linker adds the
	// section's virtual address to it, so it must be the data's offset within
	// this section and not a placeholder.
	if got := string(data[leaf.offsetToData : leaf.offsetToData+leaf.size]); got != testManifest {
		t.Errorf("embedded manifest = %q, want %q", got, testManifest)
	}
}

// The manifest-only layout must not move when icon support was added, because
// the committed GUI object is drift-checked against it.
func TestManifestOnlyLayoutIsUnchangedByIconSupport(t *testing.T) {
	object, err := ManifestObject([]byte(testManifest), "amd64")
	if err != nil {
		t.Fatalf("ManifestObject: %v", err)
	}
	file, err := pe.NewFile(bytes.NewReader(object))
	if err != nil {
		t.Fatalf("parsing generated object: %v", err)
	}
	defer file.Close()

	leaf := parseResources(t, sectionData(t, file))[0]
	// type dir 24 + name dir 24 + language dir 24 = 72, then the 16-byte entry.
	if leaf.dataEntry != 72 || leaf.offsetToData != 88 {
		t.Errorf("data entry at %d, data at %d; want 72 and 88 as before", leaf.dataEntry, leaf.offsetToData)
	}
}

func TestIconAndManifestObjectCarriesEveryResource(t *testing.T) {
	entries := testIconEntries(t, []int{16, 32})
	resources := append(IconResources(entries), ManifestResource([]byte(testManifest)))
	// Deliberately unsorted: the PE directory needs ordered keys, so Object must
	// sort rather than trust the caller.
	resources[0], resources[len(resources)-1] = resources[len(resources)-1], resources[0]

	object, err := Object(resources, "amd64")
	if err != nil {
		t.Fatalf("Object: %v", err)
	}
	file, err := pe.NewFile(bytes.NewReader(object))
	if err != nil {
		t.Fatalf("parsing generated object: %v", err)
	}
	defer file.Close()

	data := sectionData(t, file)
	leaves := parseResources(t, data)
	// Two RT_ICON images, one RT_GROUP_ICON, one RT_MANIFEST.
	if len(leaves) != 4 {
		t.Fatalf("resource count = %d, want 4", len(leaves))
	}

	wantOrder := []struct{ typ, id uint32 }{
		{rtIcon, 1}, {rtIcon, 2}, {rtGroupIcon, AppIconGroupID}, {rtManifest, createProcessManifestID},
	}
	for i, want := range wantOrder {
		if leaves[i].typ != want.typ || leaves[i].id != want.id {
			t.Errorf("resource %d = type %d ID %d, want type %d ID %d", i, leaves[i].typ, leaves[i].id, want.typ, want.id)
		}
	}

	relocs := file.Sections[0].Relocs
	if len(relocs) != len(leaves) {
		t.Fatalf("relocations = %d, want one per resource (%d)", len(relocs), len(leaves))
	}
	for i, leaf := range leaves {
		if relocs[i].VirtualAddress != leaf.dataEntry {
			t.Errorf("relocation %d applies at %d, want the leaf's OffsetToData field at %d", i, relocs[i].VirtualAddress, leaf.dataEntry)
		}
		if leaf.offsetToData%dataAlignment != 0 {
			t.Errorf("resource %d data offset %d is not %d-byte aligned", i, leaf.offsetToData, dataAlignment)
		}
	}

	// Data round-trips byte for byte: each image, the group directory, the manifest.
	for i, entry := range entries {
		got := data[leaves[i].offsetToData : leaves[i].offsetToData+leaves[i].size]
		if !bytes.Equal(got, entry.DIB) {
			t.Errorf("RT_ICON %d does not carry the %dpx image", i+1, entry.Size)
		}
	}
	group := data[leaves[2].offsetToData : leaves[2].offsetToData+leaves[2].size]
	if !bytes.Equal(group, icon.GroupIcon(entries, 1)) {
		t.Error("RT_GROUP_ICON does not match the entries it should describe")
	}
	// Every ID the group names must exist as an RT_ICON of the advertised size.
	count := int(binary.LittleEndian.Uint16(group[4:]))
	for i := 0; i < count; i++ {
		dir := group[6+14*i:]
		id := uint32(binary.LittleEndian.Uint16(dir[12:]))
		length := binary.LittleEndian.Uint32(dir[8:])
		found := false
		for _, leaf := range leaves {
			if leaf.typ == rtIcon && leaf.id == id {
				found = true
				if leaf.size != length {
					t.Errorf("group entry %d says %d bytes, RT_ICON %d holds %d", i, length, id, leaf.size)
				}
			}
		}
		if !found {
			t.Errorf("group entry %d names RT_ICON %d, which is not in the object", i, id)
		}
	}
	if got := string(data[leaves[3].offsetToData : leaves[3].offsetToData+leaves[3].size]); got != testManifest {
		t.Errorf("embedded manifest = %q, want %q", got, testManifest)
	}
}

func TestObjectIsDeterministic(t *testing.T) {
	entries := testIconEntries(t, []int{16, 32})
	build := func() []byte {
		object, err := Object(append(IconResources(entries), ManifestResource([]byte(testManifest))), "amd64")
		if err != nil {
			t.Fatalf("Object: %v", err)
		}
		return object
	}
	if !bytes.Equal(build(), build()) {
		t.Error("two identical calls produced different bytes; the committed .syso could not be drift-checked")
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

func TestObjectRejectsUnusableInput(t *testing.T) {
	if _, err := ManifestObject([]byte(testManifest), "riscv64"); err == nil {
		t.Error("expected an error for an unsupported architecture")
	}
	if _, err := ManifestObject(nil, "amd64"); err == nil {
		t.Error("expected an error for an empty manifest")
	}
	if _, err := Object(nil, "amd64"); err == nil {
		t.Error("expected an error when there is nothing to embed")
	}
	if _, err := Object([]Resource{{Type: rtIcon, ID: 1}}, "amd64"); err == nil {
		t.Error("expected an error for an empty resource")
	}
	duplicate := []Resource{{Type: rtIcon, ID: 1, Data: []byte{1}}, {Type: rtIcon, ID: 1, Data: []byte{2}}}
	if _, err := Object(duplicate, "amd64"); err == nil {
		t.Error("expected an error for a duplicate type/ID pair")
	}
}

func testIconEntries(t *testing.T, sizes []int) []icon.Entry {
	t.Helper()
	src := image.NewNRGBA(image.Rect(0, 0, 64, 64))
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			src.SetNRGBA(x, y, color.NRGBA{R: uint8(x * 4), G: uint8(y * 4), B: 40, A: 255})
		}
	}
	entries, err := icon.Entries(src, sizes)
	if err != nil {
		t.Fatalf("icon.Entries: %v", err)
	}
	return entries
}

func sectionData(t *testing.T, file *pe.File) []byte {
	t.Helper()
	data, err := file.Sections[0].Data()
	if err != nil {
		t.Fatalf("reading section data: %v", err)
	}
	return data
}

// leaf is one resource found by walking the type -> name -> language tree.
type leaf struct {
	typ, id, lang uint32
	dataEntry     uint32 // offset of the IMAGE_RESOURCE_DATA_ENTRY
	offsetToData  uint32
	size          uint32
}

// parseResources walks the three directory levels Windows requires and returns
// every leaf in directory order, failing the test on anything malformed.
func parseResources(t *testing.T, data []byte) []leaf {
	t.Helper()
	var leaves []leaf
	for _, typeEntry := range readDirectory(t, data, 0) {
		if typeEntry.target&0x80000000 == 0 {
			t.Fatalf("type %d entry is not marked as a subdirectory", typeEntry.id)
		}
		for _, nameEntry := range readDirectory(t, data, typeEntry.target&0x7fffffff) {
			if nameEntry.target&0x80000000 == 0 {
				t.Fatalf("type %d name %d entry is not marked as a subdirectory", typeEntry.id, nameEntry.id)
			}
			for _, langEntry := range readDirectory(t, data, nameEntry.target&0x7fffffff) {
				if langEntry.target&0x80000000 != 0 {
					t.Fatalf("type %d name %d language entry points at a subdirectory, not a leaf", typeEntry.id, nameEntry.id)
				}
				at := langEntry.target
				leaves = append(leaves, leaf{
					typ: typeEntry.id, id: nameEntry.id, lang: langEntry.id,
					dataEntry:    at,
					offsetToData: binary.LittleEndian.Uint32(data[at:]),
					size:         binary.LittleEndian.Uint32(data[at+4:]),
				})
			}
		}
	}
	return leaves
}

type dirEntry struct{ id, target uint32 }

// readDirectory reads an IMAGE_RESOURCE_DIRECTORY and its ID entries, and checks
// that the IDs are strictly ascending, which Windows requires to binary-search.
func readDirectory(t *testing.T, data []byte, offset uint32) []dirEntry {
	t.Helper()
	if int(offset)+dirSize > len(data) {
		t.Fatalf("directory at %d runs past the section", offset)
	}
	if named := binary.LittleEndian.Uint16(data[offset+12:]); named != 0 {
		t.Fatalf("directory at %d has %d named entries, want none", offset, named)
	}
	count := int(binary.LittleEndian.Uint16(data[offset+14:]))
	entries := make([]dirEntry, 0, count)
	for i := 0; i < count; i++ {
		at := int(offset) + dirSize + i*dirEntrySize
		if at+dirEntrySize > len(data) {
			t.Fatalf("directory entry at %d runs past the section", at)
		}
		entry := dirEntry{binary.LittleEndian.Uint32(data[at:]), binary.LittleEndian.Uint32(data[at+4:])}
		if i > 0 && entry.id <= entries[i-1].id {
			t.Fatalf("directory at %d is not sorted by ID (%d after %d)", offset, entry.id, entries[i-1].id)
		}
		entries = append(entries, entry)
	}
	return entries
}
