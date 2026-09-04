package inspect

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/spilloid/spoolsmith/internal/evidence"
)

func TestInspectDeterministicGoldenOutput(t *testing.T) {
	fixtures := []string{
		"hp-laserjet-m404-synthetic.json",
		"brother-hl-l2350dw-synthetic.json",
		"ambiguous-unknown-vendor.json",
	}
	for _, name := range fixtures {
		t.Run(name, func(t *testing.T) {
			got := inspectFixtureJSON(t, name)
			goldenPath := filepath.Join("testdata", name)
			want, err := os.ReadFile(goldenPath)
			if err != nil {
				t.Fatalf("ReadFile(%q) error = %v", goldenPath, err)
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("inspect output differs from %s\ngot:\n%s\nwant:\n%s", goldenPath, got, want)
			}
		})
	}
}

func TestInspectPreservesEvidenceProvenance(t *testing.T) {
	e := loadInspectFixture(t, "hp-laserjet-m404-synthetic.json")
	got := Inspect(e)
	if got.Evidence.Provenance != e.Provenance || got.Evidence.ProvenanceNote != e.ProvenanceNote {
		t.Fatalf("Inspect() did not preserve provenance: %#v", got.Evidence)
	}
	if got.Manufacturer != "HP" || got.Model != "HP LaserJet Pro M404dn" {
		t.Fatalf("Inspect() identity = %q %q", got.Manufacturer, got.Model)
	}
}

func inspectFixtureJSON(t *testing.T, name string) []byte {
	t.Helper()
	result := Inspect(loadInspectFixture(t, name))
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		t.Fatalf("MarshalIndent() error = %v", err)
	}
	return append(data, '\n')
}

func loadInspectFixture(t *testing.T, name string) evidence.Evidence {
	t.Helper()
	e, err := evidence.LoadFixture(filepath.Join("..", "..", "fixtures", name))
	if err != nil {
		t.Fatalf("LoadFixture(%q) error = %v", name, err)
	}
	return e
}
