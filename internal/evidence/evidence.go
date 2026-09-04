// Package evidence defines printer observations and loads them from fixtures.
package evidence

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// Evidence is the already-observed fingerprint data for one printer. This
// package does not collect evidence from a network or device.
type Evidence struct {
	IP              string `json:"ip,omitempty"`
	SNMPSysDescr    string `json:"snmp_sys_descr,omitempty"`
	SNMPSysObjectID string `json:"snmp_sys_object_id,omitempty"`
	HTTPTitle       string `json:"http_title,omitempty"`
	HTTPModelString string `json:"http_model_string,omitempty"`
	PJLID           string `json:"pjl_id,omitempty"`
	OpenPorts       []int  `json:"open_ports,omitempty"`
	MACVendor       string `json:"mac_vendor,omitempty"`
	Hostname        string `json:"hostname,omitempty"`
	Provenance      string `json:"provenance"`
	ProvenanceNote  string `json:"provenance_note,omitempty"`
}

// LoadFixture loads and validates an evidence fixture. Provenance is required
// so callers cannot accidentally treat assumed evidence as a real capture.
func LoadFixture(path string) (Evidence, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Evidence{}, fmt.Errorf("read fixture %q: %w", path, err)
	}

	var result Evidence
	if err := json.Unmarshal(data, &result); err != nil {
		return Evidence{}, fmt.Errorf("decode fixture %q: %w", path, err)
	}

	switch result.Provenance {
	case "captured":
	case "synthetic":
		if strings.TrimSpace(result.ProvenanceNote) == "" {
			return Evidence{}, fmt.Errorf("validate fixture %q: provenance_note is required when provenance is synthetic", path)
		}
	default:
		return Evidence{}, fmt.Errorf("validate fixture %q: provenance must be exactly %q or %q", path, "captured", "synthetic")
	}

	return result, nil
}
