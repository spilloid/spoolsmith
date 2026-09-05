// Package inspect assembles reviewable printer and driver resolution results.
package inspect

import (
	"context"
	"os"

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
func Target(ctx context.Context, target string) (InspectResult, error) {
	info, statErr := os.Stat(target)
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
