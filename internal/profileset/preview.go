package profileset

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// TransferScope is the same handoff guidance in CLI previews and desktop review.
const TransferScope = "Saves JSON settings and captured evidence only; no printers are installed or changed. Driver archives are separate: copy them too, preserving paths relative to the destination setup folder. Absolute archive paths must remain available or be updated before use."

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
// expose or reload the profiles that the operator reviewed.
type Transfer struct {
	operation   string
	source      string
	destination string
	collection  Collection
	conflicts   []string
}

func PrepareImport(path, directory string) (*Transfer, error) {
	c, err := Load(path)
	if err != nil {
		return nil, err
	}
	return (&Transfer{operation: "import-all", source: path, collection: c}).WithDestination(directory)
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
	names := make([]string, 0, len(t.collection.Profiles))
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
		for _, entry := range t.collection.Profiles {
			names = append(names, entry.File)
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
	p := Preview{
		Operation: t.operation, Source: t.source, Destination: t.destination,
		Count: len(t.collection.Profiles), Scope: TransferScope,
		Profiles:  make([]ProfilePreview, 0, len(t.collection.Profiles)),
		Conflicts: append([]string{}, t.conflicts...),
	}
	for _, entry := range t.collection.Profiles {
		profile := ProfilePreview{File: entry.File, PrinterName: entry.Profile.PrinterName, Target: entry.Profile.Target, DriverName: entry.Profile.DriverName}
		if entry.Profile.DriverPackage != nil {
			profile.Archive = entry.Profile.DriverPackage.Archive
		}
		p.Profiles = append(p.Profiles, profile)
	}
	return p
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
		return exportCollection(t.collection, t.destination)
	}
	return importCollection(t.collection, t.destination)
}
