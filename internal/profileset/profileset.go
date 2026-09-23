// Package profileset transfers saved printer files as a single printer set,
// without applying Windows changes.
//
// A printer set (package internal/bundle) is a plain .zip carrying the
// operator's saved-printer .ssb files verbatim at its top level -- including
// any driver a file embeds. A .ssb is one printer; a .zip is a set of them.
// Members are streamed through, never held in memory, because an embedded
// driver can be hundreds of megabytes.
package profileset

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spilloid/spoolsmith/internal/bundle"
)

// PrepareExport reads and validates every top-level .ssb in directory without
// writing anything. The prepared transfer records a digest of each file shown
// during review, and Execute refuses to export a file that changed since.
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
		source := filepath.Join(directory, e.Name())
		info, err := os.Stat(source)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", e.Name(), err)
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("%s: expected a regular file", e.Name())
		}
		preview, digest, err := describeMember(source, e.Name())
		if err != nil {
			return nil, err
		}
		transfer.members = append(transfer.members, member{name: e.Name(), path: source, digest: digest})
		transfer.previews = append(transfer.previews, preview)
	}
	if len(transfer.members) == 0 {
		return nil, fmt.Errorf("saved setups: no saved printers (.ssb) in %s; choose a folder containing saved printers or save a printer first", directory)
	}
	return transfer.WithDestination(path)
}

// Export is PrepareExport immediately followed by Execute, for a caller that
// does not need to review the set first.
func Export(directory, path string) (int, error) {
	transfer, err := PrepareExport(directory, path)
	if err != nil {
		return 0, err
	}
	return transfer.Execute()
}

// Import is PrepareImport immediately followed by Execute, for a caller that
// does not need to review the set first.
func Import(path, directory string) (int, error) {
	transfer, err := PrepareImport(path, directory)
	if err != nil {
		return 0, err
	}
	return transfer.Execute()
}

// PrepareImport opens a printer set and validates every member, without
// writing anything to directory. Members are checked by extracting them into
// a private working folder that is removed again before this returns.
func PrepareImport(path, directory string) (*Transfer, error) {
	set, err := bundle.OpenSet(path)
	if err != nil {
		return nil, err
	}
	defer set.Close()
	work, err := os.MkdirTemp("", "spoolsmith-import-review-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(work)
	transfer := &Transfer{operation: "import-all", source: path}
	for _, name := range set.Members {
		extracted, err := set.Extract(name, work)
		if err != nil {
			return nil, err
		}
		preview, digest, err := describeMember(extracted, name)
		os.Remove(extracted)
		if err != nil {
			return nil, err
		}
		transfer.members = append(transfer.members, member{name: name, digest: digest})
		transfer.previews = append(transfer.previews, preview)
	}
	return transfer.WithDestination(directory)
}

// describeMember validates a printer file on disk as a bundle, describes it
// for review, and returns the digest of its exact bytes.
func describeMember(path, name string) (ProfilePreview, string, error) {
	opened, err := bundle.Open(path)
	if err != nil {
		return ProfilePreview{}, "", fmt.Errorf("%s: %w", name, err)
	}
	defer opened.Close()
	if err := opened.Verify(); err != nil {
		return ProfilePreview{}, "", fmt.Errorf("%s: %w", name, err)
	}
	profile := opened.Manifest.Profile
	preview := ProfilePreview{
		File: name, PrinterName: profile.PrinterName, Target: profile.Target, DriverName: profile.DriverName,
		Driver: opened.Manifest.Driver != nil,
	}
	if profile.DriverPackage != nil {
		preview.Archive = profile.DriverPackage.Archive
	}
	digest, err := fileDigest(path)
	if err != nil {
		return ProfilePreview{}, "", fmt.Errorf("%s: %w", name, err)
	}
	return preview, digest, nil
}

func fileDigest(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	return readerDigest(f)
}

func readerDigest(r io.Reader) (string, error) {
	digest := sha256.New()
	if _, err := io.Copy(digest, r); err != nil {
		return "", err
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}
