package bundle

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// A set is several printer files (.ssb bundles) carried together as one plain
// zip archive -- exactly what Explorer's "Compress to ZIP folder" produces when
// an operator selects a handful of .ssb files. There is no index file: the
// archive's own entry list is the membership, so a hand-made zip and one
// SpoolSmith wrote are the same thing. The optional note lives in the zip
// archive comment.
//
// A .ssb is one printer; a .zip is a set of them. Each member is copied in
// and out verbatim, driver payload and all, and is only ever trusted after its
// own Open+Verify -- the set adds no trust of its own.
const (
	SetExt = ".zip"

	MaxSetMembers = 1000
	// MaxSetBytes bounds the total uncompressed size of every member. It is a
	// sanity limit against a hostile archive, not a memory budget: members
	// are streamed, never held in memory.
	MaxSetBytes = 8 << 30 // 8 GiB
	// maxSetMemberBytes bounds one member: the largest payload a bundle may
	// carry plus generous room for its manifest and zip overhead.
	maxSetMemberBytes = MaxPayloadBytes + 64<<20
	// maxSetNoteBytes is the zip format's own archive-comment limit.
	maxSetNoteBytes = 65535
)

// validateMemberName applies the same plain-filename discipline a
// destination directory needs, not the path-with-slashes discipline a
// payload entry gets: a member is a display name an operator will see, and
// later a real file on disk, never a directory structure.
func validateMemberName(name string) error {
	if name == "" || len(name) > 200 || strings.TrimSpace(name) != name {
		return fmt.Errorf("bundle: unsafe member filename %q", name)
	}
	if strings.ContainsAny(name, `/\:<>"|?*`) {
		return fmt.Errorf("bundle: unsafe member filename %q", name)
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("bundle: unsafe member filename %q", name)
		}
	}
	stem := strings.ToUpper(strings.SplitN(name, ".", 2)[0])
	reserved := stem == "CON" || stem == "PRN" || stem == "AUX" || stem == "NUL" ||
		(len(stem) == 4 && (strings.HasPrefix(stem, "COM") || strings.HasPrefix(stem, "LPT")) && stem[3] >= '0' && stem[3] <= '9')
	if reserved {
		return fmt.Errorf("bundle: unsafe member filename %q", name)
	}
	if !strings.HasSuffix(strings.ToLower(name), ".ssb") {
		return fmt.Errorf("bundle: member %q is not a .ssb file", name)
	}
	return nil
}

// SetMember is one printer file to carry in a set.
type SetMember struct {
	Name string // "Office.ssb" -- a plain filename, exactly as it will read back out
	Path string // the source .ssb on disk, copied verbatim (streamed, not loaded into memory)
}

// WriteSet packages several printer files into one set at path. It never
// overwrites an existing file, and removes its own partial file on any
// failure. Each member is validated as a bundle (Open+Verify) before it is
// added; a duplicate name (case-insensitive, as Windows compares them) is
// refused rather than silently renamed.
func WriteSet(path, note string, members []SetMember) error {
	if len(members) == 0 {
		return errors.New("bundle: a printer set needs at least one printer file")
	}
	if len(members) > MaxSetMembers {
		return fmt.Errorf("bundle: a printer set holds at most %d printer files (got %d)", MaxSetMembers, len(members))
	}
	if len(note) > maxSetNoteBytes {
		return fmt.Errorf("bundle: the set note is longer than %d bytes", maxSetNoteBytes)
	}
	seen := make(map[string]bool, len(members))
	var total int64
	for _, m := range members {
		if err := validateMemberName(m.Name); err != nil {
			return err
		}
		key := strings.ToLower(m.Name)
		if seen[key] {
			return fmt.Errorf("bundle: the set would contain %q more than once", m.Name)
		}
		seen[key] = true
		info, err := os.Stat(m.Path)
		if err != nil {
			return fmt.Errorf("bundle: %s: %w", m.Name, err)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("bundle: %s: %s is not a regular file", m.Name, m.Path)
		}
		if info.Size() > maxSetMemberBytes {
			return fmt.Errorf("bundle: %s is larger than any printer file can be (%d bytes)", m.Name, info.Size())
		}
		total += info.Size()
		if total > MaxSetBytes {
			return fmt.Errorf("bundle: the set would exceed the %d byte limit", int64(MaxSetBytes))
		}
	}

	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	// A partially written set is worse than none: it looks applyable.
	committed := false
	defer func() {
		f.Close()
		if !committed {
			os.Remove(path)
		}
	}()

	zw := zip.NewWriter(f)
	if note != "" {
		if err := zw.SetComment(note); err != nil {
			return err
		}
	}
	for _, m := range members {
		if err := addSetMember(zw, m); err != nil {
			return err
		}
	}
	if err := zw.Close(); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	committed = true
	return nil
}

