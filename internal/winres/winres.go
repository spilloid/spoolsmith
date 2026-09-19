// Package winres builds the Windows resource object (.syso) that embeds an
// application manifest and icon directly into a Go-linked PE image.
//
// SpoolSmith's desktop app needs the comctl32 v6 activation context; without it
// tailscale/walk's InitCommonControlsEx call fails and the GUI exits before it
// ever paints a window. Shipping that manifest as a side-car
// "<name>.exe.manifest" file is not safe: Windows caches the activation context
// per executable path, so an executable launched even once before its side-car
// lands stays broken at that path forever, and dropping the manifest in
// afterwards does not repair it. Embedding the manifest as an RT_MANIFEST
// resource removes the side-car, and with it the ordering hazard.
//
// The product icon travels the same way, for the same reason: an icon in the
// executable's own resources is what Explorer, the taskbar and Alt+Tab show, and
// it cannot be separated from the binary when a release is copied or renamed.
//
// The object this package emits is deliberately minimal: one .rsrc section
// holding a three-level resource tree (type, name, language), one relocation per
// leaf, and one section symbol. The Go linker turns each relocation's in-place
// addend into the resource RVA (see cmd/link/internal/ld.addpersrc), so a leaf
// records its data's offset inside the section rather than a placeholder.
package winres

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image/png"
	"io"
	"sort"

	"github.com/spilloid/spoolsmith/internal/icon"
)

// Resource type and identifier constants from the PE/COFF specification.
const (
	rtIcon      = 3  // RT_ICON
	rtGroupIcon = 14 // RT_GROUP_ICON
	rtManifest  = 24 // RT_MANIFEST
	// CREATEPROCESS_MANIFEST_RESOURCE_ID: the manifest Windows applies to the
	// process itself, as opposed to an isolated-COM or side-by-side manifest.
	createProcessManifestID = 1
	// AppIconGroupID is the RT_GROUP_ICON that represents the executable. It is 7
	// because tailscale/walk registers every window class it creates, main
	// window and dialogs alike, with LoadIcon(hInstance, 7) ("rsrc uses 7 for
	// app icon", walk/window.go). Numbering the group 7 makes each SpoolSmith
	// window carry the product icon with no per-window code. Explorer shows the
	// lowest-numbered group, and this is the only one, so it shows the same icon.
	AppIconGroupID = 7
	// US English. Neither manifests nor icons are localized, but the resource
	// tree's language level is not optional, and Windows looks here first.
	langUSEnglish = 1033
)

const (
	imageSCNCntInitializedData = 0x00000040
	imageSCNMemRead            = 0x40000000
	imageSymClassStatic        = 3

	fileHeaderSize    = 20
	sectionHeaderSize = 40
	relocationSize    = 10
	symbolSize        = 18
	dirSize           = 16 // IMAGE_RESOURCE_DIRECTORY
	dirEntrySize      = 8  // IMAGE_RESOURCE_DIRECTORY_ENTRY
	dataEntrySize     = 16 // IMAGE_RESOURCE_DATA_ENTRY

	// Resource data is padded to this boundary, as other resource compilers do.
	dataAlignment = 8
)

// target describes the per-architecture COFF values that differ between builds.
type target struct {
	machine   uint16
	relocType uint16
}

// targets maps a GOARCH to its COFF machine type and the image-relative
// ("NB", no base) 32-bit relocation that architecture uses.
var targets = map[string]target{
	"amd64": {machine: 0x8664, relocType: 0x0003}, // IMAGE_REL_AMD64_ADDR32NB
	"386":   {machine: 0x014c, relocType: 0x0007}, // IMAGE_REL_I386_DIR32NB
	"arm64": {machine: 0xaa64, relocType: 0x0002}, // IMAGE_REL_ARM64_ADDR32NB
}

// SupportedArches reports the GOARCH values Object and ManifestObject accept.
func SupportedArches() []string {
	return []string{"386", "amd64", "arm64"}
}

// Resource is one leaf of the resource tree.
type Resource struct {
	Type uint16
	ID   uint16
	Data []byte
}

// ManifestResource wraps an application manifest as the process's RT_MANIFEST.
func ManifestResource(manifest []byte) Resource {
	return Resource{Type: rtManifest, ID: createProcessManifestID, Data: manifest}
}

// IconResources turns rendered icon sizes into one RT_GROUP_ICON, numbered
// AppIconGroupID, plus the RT_ICON image it points at for each size.
func IconResources(entries []icon.Entry) []Resource {
	resources := []Resource{{Type: rtGroupIcon, ID: AppIconGroupID, Data: icon.GroupIcon(entries, 1)}}
	for i, entry := range entries {
		resources = append(resources, Resource{Type: rtIcon, ID: uint16(1 + i), Data: entry.DIB})
	}
	return resources
}

