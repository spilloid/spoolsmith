// Package bundle packages one already-reviewed printer setup into a single
// portable archive, so a second machine can reproduce that setup without an
// operator re-deriving the queue name, the target address, or the exact
// registered Windows driver name by hand.
//
// The container is a plain zip. That is deliberate: a bundle has to open on a
// machine nobody has prepared, and Windows reads zip natively with no tool
// installed. Nothing here executes; a bundle carries a profile document, and
// optionally driver files exported from a Windows driver store.
//
// A bundle is tamper-evident, not authenticated. Hashes here detect corruption
// and casual edits. They are not a signature and must not be described as one:
// the real trust anchor for a driver payload remains Windows' own catalog
// signature check when pnputil stages the INF, exactly as it is for the
// reviewed vendor-archive path.
package bundle

import (
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/spilloid/spoolsmith/internal/install"
)

// Version is the bundle format version. A reader refuses anything else rather
// than guessing at a document it does not understand.
const Version = 1

// ManifestName is the manifest entry's fixed path inside the archive.
const ManifestName = "manifest.json"

// PayloadPrefix is the fixed directory holding exported driver files.
const PayloadPrefix = "payload/"

// Limits bound what a reader will accept, so a malformed or hostile archive
// cannot exhaust memory or disk before validation has a chance to reject it.
const (
	MaxManifestBytes = 1 << 20 // 1 MiB
	MaxPayloadBytes  = 1 << 30 // 1 GiB total uncompressed
	MaxPayloadFiles  = 4096
)

// Manifest is the bundle's own description of itself. It contains
// configuration and provenance only, never commands.
type Manifest struct {
	Version int    `json:"version"`
	Created string `json:"created_utc"`
	// CreatedBy names the SpoolSmith build that wrote the bundle. It is
	// provenance for a human reading the manifest, never a trust decision.
	CreatedBy string `json:"created_by,omitempty"`
	// SourceHost records which machine the setup was cloned from. Recorded so
	// an operator can trace a bundle back to the install it came from.
	SourceHost string `json:"source_host,omitempty"`
	// Note is an operator-supplied free-text label.
	Note string `json:"note,omitempty"`
	// Profile is the reviewed queue/driver/evidence document, byte-identical
	// to what `profile capture` writes.
	Profile install.Profile `json:"profile"`
	// Driver is present only when driver files were exported alongside the
	// profile. Absent means the target machine must already have the driver.
	Driver *DriverPayload `json:"driver_payload,omitempty"`
}

// DriverPayload describes driver files exported from a Windows driver store.
type DriverPayload struct {
	// WindowsDriverName is the exact registered name the payload provides. It
	// must equal the profile's driver name; a bundle that disagrees with
	// itself is rejected rather than reconciled.
	WindowsDriverName string `json:"windows_driver_name"`
	// INF is the payload-relative path to the INF that pnputil stages.
	INF string `json:"inf"`
	// ExportedFrom records the origin driver-store package, for provenance.
	ExportedFrom string `json:"exported_from,omitempty"`
	// Files lists every payload entry with its hash, so extraction can verify
	// each file rather than trusting the archive.
	Files []File `json:"files"`
}

// File is one payload entry.
type File struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// Validate checks a manifest's internal consistency. It does not touch the
// filesystem or the network.
func (m Manifest) Validate() error {
	if m.Version != Version {
		return fmt.Errorf("bundle: unsupported bundle version %d (expected %d)", m.Version, Version)
	}
	if err := m.Profile.Validate(); err != nil {
		return err
	}
	if m.Driver == nil {
		return nil
	}
	d := m.Driver
	if strings.TrimSpace(d.WindowsDriverName) == "" {
		return errors.New("bundle: driver payload is missing its Windows driver name")
	}
	if d.WindowsDriverName != m.Profile.DriverName {
		return fmt.Errorf("bundle: driver payload provides %q but the profile maps %q; the bundle disagrees with itself", d.WindowsDriverName, m.Profile.DriverName)
	}
	if strings.TrimSpace(d.INF) == "" {
		return errors.New("bundle: driver payload is missing its INF path")
	}
	if !strings.EqualFold(path.Ext(d.INF), ".inf") {
		return fmt.Errorf("bundle: driver payload INF %q is not an .inf file", d.INF)
	}
	if len(d.Files) == 0 {
		return errors.New("bundle: driver payload lists no files")
	}
	if len(d.Files) > MaxPayloadFiles {
		return fmt.Errorf("bundle: driver payload lists %d files, above the %d limit", len(d.Files), MaxPayloadFiles)
	}
	var total int64
	seen := make(map[string]bool, len(d.Files))
	infListed := false
	for _, f := range d.Files {
		if err := validatePayloadPath(f.Path); err != nil {
			return err
		}
		if seen[strings.ToLower(f.Path)] {
			return fmt.Errorf("bundle: driver payload lists %q more than once", f.Path)
		}
		seen[strings.ToLower(f.Path)] = true
		if f.Size < 0 {
			return fmt.Errorf("bundle: driver payload file %q has a negative size", f.Path)
		}
		if len(f.SHA256) != 64 {
			return fmt.Errorf("bundle: driver payload file %q has a malformed SHA-256", f.Path)
		}
		if _, err := hex.DecodeString(f.SHA256); err != nil {
			return fmt.Errorf("bundle: driver payload file %q has a non-hexadecimal SHA-256", f.Path)
		}
		total += f.Size
		if total > MaxPayloadBytes {
			return fmt.Errorf("bundle: driver payload exceeds the %d byte limit", int64(MaxPayloadBytes))
		}
		if f.Path == d.INF {
			infListed = true
		}
	}
	if !infListed {
		return fmt.Errorf("bundle: driver payload INF %q is not among the listed files", d.INF)
	}
	return nil
}

// validatePayloadPath rejects any entry that could escape the extraction
// directory or name something other than a plain relative file.
func validatePayloadPath(name string) error {
	if strings.TrimSpace(name) == "" {
		return errors.New("bundle: payload entry has an empty path")
	}
	if name != path.Clean(name) {
		return fmt.Errorf("bundle: payload entry %q is not a clean relative path", name)
	}
	if path.IsAbs(name) || strings.HasPrefix(name, "/") {
		return fmt.Errorf("bundle: payload entry %q is absolute", name)
	}
	if strings.Contains(name, `\`) {
		return fmt.Errorf("bundle: payload entry %q contains a backslash; entries use forward slashes", name)
	}
	// A Windows drive-relative or UNC path must never survive extraction.
	if len(name) >= 2 && name[1] == ':' {
		return fmt.Errorf("bundle: payload entry %q names a drive", name)
	}
	for _, part := range strings.Split(name, "/") {
		if part == "" || part == "." || part == ".." {
			return fmt.Errorf("bundle: payload entry %q contains a traversal or empty segment", name)
		}
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("bundle: payload entry %q contains a control character", name)
		}
	}
	return nil
}