// addSetMember validates one member as a bundle and streams it, verbatim,
// into the archive -- through the same open handle, so what was checked is
// what was copied.
func addSetMember(zw *zip.Writer, m SetMember) error {
	src, err := os.Open(m.Path)
	if err != nil {
		return fmt.Errorf("bundle: %s: %w", m.Name, err)
	}
	defer src.Close()
	info, err := src.Stat()
	if err != nil {
		return fmt.Errorf("bundle: %s: %w", m.Name, err)
	}
	size := info.Size()
	reader, err := zip.NewReader(src, size)
	if err != nil {
		return fmt.Errorf("bundle: %s is not a printer file: %w", m.Name, err)
	}
	opened, err := openReader(reader)
	if err != nil {
		return fmt.Errorf("bundle: %s: %w", m.Name, err)
	}
	if err := opened.Verify(); err != nil {
		return fmt.Errorf("bundle: %s: %w", m.Name, err)
	}
	// Bundles are already compressed zips; storing them avoids paying to
	// deflate hundreds of megabytes of driver files a second time.
	entry, err := zw.CreateHeader(&zip.FileHeader{Name: m.Name, Method: zip.Store, Modified: info.ModTime()})
	if err != nil {
		return err
	}
	copied, err := io.Copy(entry, io.NewSectionReader(src, 0, size))
	if err != nil {
		return fmt.Errorf("bundle: copy %s: %w", m.Name, err)
	}
	if copied != size {
		return fmt.Errorf("bundle: %s changed size while it was being copied", m.Name)
	}
	return nil
}

// Set is an opened, structurally validated set. Members are only checked as
// bundles when extracted.
type Set struct {
	Path    string
	Note    string
	Members []string // entry names in archive order

	reader  *zip.ReadCloser
	entries map[string]*zip.File
}

// OpenSet opens a set and checks its shape, failing closed: every file entry
// must be a top-level .ssb with a safe name, directory entries are ignored,
// and anything else -- a nested path, a non-.ssb file, no members at all, too
// many, or two names differing only by case -- is refused with a plain
// explanation.
func OpenSet(path string) (*Set, error) {
	reader, err := zip.OpenReader(path)
	if err != nil {
		return nil, fmt.Errorf("%s is not a readable printer set (.zip): %w", filepath.Base(path), err)
	}
	members, entries, err := setMembers(&reader.Reader, filepath.Base(path))
	if err != nil {
		reader.Close()
		return nil, err
	}
	return &Set{Path: path, Note: reader.Comment, Members: members, reader: reader, entries: entries}, nil
}

