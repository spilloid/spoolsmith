package profileset

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spilloid/spoolsmith/internal/bundle"
)

// TransferScope is the same handoff guidance in CLI previews and desktop review.
const TransferScope = "Saves printer settings and captured evidence only; no printers are installed or changed. A saved printer carrying an embedded driver payload is not eligible for this transfer -- copy it to a file directly instead. A driver_package reference (a separate local vendor archive) travels as a path only: copy the archive too, preserving its path relative to the destination folder. Absolute archive paths must remain available or be updated before use."

// ProfilePreview describes one member for review, without exposing its raw
// bundle bytes.
type ProfilePreview struct {
	File        string `json:"file"`
	PrinterName string `json:"printer_name"`
	Target      string `json:"target"`
	DriverName  string `json:"driver_name"`
	Archive     string `json:"archive,omitempty"`
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

// Transfer owns a validated snapshot. Neither previews nor destination changes
// expose or reload the printers that the operator reviewed.
type Transfer struct {
	operation   string
	source      string
	destination string
	members     []bundle.SetMember
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
			names = append(names, m.Name)
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
// Import failures roll back only files created by this attempt.
func (t *Transfer) Execute() (int, error) {
	checked, err := t.WithDestination(t.destination)
	if err != nil {
		return 0, err
	}
	if len(checked.conflicts) > 0 {
		return 0, fmt.Errorf("saved setups: files already exist: %s; choose another destination", strings.Join(checked.conflicts, ", "))
	}
	if t.operation == "export-all" {
		if err := bundle.WriteSet(t.destination, bundle.SetIndex{}, t.members); err != nil {
			return 0, err
		}
		return len(t.members), nil
	}
	return importMembers(t.members, t.destination)
}

// importMembers writes each member's bytes verbatim, preserving filenames and
// refusing all collisions, including case-only ones. On a write failure it
// removes only files created by this attempt.
func importMembers(members []bundle.SetMember, directory string) (int, error) {
	if err := os.MkdirAll(directory, 0700); err != nil {
		return 0, err
	}
	created := []string{}
	rollback := func() {
		for _, p := range created {
			_ = os.Remove(p)
		}
	}
	for _, m := range members {
		target := filepath.Join(directory, m.Name)
		f, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			rollback()
			return 0, err
		}
		created = append(created, target)
		_, writeErr := f.Write(m.Data)
		closeErr := f.Close()
		if writeErr != nil {
			rollback()
			return 0, writeErr
		}
		if closeErr != nil {
			rollback()
			return 0, closeErr
		}
	}
	return len(created), nil
}
