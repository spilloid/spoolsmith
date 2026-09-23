package profileset

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spilloid/spoolsmith/internal/bundle"
)

// TransferScope is the same handoff guidance in CLI previews and desktop review.
const TransferScope = "Saves printer files exactly as they are, including any driver a file carries; no printers are installed or changed. A driver_package reference (a separate local vendor archive) travels as a path only: copy the archive too, preserving its path relative to the destination folder. Absolute archive paths must remain available or be updated before use."

// ProfilePreview describes one member for review, without exposing its raw
// bundle bytes.
type ProfilePreview struct {
	File        string `json:"file"`
	PrinterName string `json:"printer_name"`
	Target      string `json:"target"`
	DriverName  string `json:"driver_name"`
	// Driver reports that the printer file carries its own embedded driver.
	Driver  bool   `json:"driver"`
	Archive string `json:"archive,omitempty"`
}

type Preview struct {
	Operation   string           `json:"operation"`
	Source      string           `json:"source"`
	Destination string           `json:"destination"`
	Count       int              `json:"count"`
	Profiles    []ProfilePreview `json:"profiles"`
	Conflicts   []string         `json:"conflicts"`
	Scope       string           `json:"scope"`
}

// member is one reviewed printer file: its name in the set, its source path
// (export only), and the digest of the exact bytes the operator reviewed.
type member struct {
	name   string
	path   string
	digest string
}

// Transfer owns a validated review. Neither previews nor destination changes
// reload the printers that the operator reviewed, and Execute refuses to
// write anything whose bytes differ from what was reviewed.
type Transfer struct {
	operation   string
	source      string
	destination string
	members     []member
	previews    []ProfilePreview
	conflicts   []string
}

// WithDestination checks all names without creating a folder or writing files.
// Conflicts are review data, so an operator can choose another destination.
func (t *Transfer) WithDestination(destination string) (*Transfer, error) {
	if strings.TrimSpace(destination) == "" {
		return nil, fmt.Errorf("saved setups: choose a destination first")
	}
	next := *t
	next.destination = destination
	next.conflicts = nil
	directory := destination
	names := make([]string, 0, len(t.members))
	if t.operation == "export-all" {
		if !strings.EqualFold(filepath.Ext(destination), bundle.SetExt) {
			return nil, fmt.Errorf("saved setups: %s must be a .zip file; saved printers are exported together as one printer set", destination)
		}
		directory = filepath.Dir(destination)
		source, err := os.Stat(t.source)
		if err != nil {
			return nil, err
		}
		dest, err := os.Stat(directory)
		if err != nil {
			return nil, fmt.Errorf("saved setups: choose an existing export folder: %w", err)
		}
		if os.SameFile(source, dest) {
			return nil, fmt.Errorf("saved setups: choose an export destination outside the saved-setup folder")
		}
		names = append(names, filepath.Base(destination))
	} else {
		for _, m := range t.members {
			names = append(names, m.name)
		}
	}
	entries, err := os.ReadDir(directory)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("saved setups: cannot read destination folder: %w", err)
	}
	existing := make(map[string]bool, len(entries))
	for _, entry := range entries {
		existing[strings.ToLower(entry.Name())] = true
	}
	for _, name := range names {
		if existing[strings.ToLower(name)] {
			next.conflicts = append(next.conflicts, name)
		}
	}
	return &next, nil
}

func (t *Transfer) Preview() Preview {
	return Preview{
		Operation: t.operation, Source: t.source, Destination: t.destination,
		Count: len(t.members), Scope: TransferScope,
		Profiles:  append([]ProfilePreview{}, t.previews...),
		Conflicts: append([]string{}, t.conflicts...),
	}
}

// Execute rechecks destination collisions and uses exclusive file creation.
// It refuses to write a printer file whose bytes changed since review. Import
// failures roll back only files created by this attempt.
func (t *Transfer) Execute() (int, error) {
	checked, err := t.WithDestination(t.destination)
	if err != nil {
		return 0, err
	}
	if len(checked.conflicts) > 0 {
		return 0, fmt.Errorf("saved setups: files already exist: %s; choose another destination", strings.Join(checked.conflicts, ", "))
	}
	if t.operation == "export-all" {
		return t.export()
	}
	return t.importMembers()
}

func changedSinceReview(name string) error {
	return fmt.Errorf("saved setups: %s changed after it was reviewed; review the transfer again", name)
}

func (t *Transfer) export() (int, error) {
	members := make([]bundle.SetMember, 0, len(t.members))
	for _, m := range t.members {
		digest, err := fileDigest(m.path)
		if err != nil {
			return 0, fmt.Errorf("%s: %w", m.name, err)
		}
		if digest != m.digest {
			return 0, changedSinceReview(m.name)
		}
		members = append(members, bundle.SetMember{Name: m.name, Path: m.path})
	}
	if err := bundle.WriteSet(t.destination, "", members); err != nil {
		return 0, err
	}
	return len(members), nil
}

// importMembers extracts each reviewed member verbatim into the destination,
// preserving filenames and refusing all collisions, including case-only ones.
// On any failure it removes only files created by this attempt.
func (t *Transfer) importMembers() (int, error) {
	set, err := bundle.OpenSet(t.source)
	if err != nil {
		return 0, err
	}
	defer set.Close()
	if len(set.Members) != len(t.members) {
		return 0, changedSinceReview(filepath.Base(t.source))
	}
	for i, m := range t.members {
		if set.Members[i] != m.name {
			return 0, changedSinceReview(filepath.Base(t.source))
		}
	}
	if err := os.MkdirAll(t.destination, 0700); err != nil {
		return 0, err
	}
	created := []string{}
	rollback := func() {
		for _, p := range created {
			_ = os.Remove(p)
		}
	}
	for _, m := range t.members {
		target, err := set.Extract(m.name, t.destination)
		if err != nil {
			rollback()
			return 0, err
		}
		created = append(created, target)
		digest, err := fileDigest(target)
		if err != nil {
			rollback()
			return 0, err
		}
		if digest != m.digest {
			rollback()
			return 0, changedSinceReview(m.name)
		}
	}
	return len(created), nil
}