// IconResourcesFromPNG renders a square PNG at every size in icon.WindowsSizes
// and returns its icon resources. The generator and the drift tests both go
// through here, so what is embedded and what is checked cannot diverge.
func IconResourcesFromPNG(source io.Reader) ([]Resource, error) {
	decoded, err := png.Decode(source)
	if err != nil {
		return nil, fmt.Errorf("winres: decoding icon: %w", err)
	}
	entries, err := icon.Entries(decoded, icon.WindowsSizes)
	if err != nil {
		return nil, err
	}
	return IconResources(entries), nil
}

// ManifestObject returns a COFF object file that embeds manifest as the
// process's RT_MANIFEST resource when linked into a goarch PE binary. The
// result is deterministic: the same manifest and architecture always produce
// byte-identical output, so the generated file can be committed and checked for
// drift.
func ManifestObject(manifest []byte, goarch string) ([]byte, error) {
	if len(manifest) == 0 {
		return nil, fmt.Errorf("winres: manifest is empty")
	}
	return Object([]Resource{ManifestResource(manifest)}, goarch)
}

// Object returns a COFF object file carrying resources, deterministically:
// resources are ordered by type and then ID, which is also the order the PE
// resource directory requires, so the caller's ordering never matters.
func Object(resources []Resource, goarch string) ([]byte, error) {
	tgt, ok := targets[goarch]
	if !ok {
		return nil, fmt.Errorf("winres: unsupported architecture %q (want one of %v)", goarch, SupportedArches())
	}
	if len(resources) == 0 {
		return nil, fmt.Errorf("winres: no resources to embed")
	}
	// NumberOfRelocations is a 16-bit count in the section header.
	if len(resources) > 0xffff {
		return nil, fmt.Errorf("winres: %d resources exceed the 65535 one section can relocate", len(resources))
	}

	ordered := append([]Resource(nil), resources...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].Type != ordered[j].Type {
			return ordered[i].Type < ordered[j].Type
		}
		return ordered[i].ID < ordered[j].ID
	})
	for i, resource := range ordered {
		if len(resource.Data) == 0 {
			return nil, fmt.Errorf("winres: resource type %d ID %d is empty", resource.Type, resource.ID)
		}
		if i > 0 && ordered[i-1].Type == resource.Type && ordered[i-1].ID == resource.ID {
			return nil, fmt.Errorf("winres: duplicate resource type %d ID %d", resource.Type, resource.ID)
		}
	}

	section, relocations := buildResourceSection(ordered)

	var buf bytes.Buffer
	pointerToRawData := uint32(fileHeaderSize + sectionHeaderSize)
	pointerToRelocations := pointerToRawData + uint32(len(section))
	pointerToSymbolTable := pointerToRelocations + uint32(relocationSize*len(relocations))

	// IMAGE_FILE_HEADER. TimeDateStamp stays zero so the output is reproducible.
	write(&buf, tgt.machine)          // Machine
	write(&buf, uint16(1))            // NumberOfSections
	write(&buf, uint32(0))            // TimeDateStamp
	write(&buf, pointerToSymbolTable) // PointerToSymbolTable
	write(&buf, uint32(1))            // NumberOfSymbols
	write(&buf, uint16(0))            // SizeOfOptionalHeader
	write(&buf, uint16(0))            // Characteristics

	// IMAGE_SECTION_HEADER for .rsrc.
	buf.Write(sectionName(".rsrc"))
	write(&buf, uint32(0))                // VirtualSize (unused in an object file)
	write(&buf, uint32(0))                // VirtualAddress (assigned by the linker)
	write(&buf, uint32(len(section)))     // SizeOfRawData
	write(&buf, pointerToRawData)         // PointerToRawData
	write(&buf, pointerToRelocations)     // PointerToRelocations
	write(&buf, uint32(0))                // PointerToLinenumbers
	write(&buf, uint16(len(relocations))) // NumberOfRelocations
	write(&buf, uint16(0))                // NumberOfLinenumbers
	write(&buf, uint32(imageSCNCntInitializedData|imageSCNMemRead))

	buf.Write(section)

	// One IMAGE_RELOCATION over each leaf's OffsetToData field. The linker adds
	// the section's virtual address to the addend already stored there, which
	// turns the section-relative data offset into the RVA Windows needs.
	for _, offset := range relocations {
		write(&buf, offset)        // VirtualAddress
		write(&buf, uint32(0))     // SymbolTableIndex -> the .rsrc section symbol
		write(&buf, tgt.relocType) // Type
	}

	// One IMAGE_SYMBOL naming the section itself. Each relocation carries its
	// value in the section data, so this symbol only has to resolve.
	buf.Write(sectionName(".rsrc"))
	write(&buf, uint32(0))             // Value
	write(&buf, int16(1))              // SectionNumber (1-based)
	write(&buf, uint16(0))             // Type
	buf.WriteByte(imageSymClassStatic) // StorageClass
	buf.WriteByte(0)                   // NumberOfAuxSymbols

	// COFF requires a string table; its first four bytes are its own length.
	write(&buf, uint32(4))

	return buf.Bytes(), nil
}

