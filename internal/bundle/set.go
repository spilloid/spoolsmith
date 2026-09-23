package bundle

import (
	"archive/zip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// A set is several already-validated bundles carried in one file -- the same
// container a single printer file uses, so a bulk transfer of saved setups
// never becomes a second, different file shape. `bundle inspect`/`apply`
// distinguish a set from a single bundle by which top-level entry is present
// (manifest.json vs set.json), not by extension: both are .ssb.
const (
	SetVersion    = 1
	SetIndexName  = "set.json"
	SetMemberPath = "members/"

	MaxSetMembers = 1000
	MaxSetBytes   = 64 << 20 // 64 MiB total, generous for many profiles and the rare embedded driver
)

// SetIndex is a set's own description of itself. It carries no profile
// content directly -- each listed name is a nested, independently valid
// bundle, byte-identical to the file it came from.
type SetIndex struct {
	Version   int      `json:"version"`
	Created   string   `json:"created_utc"`
	CreatedBy string   `json:"created_by,omitempty"`
	Note      string   `json:"note,omitempty"`
	Members   []string `json:"members"`
}

func (i SetIndex) validate() error {
	if i.Version != SetVersion {
		return fmt.Errorf("bundle: unsupported set version %d (expected %d)", i.Version, SetVersion)
	}
	if len(i.Members) == 0 {
		return errors.New("bundle: set carries no members")
	}
	if len(i.Members) > MaxSetMembers {
		return fmt.Errorf("bundle: set carries %d members, above the %d limit", len(i.Members), MaxSetMembers)
	}
	seen := make(map[string]bool, len(i.Members))
	for _, name := range i.Members {
		if err := validateMemberName(name); err != nil {
			return err
		}
		key := strings.ToLower(name)
		if seen[key] {
			return fmt.Errorf("bundle: set lists %q more than once", name)
		}
		seen[key] = true
	}
	return nil
}

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

// SetMember is one bundle to embed, already read into memory and already
// validated as a standalone bundle by the caller -- WriteSet packages bytes,
// it does not itself decide what belongs in the set.
type SetMember struct {
	Name string // e.g. "Office.ssb", exactly as it will read back out
	Data []byte // the member's own complete, verbatim bundle bytes
}

// WriteSet packages several already-validated bundles into one file. It
// never overwrites an existing file, matching Write's own guarantee for a
// single bundle.
func WriteSet(path string, index SetIndex, members []SetMember) error {
	index.Version = SetVersion
	if strings.TrimSpace(index.Created) == "" {
		index.Created = time.Now().UTC().Format(time.RFC3339)
	}
	index.Members = make([]string, len(members))
	var total int64
	seen := make(map[string]bool, len(members))
	for i, m := range members {
		index.Members[i] = m.Name
		if seen[strings.ToLower(m.Name)] {
			return fmt.Errorf("bundle: set lists %q more than once", m.Name)
		}
		seen[strings.ToLower(m.Name)] = true
		total += int64(len(m.Data))
	}
	if total > MaxSetBytes {
		return fmt.Errorf("bundle: set exceeds the %d byte limit", int64(MaxSetBytes))
	}
	if err := index.validate(); err != nil {
		return err
	}

	indexJSON, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return err
	}
	indexJSON = append(indexJSON, '\n')

	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		f.Close()
		if !committed {
			os.Remove(path)
		}
	}()

	zw := zip.NewWriter(f)
	indexEntry, err := zw.Create(SetIndexName)
	if err != nil {
		return err
	}
	if _, err := indexEntry.Write(indexJSON); err != nil {
		return err
	}
	for _, m := range members {
		entry, err := zw.Create(SetMemberPath + m.Name)
		if err != nil {
			return err
		}
		if _, err := entry.Write(m.Data); err != nil {
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

// Set is an opened, index-validated set. Members are read (and their own
// bundle structure validated) on demand via Open.
type Set struct {
	Index  SetIndex
	Path   string
	reader *zip.ReadCloser
}

// OpenSet reads and validates a set's index. It does not open any member --
// each member is only as trustworthy as its own Bundle.Validate/Verify, run
// when that member is actually opened.
func OpenSet(path string) (*Set, error) {
	reader, err := zip.OpenReader(path)
	if err != nil {
		return nil, fmt.Errorf("bundle: open %q: %w", path, err)
	}
	entry, err := findEntry(&reader.Reader, SetIndexName)
	if err != nil {
		reader.Close()
		return nil, err
	}
	rc, err := entry.Open()
	if err != nil {
		reader.Close()
		return nil, err
	}
	decoder := json.NewDecoder(io.LimitReader(rc, MaxManifestBytes+1))
	decoder.DisallowUnknownFields()
	var idx SetIndex
	decodeErr := decoder.Decode(&idx)
	rc.Close()
	if decodeErr != nil {
		reader.Close()
		return nil, fmt.Errorf("bundle: decode set index: %w", decodeErr)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		reader.Close()
		return nil, errors.New("bundle: set index has trailing data or is oversized")
	}
	if err := idx.validate(); err != nil {
		reader.Close()
		return nil, err
	}
	expected := map[string]bool{SetIndexName: false}
	for _, name := range idx.Members {
		expected[SetMemberPath+name] = false
	}
	for _, f := range reader.File {
		if strings.HasSuffix(f.Name, "/") {
			continue
		}
		if _, ok := expected[f.Name]; !ok {
			reader.Close()
			return nil, fmt.Errorf("bundle: set contains %q, which the index does not list", f.Name)
		}
		if expected[f.Name] {
			reader.Close()
			return nil, fmt.Errorf("bundle: set contains %q more than once", f.Name)
		}
		expected[f.Name] = true
	}
	for name, present := range expected {
		if !present {
			reader.Close()
			return nil, fmt.Errorf("bundle: set is missing %q, which the index lists", name)
		}
	}
	return &Set{Index: idx, Path: path, reader: reader}, nil
}

// Close releases the underlying archive.
func (s *Set) Close() error {
	if s == nil || s.reader == nil {
		return nil
	}
	return s.reader.Close()
}

// MemberBytes returns one member's bytes exactly as embedded, unparsed.
func (s *Set) MemberBytes(name string) ([]byte, error) {
	entry, err := findEntry(&s.reader.Reader, SetMemberPath+name)
	if err != nil {
		return nil, fmt.Errorf("bundle: set has no member %q", name)
	}
	rc, err := entry.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	limit := int64(MaxSetBytes) + 1
	data, err := io.ReadAll(io.LimitReader(rc, limit))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) >= limit {
		return nil, fmt.Errorf("bundle: member %q exceeds the set size limit", name)
	}
	return data, nil
}

// Open reads one member's bytes and opens it as a standalone bundle, so
// every existing single-bundle check (Manifest.Validate, Verify, driver
// staging) runs on it unmodified.
func (s *Set) Open(name string) (*Bundle, error) {
	data, err := s.MemberBytes(name)
	if err != nil {
		return nil, err
	}
	return OpenBytes(data)
}
