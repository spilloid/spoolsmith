package install

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/spilloid/spoolsmith/internal/catalog"
	"github.com/spilloid/spoolsmith/internal/evidence"
)

// Profile is an operator-selected queue and installed driver, bound to captured
// model evidence. It never contains executable instructions.
type Profile struct {
	Version       int               `json:"version"`
	Target        string            `json:"target"`
	PrinterName   string            `json:"printer_name"`
	DriverName    string            `json:"driver_name"`
	Evidence      evidence.Evidence `json:"evidence"`
	DriverPackage *PackageSelection `json:"driver_package,omitempty"`
}

var errIdentityUnavailable = errors.New("profile: saved identity sources are currently unavailable; wake the printer and retry")

func (p Profile) Validate() error {
	if p.DriverPackage != nil {
		if _, err := p.DriverPackage.record(p.DriverName); err != nil {
			return err
		}
	}
	if p.Version != 1 {
		return errors.New("profile: unsupported version (expected 1)")
	}
	if net.ParseIP(p.Target) == nil {
		return errors.New("profile: target must be a literal IP address")
	}
	for _, value := range []struct{ name, text string }{{"printer name", p.PrinterName}, {"driver name", p.DriverName}} {
		if strings.TrimSpace(value.text) == "" {
			return fmt.Errorf("profile: %s is required", value.name)
		}
		if err := validatePlanValue(value.name, value.text); err != nil {
			return err
		}
	}
	if p.Evidence.Provenance != "captured" {
		return errors.New("profile: captured evidence is required")
	}
	if strings.TrimSpace(p.Evidence.HTTPTitle) == "" && strings.TrimSpace(p.Evidence.PJLID) == "" && strings.TrimSpace(p.Evidence.SNMPSysDescr) == "" {
		return errors.New("profile: HTTP, PJL, or SNMP identity is required; capture again when the printer is awake")
	}
	return nil
}

// LoadProfile rejects unknown fields and trailing JSON rather than ignoring typos.
func LoadProfile(path string) (Profile, error) {
	f, err := os.Open(path)
	if err != nil {
		return Profile{}, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return Profile{}, err
	}
	if !info.Mode().IsRegular() || info.Size() > 1024*1024 {
		return Profile{}, errors.New("profile: expected a regular JSON file no larger than 1 MiB")
	}
	decoder := json.NewDecoder(io.LimitReader(f, 1024*1024+1))
	decoder.DisallowUnknownFields()
	var p Profile
	if err := decoder.Decode(&p); err != nil {
		return p, fmt.Errorf("profile: decode: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return p, errors.New("profile: trailing data or oversized document")
	}
	return p, p.Validate()
}

// ResolvePackagePath makes a loaded profile portable with its adjacent payload.
// Editing keeps the original relative path; only execution resolves it.
func (p *Profile) ResolvePackagePath(profilePath string) error {
	if p.DriverPackage == nil {
		return nil
	}
	selection := *p.DriverPackage
	if !filepath.IsAbs(selection.Archive) {
		absolute, err := filepath.Abs(filepath.Join(filepath.Dir(profilePath), selection.Archive))
		if err != nil {
			return err
		}
		selection.Archive = absolute
	}
	p.DriverPackage = &selection
	return nil
}

// SaveProfile creates a new file only. Existing inventory is never overwritten.
func SaveProfile(path string, p Profile) error {
	if err := p.Validate(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, writeErr := f.Write(append(data, '\n'))
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}

// EditProfile preserves the previous file in a unique backup and replaces the
// profile only after the new document has been fully written and closed.
func EditProfile(path string, p Profile) (string, error) {
	if err := p.Validate(); err != nil {
		return "", err
	}
	original, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return "", err
	}
	backupDir := filepath.Join(filepath.Dir(path), ".backups")
	if err := os.MkdirAll(backupDir, 0700); err != nil {
		return "", err
	}
	backup, err := os.CreateTemp(backupDir, filepath.Base(path)+"-*.bak")
	if err != nil {
		return "", err
	}
	backupPath := backup.Name()
	_, writeErr := backup.Write(original)
	closeErr := backup.Close()
	if writeErr != nil {
		return backupPath, writeErr
	}
	if closeErr != nil {
		return backupPath, closeErr
	}
	next, err := os.CreateTemp(filepath.Dir(path), ".profile-*.tmp")
	if err != nil {
		return backupPath, err
	}
	defer os.Remove(next.Name())
	_, writeErr = next.Write(append(data, '\n'))
	closeErr = next.Close()
	if writeErr != nil {
		return backupPath, writeErr
	}
	if closeErr != nil {
		return backupPath, closeErr
	}
	return backupPath, os.Rename(next.Name(), path)
}

func (p Profile) resolution(current evidence.Evidence) (catalog.ResolutionResult, error) {
	if err := p.Validate(); err != nil {
		return catalog.ResolutionResult{}, err
	}
	matched := false
	hasModelSource := strings.TrimSpace(p.Evidence.HTTPTitle) != "" || strings.TrimSpace(p.Evidence.PJLID) != ""
	for index, pair := range [][2]string{{p.Evidence.HTTPTitle, current.HTTPTitle}, {p.Evidence.PJLID, current.PJLID}, {p.Evidence.SNMPSysDescr, current.SNMPSysDescr}} {
		field := []string{"HTTP title", "PJL identity", "SNMP description"}[index]
		if strings.TrimSpace(pair[0]) == "" {
			continue
		}
		if strings.TrimSpace(pair[1]) == "" {
			continue
		}
		if strings.TrimSpace(pair[0]) != strings.TrimSpace(pair[1]) {
			return catalog.ResolutionResult{}, fmt.Errorf("profile: %s changed: saved %q, observed %q; verify the device and recapture if appropriate", field, pair[0], pair[1])
		}
		if index < 2 || !hasModelSource {
			matched = true
		}
	}
	if !matched {
		return catalog.ResolutionResult{}, errIdentityUnavailable
	}
	family := catalog.Family{ID: "operator-profile", Manufacturer: "Operator selected"}
	driver := catalog.DriverPackage{FamilyID: family.ID, Name: p.DriverName, WindowsDriverName: p.DriverName, Source: "Operator-selected installed Windows driver", Strategy: "existing-windows-driver"}
	return catalog.ResolutionResult{NormalizedModel: p.PrinterName, Family: &family, Driver: &driver, Confidence: 0, Uncertain: []string{"driver compatibility is operator-selected; captured identity is not device authentication"}}, nil
}
