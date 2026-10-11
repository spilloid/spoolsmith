// Package inspect assembles reviewable printer and driver resolution results.
package inspect

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/spilloid/spoolsmith/internal/catalog"
	"github.com/spilloid/spoolsmith/internal/evidence"
	"github.com/spilloid/spoolsmith/internal/probe"
)

// InspectResult contains the original evidence, its provenance, and the full
// resolution result. A nil Family and Driver is a non-actionable result.
type InspectResult struct {
	Manufacturer    string                 `json:"manufacturer,omitempty"`
	Model           string                 `json:"model,omitempty"`
	Evidence        evidence.Evidence      `json:"evidence"`
	NormalizedModel string                 `json:"normalized_model"`
	Family          *catalog.Family        `json:"family,omitempty"`
	Driver          *catalog.DriverPackage `json:"driver,omitempty"`
	Confidence      float64                `json:"confidence"`
	Uncertain       []string               `json:"uncertain,omitempty"`
}

// Inspect resolves evidence and preserves it verbatim in a reviewable result.
func Inspect(e evidence.Evidence) InspectResult {
	resolution := catalog.Resolve(e)
	result := InspectResult{
		Evidence:        e,
		NormalizedModel: resolution.NormalizedModel,
		Family:          resolution.Family,
		Driver:          resolution.Driver,
		Confidence:      resolution.Confidence,
		Uncertain:       resolution.Uncertain,
	}
	if resolution.Family != nil {
		result.Manufacturer = resolution.Family.Manufacturer
		result.Model = resolution.NormalizedModel
	}
	return result
}

// Target loads a fixture when target names a regular file, otherwise it runs
// the live probe, then returns the same pure inspection result for either path.
//
// A target that can only be a file name -- it has a path separator or a .json
// extension -- and does not exist is reported as such, rather than being
// looked up as a host name.
func Target(ctx context.Context, target string) (InspectResult, error) {
	info, statErr := os.Stat(target)
	if statErr != nil && looksLikeFile(target) {
		if errors.Is(statErr, fs.ErrNotExist) {
			return InspectResult{}, fmt.Errorf("inspect: %s: file not found", target)
		}
		return InspectResult{}, fmt.Errorf("inspect: %w", statErr)
	}
	if statErr == nil && info.Mode().IsRegular() {
		e, err := evidence.LoadFixture(target)
		if err != nil {
			return InspectResult{}, err
		}
		return Inspect(e), nil
	}
	result, err := probe.Collect(ctx, target)
	if err != nil {
		return InspectResult{}, err
	}
	return Inspect(result.Evidence), nil
}

// looksLikeFile reports a target no printer address or host name could be.
func looksLikeFile(target string) bool {
	return strings.ContainsAny(target, `/\`) || strings.EqualFold(filepath.Ext(target), ".json")
}
