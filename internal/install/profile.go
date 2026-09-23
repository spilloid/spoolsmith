package install

import (
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"strings"

	"github.com/spilloid/spoolsmith/internal/catalog"
	"github.com/spilloid/spoolsmith/internal/evidence"
)

// Profile is an operator-selected queue and installed driver, bound to captured
// model evidence. It never contains executable instructions.
//
// A Profile is a pure, in-memory document; it does not know how to read or
// write itself. On disk, every Profile lives inside a bundle (package
// internal/bundle, extension .ssb) -- there is deliberately no separate,
// bare-JSON file format for a profile. A single saved printer and a printer
// handed to another PC used to be two different file shapes for the same
// document; internal/bundle.SaveProfile/LoadProfile/EditProfile is the one
// place that now reads and writes it, whether or not a driver payload rides
// along.
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
	switch p.Evidence.Provenance {
	case "captured":
		if !p.hasCapturedIdentity() {
			return errors.New("profile: HTTP, PJL, or SNMP identity is required; capture again when the printer is awake")
		}
	case "unconfirmed":
		// The source printer never answered when this profile was captured
		// (see bundle.Create's offline fallback). There is deliberately no
		// identity here to check -- RunInstall always treats a profile like
		// this as offline, never as a silent pass on a live comparison.
		if strings.TrimSpace(p.Evidence.ProvenanceNote) == "" {
			return errors.New("profile: provenance_note is required when evidence is unconfirmed")
		}
	default:
		return errors.New("profile: captured or unconfirmed evidence is required")
	}
	return nil
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

// hasCapturedIdentity reports whether this profile carries any live-captured
// identity signal at all. A profile captured offline (Provenance
// "unconfirmed") has none by definition, which RunInstall uses to skip
// straight to its offline fallback instead of probing for a comparison that
// can never succeed.
func (p Profile) hasCapturedIdentity() bool {
	return strings.TrimSpace(p.Evidence.HTTPTitle) != "" || strings.TrimSpace(p.Evidence.PJLID) != "" || strings.TrimSpace(p.Evidence.SNMPSysDescr) != ""
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
	return p.selectedResolution(), nil
}

// selectedResolution describes the operator's selection without asserting any live evidence.
func (p Profile) selectedResolution() catalog.ResolutionResult {
	family := catalog.Family{ID: "operator-profile", Manufacturer: "Operator selected"}
	driver := catalog.DriverPackage{FamilyID: family.ID, Name: p.DriverName, WindowsDriverName: p.DriverName, Source: "Operator-selected installed Windows driver", Strategy: "existing-windows-driver"}
	return catalog.ResolutionResult{NormalizedModel: p.PrinterName, Family: &family, Driver: &driver, Confidence: 0, Uncertain: []string{"driver compatibility is operator-selected; captured identity is not device authentication"}}
}