// buildResourceSection lays out the .rsrc section for resources already sorted
// by type then ID: the type directory, one name directory per type, one
// language directory per resource, the data entries, and finally the data. It
// returns the section and the offset of every data entry's OffsetToData field,
// which is where each relocation applies.
//
// The directories are laid out breadth-first, so every subdirectory's offset is
// known before its parent is written.
func buildResourceSection(resources []Resource) (section []byte, relocations []uint32) {
	// Group resources by type; the input is sorted, so groups are contiguous.
	var types []uint16
	names := map[uint16][]Resource{}
	for _, resource := range resources {
		if len(types) == 0 || types[len(types)-1] != resource.Type {
			types = append(types, resource.Type)
		}
		names[resource.Type] = append(names[resource.Type], resource)
	}

	// Compute every offset first.
	nameDirOffset := map[uint16]uint32{}
	offset := uint32(dirSize + dirEntrySize*len(types)) // just past the type directory
	for _, typ := range types {
		nameDirOffset[typ] = offset
		offset += uint32(dirSize + dirEntrySize*len(names[typ]))
	}
	langDirOffset := make([]uint32, len(resources))
	for i := range resources {
		langDirOffset[i] = offset
		offset += dirSize + dirEntrySize
	}
	dataEntryOffset := make([]uint32, len(resources))
	for i := range resources {
		dataEntryOffset[i] = offset
		offset += dataEntrySize
	}
	dataOffset := make([]uint32, len(resources))
	for i, resource := range resources {
		offset = align(offset)
		dataOffset[i] = offset
		offset += uint32(len(resource.Data))
	}

	var buf bytes.Buffer

	// Level 1, keyed by resource type.
	writeDirectory(&buf, len(types))
	for _, typ := range types {
		writeSubdirectoryEntry(&buf, uint32(typ), nameDirOffset[typ])
	}

	// Level 2, keyed by resource name/ID: one directory per type.
	index := 0
	for _, typ := range types {
		writeDirectory(&buf, len(names[typ]))
		for range names[typ] {
			writeSubdirectoryEntry(&buf, uint32(resources[index].ID), langDirOffset[index])
			index++
		}
	}

	// Level 3, keyed by language: one directory per resource.
	for i := range resources {
		writeDirectory(&buf, 1)
		writeLeafEntry(&buf, langUSEnglish, dataEntryOffset[i])
	}

	// IMAGE_RESOURCE_DATA_ENTRY for each resource. OffsetToData holds the data's
	// offset within this section; its relocation promotes it to an RVA at link
	// time.
	for i, resource := range resources {
		relocations = append(relocations, dataEntryOffset[i])
		write(&buf, dataOffset[i])
		write(&buf, uint32(len(resource.Data)))
		write(&buf, uint32(0)) // CodePage
		write(&buf, uint32(0)) // Reserved
	}

	for i, resource := range resources {
		for uint32(buf.Len()) < dataOffset[i] {
			buf.WriteByte(0)
		}
		buf.Write(resource.Data)
	}
	return buf.Bytes(), relocations
}

func align(offset uint32) uint32 {
	return (offset + dataAlignment - 1) &^ (dataAlignment - 1)
}

// writeDirectory emits an IMAGE_RESOURCE_DIRECTORY holding count ID-keyed
// entries and no name-keyed entries.
func writeDirectory(buf *bytes.Buffer, count int) {
	write(buf, uint32(0))     // Characteristics
	write(buf, uint32(0))     // TimeDateStamp
	write(buf, uint16(0))     // MajorVersion
	write(buf, uint16(0))     // MinorVersion
	write(buf, uint16(0))     // NumberOfNamedEntries
	write(buf, uint16(count)) // NumberOfIdEntries
}

// writeSubdirectoryEntry points at another directory. The high bit marks the
// offset as a directory rather than a leaf.
func writeSubdirectoryEntry(buf *bytes.Buffer, id, offset uint32) {
	write(buf, id)
	write(buf, offset|0x80000000)
}

// writeLeafEntry points at an IMAGE_RESOURCE_DATA_ENTRY.
func writeLeafEntry(buf *bytes.Buffer, id, offset uint32) {
	write(buf, id)
	write(buf, offset)
}

// sectionName renders a COFF 8-byte, NUL-padded section or symbol name.
func sectionName(name string) []byte {
	out := make([]byte, 8)
	copy(out, name)
	return out
}

func write(buf *bytes.Buffer, value any) {
	if err := binary.Write(buf, binary.LittleEndian, value); err != nil {
		// Every value written here is a fixed-size type into an in-memory
		// buffer, so this cannot fail in practice.
		panic(fmt.Sprintf("winres: encoding %T: %v", value, err))
	}
}