// setMembers validates a zip as a set and returns its member names in archive
// order.
func setMembers(reader *zip.Reader, label string) ([]string, map[string]*zip.File, error) {
	var members []string
	entries := make(map[string]*zip.File)
	seen := make(map[string]string)
	var total uint64
	for _, f := range reader.File {
		name := f.Name
		if strings.HasSuffix(name, "/") || f.FileInfo().IsDir() {
			continue // directory entries carry no content
		}
		if name == ManifestName {
			return nil, nil, fmt.Errorf("%s is a single printer file, not a printer set", label)
		}
		if strings.ContainsAny(name, `/\`) {
			return nil, nil, fmt.Errorf("%s is not a usable printer set: it contains %q inside a folder; a set holds .ssb printer files at its top level only", label, name)
		}
		if !strings.EqualFold(filepath.Ext(name), ".ssb") {
			return nil, nil, fmt.Errorf("%s is not a usable printer set: it contains %q, which is not a printer file (.ssb)", label, name)
		}
		if err := validateMemberName(name); err != nil {
			return nil, nil, fmt.Errorf("%s is not a usable printer set: %w", label, err)
		}
		key := strings.ToLower(name)
		if previous, dup := seen[key]; dup {
			return nil, nil, fmt.Errorf("%s is not a usable printer set: it contains %q and %q, which are the same file name on Windows", label, previous, name)
		}
		seen[key] = name
		if f.UncompressedSize64 > maxSetMemberBytes {
			return nil, nil, fmt.Errorf("%s is not a usable printer set: %q is larger than any printer file can be", label, name)
		}
		total += f.UncompressedSize64
		if total > MaxSetBytes {
			return nil, nil, fmt.Errorf("%s is not a usable printer set: its printer files exceed the %d byte limit", label, int64(MaxSetBytes))
		}
		members = append(members, name)
		entries[name] = f
		if len(members) > MaxSetMembers {
			return nil, nil, fmt.Errorf("%s is not a usable printer set: it holds more than %d printer files", label, MaxSetMembers)
		}
	}
	if len(members) == 0 {
		return nil, nil, fmt.Errorf("%s is not a usable printer set: it contains no printer files (.ssb)", label)
	}
	return members, entries, nil
}

// Close releases the underlying archive.
func (s *Set) Close() error {
	if s == nil || s.reader == nil {
		return nil
	}
	return s.reader.Close()
}

// Extract streams one member, verbatim, to dir/name (never overwriting an
// existing file), then opens and verifies it as a bundle. A member that is
// not a valid bundle is removed again and reported. It returns the written
// file's path.
func (s *Set) Extract(name, dir string) (string, error) {
	entry, ok := s.entries[name]
	if !ok {
		return "", fmt.Errorf("%s has no printer file named %q", filepath.Base(s.Path), name)
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	target := filepath.Join(dir, name)
	if err := extractEntry(entry, target); err != nil {
		return "", fmt.Errorf("%s: %w", name, err)
	}
	opened, err := Open(target)
	if err == nil {
		err = opened.Verify()
		opened.Close()
	}
	if err != nil {
		os.Remove(target)
		return "", fmt.Errorf("%s is not a valid printer file: %w", name, err)
	}
	return target, nil
}

func extractEntry(entry *zip.File, target string) (err error) {
	rc, err := entry.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer func() {
		closeErr := out.Close()
		if err == nil {
			err = closeErr
		}
		if err != nil {
			os.Remove(target)
		}
	}()
	// Read one byte past the declared size so an entry that decompresses to
	// more than it claims is caught rather than silently truncated.
	written, err := io.Copy(out, io.LimitReader(rc, int64(entry.UncompressedSize64)+1))
	if err != nil {
		return err
	}
	if uint64(written) != entry.UncompressedSize64 {
		return fmt.Errorf("decompressed to %d bytes but the archive declares %d", written, entry.UncompressedSize64)
	}
	return nil
}

// IsSet reports whether path is a printer set rather than a single printer
// file, by content rather than extension: a zip carrying the bundle manifest
// is a single printer (false); a zip whose entries are all .ssb members is a
// set (true); anything else, or an unreadable file, is an error.
func IsSet(path string) (bool, error) {
	reader, err := zip.OpenReader(path)
	if err != nil {
		return false, fmt.Errorf("%s is not a printer file (.ssb) or printer set (.zip): %w", filepath.Base(path), err)
	}
	defer reader.Close()
	for _, f := range reader.File {
		if f.Name == ManifestName {
			return false, nil
		}
	}
	if _, _, err := setMembers(&reader.Reader, filepath.Base(path)); err != nil {
		return false, err
	}
	return true, nil
}

// DefaultSetName is a timestamped set filename for when the operator names
// none, e.g. SpoolSmith-printers-20260923-141500.zip.
func DefaultSetName(now time.Time) string {
	return "SpoolSmith-printers-" + now.Format("20060102-150405") + SetExt
}
