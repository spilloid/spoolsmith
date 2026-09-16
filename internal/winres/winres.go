// Package winres builds the Windows resource object (.syso) that embeds an
// application manifest directly into a Go-linked PE image.
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
// The object this package emits is deliberately minimal: one .rsrc section
// holding a three-level resource tree (type, name, language) with a single
// manifest leaf, one relocation, and one section symbol. The Go linker turns
// the relocation's in-place addend into the resource RVA (see
// cmd/link/internal/ld.addpersrc), so the leaf records the manifest's offset
// inside the section rather than a placeholder.
package winres

import (
	"bytes"
	"encoding/binary"
	"fmt"
)

// Resource type and identifier constants from the PE/COFF specification.
const (
	rtManifest = 24 // RT_MANIFEST
	// CREATEPROCESS_MANIFEST_RESOURCE_ID: the manifest Windows applies to the
	// process itself, as opposed to an isolated-COM or side-by-side manifest.
	createProcessManifestID = 1
	// US English. Manifests are not localized, but the resource tree's language
	// level is not optional, and Windows looks here first.
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

	// Offsets within the .rsrc section, fixed because the tree always holds
	// exactly one manifest.
	typeDirOffset  = 0
	nameDirOffset  = typeDirOffset + dirSize + dirEntrySize // 24
	langDirOffset  = nameDirOffset + dirSize + dirEntrySize // 48
	dataEntryOff   = langDirOffset + dirSize + dirEntrySize // 72
	manifestOffset = dataEntryOff + dataEntrySize           // 88
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

// SupportedArches reports the GOARCH values ManifestObject accepts.
func SupportedArches() []string {
	return []string{"386", "amd64", "arm64"}
}

// ManifestObject returns a COFF object file that embeds manifest as the
// process's RT_MANIFEST resource when linked into a goarch PE binary. The
// result is deterministic: the same manifest and architecture always produce
// byte-identical output, so the generated file can be committed and checked for
// drift.
func ManifestObject(manifest []byte, goarch string) ([]byte, error) {
	tgt, ok := targets[goarch]
	if !ok {
		return nil, fmt.Errorf("winres: unsupported architecture %q (want one of %v)", goarch, SupportedArches())
	}
	if len(manifest) == 0 {
		return nil, fmt.Errorf("winres: manifest is empty")
	}

	section := buildResourceSection(manifest)

	var buf bytes.Buffer
	pointerToRawData := uint32(fileHeaderSize + sectionHeaderSize)
	pointerToRelocations := pointerToRawData + uint32(len(section))
	pointerToSymbolTable := pointerToRelocations + relocationSize

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
	write(&buf, uint32(0))            // VirtualSize (unused in an object file)
	write(&buf, uint32(0))            // VirtualAddress (assigned by the linker)
	write(&buf, uint32(len(section))) // SizeOfRawData
	write(&buf, pointerToRawData)     // PointerToRawData
	write(&buf, pointerToRelocations) // PointerToRelocations
	write(&buf, uint32(0))            // PointerToLinenumbers
	write(&buf, uint16(1))            // NumberOfRelocations
	write(&buf, uint16(0))            // NumberOfLinenumbers
	write(&buf, uint32(imageSCNCntInitializedData|imageSCNMemRead))

	buf.Write(section)

	// One IMAGE_RELOCATION over the leaf's OffsetToData field. The linker adds
	// the section's virtual address to the addend already stored there, which
	// turns the section-relative manifest offset into the RVA Windows needs.
	write(&buf, uint32(dataEntryOff)) // VirtualAddress
	write(&buf, uint32(0))            // SymbolTableIndex -> the .rsrc section symbol
	write(&buf, tgt.relocType)        // Type

	// One IMAGE_SYMBOL naming the section itself. The relocation carries its
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

// buildResourceSection lays out the .rsrc section: a directory level each for
// resource type, name and language, then the leaf and the manifest bytes.
func buildResourceSection(manifest []byte) []byte {
	var buf bytes.Buffer

	// Level 1, keyed by resource type.
	writeDirectory(&buf)
	writeSubdirectoryEntry(&buf, rtManifest, nameDirOffset)
	// Level 2, keyed by resource name/ID.
	writeDirectory(&buf)
	writeSubdirectoryEntry(&buf, createProcessManifestID, langDirOffset)
	// Level 3, keyed by language.
	writeDirectory(&buf)
	writeLeafEntry(&buf, langUSEnglish, dataEntryOff)

	// IMAGE_RESOURCE_DATA_ENTRY. OffsetToData holds the manifest's offset within
	// this section; the relocation above promotes it to an RVA at link time.
	write(&buf, uint32(manifestOffset))
	write(&buf, uint32(len(manifest)))
	write(&buf, uint32(0)) // CodePage
	write(&buf, uint32(0)) // Reserved

	buf.Write(manifest)
	return buf.Bytes()
}

// writeDirectory emits an IMAGE_RESOURCE_DIRECTORY holding exactly one
// ID-keyed entry and no name-keyed entries.
func writeDirectory(buf *bytes.Buffer) {
	write(buf, uint32(0)) // Characteristics
	write(buf, uint32(0)) // TimeDateStamp
	write(buf, uint16(0)) // MajorVersion
	write(buf, uint16(0)) // MinorVersion
	write(buf, uint16(0)) // NumberOfNamedEntries
	write(buf, uint16(1)) // NumberOfIdEntries
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
