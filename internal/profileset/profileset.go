// Package profileset transfers saved printer files as a single collection,
// without applying Windows changes.
//
// A collection is a set (package internal/bundle), extension .ssb, carrying
// the operator's saved-printer .ssb files verbatim as members. This used to
// be a bespoke JSON document that re-encoded each profile inline -- a saved
// printer and a collection of them were two different file shapes for the
// same underlying document. They are the same shape now: a set is just
// several bundles carried in one file.
package profileset

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spilloid/spoolsmith/internal/bundle"
)

// maxMemberBytes bounds one member, generous for a profile plus the rare
// driver_package reference; an embedded driver payload is refused outright
// (see PrepareExport), so this never has to bound one.
const maxMemberBytes = 1 << 20

// PrepareExport reads and validates every top-level .ssb in directory without
// writing anything. The prepared transfer retains exactly the files shown
// during review.
func PrepareExport(directory, path string) (*Transfer, error) {
	transfer := &Transfer{operation: "export-all", source: directory}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, fmt.Errorf("saved setups: cannot read printer folder %s; choose a folder containing saved printers: %w", directory, err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), ".ssb") {
			continue
		}
		if e.Type()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("saved setups: symbolic link %q is not a saved printer", e.Name())
		}
		data, preview, err := readMember(filepath.Join(directory, e.Name()), e.Name())
		if err != nil {
			return nil, err
		}
		transfer.members = append(transfer.members, bundle.SetMember{Name: e.Name(), Data: data})
		transfer.previews = append(transfer.previews, preview)
	}
	if len(transfer.members) == 0 {
		return nil, fmt.Errorf("saved setups: no saved printers (.ssb) in %s; choose a folder containing saved printers or save a printer first", directory)
	}
	return transfer.WithDestination(path)
}

// Export is PrepareExport immediately followed by Execute, for a caller that
// does not need to review the collection first.
func Export(directory, path string) (int, error) {
	transfer, err := PrepareExport(directory, path)
	if err != nil {
		return 0, err
	}
	return transfer.Execute()
}

// Import is PrepareImport immediately followed by Execute, for a caller that
// does not need to review the collection first.
func Import(path, directory string) (int, error) {
	transfer, err := PrepareImport(path, directory)
	if err != nil {
		return 0, err
	}
	return transfer.Execute()
}

// PrepareImport opens a collection and validates every member without
// writing anything.
func PrepareImport(path, directory string) (*Transfer, error) {
	opened, err := bundle.OpenSet(path)
	if err != nil {
		return nil, err
	}
	defer opened.Close()
	transfer := &Transfer{operation: "import-all", source: path}
	for _, name := range opened.Index.Members {
		data, err := opened.MemberBytes(name)
		if err != nil {
			return nil, err
		}
		if len(data) > maxMemberBytes {
			return nil, fmt.Errorf("%s: exceeds %d bytes", name, maxMemberBytes)
		}
		_, preview, err := decodeMember(data, name)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		transfer.members = append(transfer.members, bundle.SetMember{Name: name, Data: data})
		transfer.previews = append(transfer.previews, preview)
	}
	return transfer.WithDestination(directory)
}

// readMember loads and verifies a saved printer file straight off disk.
func readMember(path, name string) ([]byte, ProfilePreview, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, ProfilePreview{}, fmt.Errorf("%s: %w", name, err)
	}
	if !info.Mode().IsRegular() || info.Size() > maxMemberBytes {
		return nil, ProfilePreview{}, fmt.Errorf("%s: expected a regular file no larger than %d bytes", name, maxMemberBytes)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, ProfilePreview{}, fmt.Errorf("%s: %w", name, err)
	}
	return decodeMember(data, name)
}

// decodeMember validates a member's bytes as a standalone bundle and
// describes it for review. Saved-setup transfer carries settings only: a
// member with an embedded driver payload is refused outright rather than
// silently carrying or silently dropping several megabytes of driver files.
func decodeMember(data []byte, name string) ([]byte, ProfilePreview, error) {
	opened, err := bundle.OpenBytes(data)
	if err != nil {
		return nil, ProfilePreview{}, fmt.Errorf("%s: %w", name, err)
	}
	defer opened.Close()
	if opened.Manifest.Driver != nil {
		return nil, ProfilePreview{}, fmt.Errorf("%s: carries an embedded driver payload, which saved-setup transfer does not carry; copy this printer to a file directly instead", name)
	}
	profile := opened.Manifest.Profile
	preview := ProfilePreview{File: name, PrinterName: profile.PrinterName, Target: profile.Target, DriverName: profile.DriverName}
	if profile.DriverPackage != nil {
		preview.Archive = profile.DriverPackage.Archive
	}
	return data, preview, nil
}
