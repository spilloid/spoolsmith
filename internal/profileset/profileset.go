// Package profileset transfers saved printer JSON without applying Windows changes.
package profileset

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spilloid/spoolsmith/internal/install"
)

const maxSize = 16 << 20

type Entry struct {
	File    string          `json:"file"`
	Profile install.Profile `json:"profile"`
}

type Collection struct {
	Version  int     `json:"version"`
	Profiles []Entry `json:"profiles"`
}

func (c Collection) validate() error {
	if c.Version != 1 || len(c.Profiles) == 0 || len(c.Profiles) > 1000 {
		return fmt.Errorf("saved setups: expected version 1 and between 1 and 1000 profiles")
	}
	seen := map[string]bool{}
	for _, e := range c.Profiles {
		stem := strings.Split(strings.ToUpper(e.File), ".")[0]
		reserved := stem == "CON" || stem == "PRN" || stem == "AUX" || stem == "NUL" || (len(stem) == 4 && (strings.HasPrefix(stem, "COM") || strings.HasPrefix(stem, "LPT")) && stem[3] >= '0' && stem[3] <= '9')
		if e.File == "" || len(e.File) > 200 || strings.ContainsAny(e.File, "/\\:<>\"|?*\x00") || strings.TrimSpace(e.File) != e.File || !strings.HasSuffix(strings.ToLower(e.File), ".json") || reserved {
			return fmt.Errorf("saved setups: unsafe profile filename %q", e.File)
		}
		for _, r := range e.File {
			if r < 32 {
				return fmt.Errorf("saved setups: unsafe profile filename %q", e.File)
			}
		}
		key := strings.ToLower(e.File)
		if seen[key] {
			return fmt.Errorf("saved setups: duplicate filename %q", e.File)
		}
		seen[key] = true
		if err := e.Profile.Validate(); err != nil {
			return fmt.Errorf("%s: %w", e.File, err)
		}
		data, err := json.MarshalIndent(e.Profile, "", "  ")
		if err != nil {
			return err
		}
		if len(data)+1 > 1<<20 {
			return fmt.Errorf("%s: profile exceeds 1 MiB", e.File)
		}
	}
	return nil
}

// PrepareExport reads and validates every top-level profile without writing files.
// The prepared transfer retains exactly the profiles shown during review.
func PrepareExport(directory, path string) (*Transfer, error) {
	transfer, err := (&Transfer{operation: "export-all", source: directory}).WithDestination(path)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, fmt.Errorf("saved setups: cannot read profile folder %s; choose a folder containing saved setups: %w", directory, err)
	}
	c := Collection{Version: 1, Profiles: []Entry{}}
	for _, e := range entries {
		if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), ".json") {
			continue
		}
		if e.Type()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("saved setups: symbolic link %q is not a profile", e.Name())
		}
		p, err := install.LoadProfile(filepath.Join(directory, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", e.Name(), err)
		}
		c.Profiles = append(c.Profiles, Entry{File: e.Name(), Profile: p})
	}
	if len(c.Profiles) == 0 {
		return nil, fmt.Errorf("saved setups: no JSON profiles in %s; choose a folder containing saved setups or save a printer first", directory)
	}
	if err := c.validate(); err != nil {
		return nil, err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return nil, err
	}
	if len(data)+1 > maxSize {
		return nil, fmt.Errorf("saved setups: export exceeds 16 MiB")
	}
	transfer.collection = c
	return transfer, nil
}

// Export preserves the existing collection format and never replaces a file.
func Export(directory, path string) (int, error) {
	transfer, err := PrepareExport(directory, path)
	if err != nil {
		return 0, err
	}
	return transfer.Execute()
}

func exportCollection(c Collection, path string) (int, error) {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return 0, err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return 0, err
	}
	_, writeErr := f.Write(append(data, '\n'))
	closeErr := f.Close()
	if writeErr != nil {
		os.Remove(path)
		return 0, writeErr
	}
	if closeErr != nil {
		os.Remove(path)
		return 0, closeErr
	}
	return len(c.Profiles), nil
}

// Load validates the entire collection before any files can be imported.
func Load(path string) (Collection, error) {
	var c Collection
	f, err := os.Open(path)
	if err != nil {
		return c, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return c, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxSize {
		return c, fmt.Errorf("saved setups: expected a regular JSON file no larger than 16 MiB")
	}
	dec := json.NewDecoder(io.LimitReader(f, maxSize+1))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return c, err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return c, fmt.Errorf("saved setups: trailing JSON data")
	}
	return c, c.validate()
}

// Import preserves filenames and refuses all collisions, including case-only ones.
// On a write failure it removes only files created by this attempt.
func Import(path, directory string) (int, error) {
	transfer, err := PrepareImport(path, directory)
	if err != nil {
		return 0, err
	}
	return transfer.Execute()
}

func importCollection(c Collection, directory string) (int, error) {
	if err := os.MkdirAll(directory, 0700); err != nil {
		return 0, err
	}
	created := []string{}
	rollback := func() {
		for _, p := range created {
			_ = os.Remove(p)
		}
	}
	for _, e := range c.Profiles {
		data, err := json.MarshalIndent(e.Profile, "", "  ")
		if err != nil {
			rollback()
			return 0, err
		}
		if len(data)+1 > 1<<20 {
			rollback()
			return 0, fmt.Errorf("%s: formatted profile exceeds 1 MiB", e.File)
		}
		target := filepath.Join(directory, e.File)
		f, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			rollback()
			return 0, err
		}
		created = append(created, target)
		_, writeErr := f.Write(append(data, '\n'))
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
