package bundle

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Write creates a new bundle at path. It never overwrites an existing file:
// an operator's captured setup is inventory, and silently replacing it is the
// same mistake `profile capture` already refuses to make.
//
// When payloadRoot is non-empty, every regular file beneath it is written
// under payload/ and hashed, and the resulting file list replaces whatever the
// caller put in m.Driver.Files — the archive's own contents are the single
// source of truth for the manifest, so the two cannot drift.
func Write(bundlePath string, m Manifest, payloadRoot string) error {
	m.Version = Version
	if strings.TrimSpace(m.Created) == "" {
		m.Created = time.Now().UTC().Format(time.RFC3339)
	}

	if payloadRoot == "" {
		if m.Driver != nil {
			return errors.New("bundle: a driver payload was described but no payload directory was given")
		}
	} else {
		if m.Driver == nil {
			return errors.New("bundle: a payload directory was given but the manifest describes no driver payload")
		}
		files, err := collectPayload(payloadRoot)
		if err != nil {
			return err
		}
		driver := *m.Driver
		driver.Files = files
		m.Driver = &driver
	}
	if err := m.Validate(); err != nil {
		return err
	}

	manifestJSON, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	manifestJSON = append(manifestJSON, '\n')

	if err := os.MkdirAll(filepath.Dir(bundlePath), 0700); err != nil {
		return err
	}
	f, err := os.OpenFile(bundlePath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	// A partially written bundle is worse than none: it looks applyable.
	// Remove it unless every entry was written and the archive closed cleanly.
	committed := false
	defer func() {
		f.Close()
		if !committed {
			os.Remove(bundlePath)
		}
	}()

	zw := zip.NewWriter(f)
	manifestEntry, err := zw.Create(ManifestName)
	if err != nil {
		return err
	}
	if _, err := manifestEntry.Write(manifestJSON); err != nil {
		return err
	}
	if m.Driver != nil {
		for _, file := range m.Driver.Files {
			source, err := os.Open(filepath.Join(payloadRoot, filepath.FromSlash(file.Path)))
			if err != nil {
				return err
			}
			entry, err := zw.Create(PayloadPrefix + file.Path)
			if err != nil {
				source.Close()
				return err
			}
			_, copyErr := io.Copy(entry, source)
			source.Close()
			if copyErr != nil {
				return copyErr
			}
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

// collectPayload walks payloadRoot and returns every regular file, hashed, in
// a stable order so two clones of the same driver produce the same manifest.
func collectPayload(root string) ([]File, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("bundle: payload path %q is not a directory", root)
	}
	var files []File
	var total int64
	err = filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if fi.IsDir() {
			return nil
		}
		// Symlinks and devices are not driver files; refuse rather than
		// silently following something out of the export directory.
		if !fi.Mode().IsRegular() {
			return fmt.Errorf("bundle: payload entry %q is not a regular file", p)
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		slash := filepath.ToSlash(rel)
		if err := validatePayloadPath(slash); err != nil {
			return err
		}
		total += fi.Size()
		if total > MaxPayloadBytes {
			return fmt.Errorf("bundle: driver payload exceeds the %d byte limit", int64(MaxPayloadBytes))
		}
		if len(files) >= MaxPayloadFiles {
			return fmt.Errorf("bundle: driver payload exceeds the %d file limit", MaxPayloadFiles)
		}
		sum, err := hashFile(p)
		if err != nil {
			return err
		}
		files = append(files, File{Path: slash, Size: fi.Size(), SHA256: sum})
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("bundle: payload directory %q contains no files", root)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, nil
}

func hashFile(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	digest := sha256.New()
	if _, err := io.Copy(digest, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

// Bundle is an opened, manifest-validated bundle. Its payload has not been
// verified until Verify or Extract runs.
type Bundle struct {
	Manifest Manifest
	// Path is the source file's path, empty for a bundle opened from memory
	// (OpenBytes) rather than from disk.
	Path   string
	reader *zip.Reader
	closer io.Closer // nil for a bundle opened from memory
}

// Open reads and validates a bundle's manifest from a file. It does not
// extract anything.
func Open(bundlePath string) (*Bundle, error) {
	rc, err := zip.OpenReader(bundlePath)
	if err != nil {
		return nil, fmt.Errorf("bundle: open %q: %w", bundlePath, err)
	}
	b, err := openReader(&rc.Reader)
	if err != nil {
		rc.Close()
		return nil, err
	}
	b.Path = bundlePath
	b.closer = rc
	return b, nil
}

// OpenBytes reads and validates a bundle already held in memory, such as one
// member of a saved-setups collection extracted without ever touching disk.
// It shares every check Open makes; only the source differs.
func OpenBytes(data []byte) (*Bundle, error) {
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("bundle: open: %w", err)
	}
	return openReader(reader)
}

func openReader(reader *zip.Reader) (*Bundle, error) {
	manifestFile, err := findEntry(reader, ManifestName)
	if err != nil {
		return nil, err
	}
	rc, err := manifestFile.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	decoder := json.NewDecoder(io.LimitReader(rc, MaxManifestBytes+1))
	decoder.DisallowUnknownFields()
	var m Manifest
	if err := decoder.Decode(&m); err != nil {
		return nil, fmt.Errorf("bundle: decode manifest: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, errors.New("bundle: manifest has trailing data or is oversized")
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	if err := checkEntries(reader, m); err != nil {
		return nil, err
	}
	return &Bundle{Manifest: m, reader: reader}, nil
}

// Close releases the underlying archive. A bundle opened with OpenBytes has
// nothing to release.
func (b *Bundle) Close() error {
	if b == nil || b.closer == nil {
		return nil
	}
	return b.closer.Close()
}

// checkEntries confirms the archive contains exactly what the manifest
// describes: no unlisted entries an operator would not see in `bundle inspect`,
// and no listed entry that is missing.
func checkEntries(reader *zip.Reader, m Manifest) error {
	expected := map[string]bool{ManifestName: false}
	if m.Driver != nil {
		for _, f := range m.Driver.Files {
			expected[PayloadPrefix+f.Path] = false
		}
	}
	for _, entry := range reader.File {
		name := entry.Name
		if strings.HasSuffix(name, "/") {
			continue // directory entries carry no content
		}
		if _, ok := expected[name]; !ok {
			return fmt.Errorf("bundle: archive contains %q, which the manifest does not list", name)
		}
		if expected[name] {
			return fmt.Errorf("bundle: archive contains %q more than once", name)
		}
		expected[name] = true
	}
	for name, present := range expected {
		if !present {
			return fmt.Errorf("bundle: archive is missing %q, which the manifest lists", name)
		}
	}
	return nil
}

func findEntry(reader *zip.Reader, name string) (*zip.File, error) {
	for _, entry := range reader.File {
		if entry.Name == name {
			return entry, nil
		}
	}
	return nil, fmt.Errorf("bundle: archive has no %q entry", name)
}

// Verify reads every payload entry and confirms its size and hash match the
// manifest. It writes nothing.
func (b *Bundle) Verify() error {
	if b.Manifest.Driver == nil {
		return nil
	}
	for _, file := range b.Manifest.Driver.Files {
		if _, err := b.readPayload(file, io.Discard); err != nil {
			return err
		}
	}
	return nil
}

// readPayload streams one verified payload entry into sink. It refuses to
// report success unless the decompressed bytes match the manifest exactly, so
// a caller can never act on partially verified content.
func (b *Bundle) readPayload(file File, sink io.Writer) (int64, error) {
	entry, err := findEntry(b.reader, PayloadPrefix+file.Path)
	if err != nil {
		return 0, err
	}
	rc, err := entry.Open()
	if err != nil {
		return 0, err
	}
	defer rc.Close()
	digest := sha256.New()
	// Read one byte past the declared size so an entry that decompresses to
	// more than the manifest claims is caught rather than silently truncated.
	written, err := io.Copy(io.MultiWriter(sink, digest), io.LimitReader(rc, file.Size+1))
	if err != nil {
		return written, fmt.Errorf("bundle: read %q: %w", file.Path, err)
	}
	if written != file.Size {
		return written, fmt.Errorf("bundle: %q is %d bytes but the manifest declares %d", file.Path, written, file.Size)
	}
	if got := hex.EncodeToString(digest.Sum(nil)); !strings.EqualFold(got, file.SHA256) {
		return written, fmt.Errorf("bundle: %q failed its SHA-256 check (expected %s, got %s)", file.Path, file.SHA256, got)
	}
	return written, nil
}

// Extract verifies and writes the driver payload into destDir, which must not
// already exist. Nothing is written unless every entry verifies, and a failed
// extraction leaves no partial payload behind for a later step to stage.
func (b *Bundle) Extract(destDir string) (err error) {
	if b.Manifest.Driver == nil {
		return errors.New("bundle: this bundle carries no driver payload")
	}
	if err := os.Mkdir(destDir, 0700); err != nil {
		return err
	}
	defer func() {
		if err != nil {
			os.RemoveAll(destDir)
		}
	}()
	root, err := filepath.Abs(destDir)
	if err != nil {
		return err
	}
	for _, file := range b.Manifest.Driver.Files {
		target := filepath.Join(root, filepath.FromSlash(file.Path))
		// Defense in depth: the manifest path was validated, but confirm the
		// resolved destination is still inside the extraction root.
		if target != root && !strings.HasPrefix(target, root+string(os.PathSeparator)) {
			return fmt.Errorf("bundle: %q resolves outside the extraction directory", file.Path)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return err
		}
		_, readErr := b.readPayload(file, out)
		closeErr := out.Close()
		if readErr != nil {
			return readErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}

// INFPath returns the extracted INF's absolute path for a given extraction
// directory, after Extract has run.
func (b *Bundle) INFPath(destDir string) (string, error) {
	if b.Manifest.Driver == nil {
		return "", errors.New("bundle: this bundle carries no driver payload")
	}
	root, err := filepath.Abs(destDir)
	if err != nil {
		return "", err
	}
	return filepath.Join(root, filepath.FromSlash(b.Manifest.Driver.INF)), nil
}

// TotalPayloadBytes reports the declared payload size, for display.
func (m Manifest) TotalPayloadBytes() int64 {
	if m.Driver == nil {
		return 0
	}
	var total int64
	for _, f := range m.Driver.Files {
		total += f.Size
	}
	return total
}
