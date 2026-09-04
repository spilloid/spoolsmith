package catalog

import (
	"path/filepath"
	"testing"

	"github.com/spilloid/spoolsmith/internal/evidence"
)

func TestFamiliesHasExactlyTwoFamilies(t *testing.T) {
	got := Families()
	if len(got) != 2 {
		t.Fatalf("len(Families()) = %d, want 2", len(got))
	}

	got[0].Aliases[0] = "mutated"
	if Families()[0].Aliases[0] == "mutated" {
		t.Fatal("Families() exposed mutable registry storage")
	}
}

func TestResolveKnownFixtures(t *testing.T) {
	tests := []struct {
		fixture string
		family  string
		model   string
	}{
		{"hp-laserjet-m404-synthetic.json", "hp-laserjet-m4xx", "HP LaserJet Pro M404dn"},
		{"brother-hl-l2350dw-synthetic.json", "brother-hl-l2xxx", "Brother HL-L2350DW"},
	}
	for _, tt := range tests {
		t.Run(tt.fixture, func(t *testing.T) {
			e := loadFixture(t, tt.fixture)
			got := Resolve(e)
			if got.Family == nil || got.Family.ID != tt.family {
				t.Fatalf("Family = %#v, want ID %q", got.Family, tt.family)
			}
			if got.Driver == nil || got.Driver.FamilyID != tt.family {
				t.Fatalf("Driver = %#v, want FamilyID %q", got.Driver, tt.family)
			}
			if got.NormalizedModel != tt.model {
				t.Fatalf("NormalizedModel = %q, want %q", got.NormalizedModel, tt.model)
			}
			if got.Confidence != 1.0 {
				t.Fatalf("Confidence = %v, want 1.0", got.Confidence)
			}
			if len(got.Uncertain) != 0 {
				t.Fatalf("Uncertain = %v, want empty", got.Uncertain)
			}
		})
	}
}

func TestResolveUnknownFixtureFailsClosed(t *testing.T) {
	got := Resolve(loadFixture(t, "ambiguous-unknown-vendor.json"))
	if got.Family != nil || got.Driver != nil {
		t.Fatalf("Resolve() returned actionable result: Family=%#v Driver=%#v", got.Family, got.Driver)
	}
	if got.Confidence >= 1.0 {
		t.Fatalf("Confidence = %v, want < 1.0", got.Confidence)
	}
	if len(got.Uncertain) == 0 {
		t.Fatal("Uncertain is empty for unresolved evidence")
	}
}

func TestResolveConflictingEvidenceFailsClosed(t *testing.T) {
	got := Resolve(evidence.Evidence{
		HTTPModelString: "HP LaserJet Pro M404dn",
		PJLID:           "Brother HL-L2350DW",
	})
	if got.Family != nil || got.Driver != nil || got.Confidence >= 1 || len(got.Uncertain) == 0 {
		t.Fatalf("Resolve() did not fail closed: %#v", got)
	}
}

func TestResolveMultipleFamiliesInOneFieldFailsClosed(t *testing.T) {
	got := Resolve(evidence.Evidence{
		HTTPModelString: "HP LaserJet Pro M404dn and Brother HL-L2350DW",
	})
	assertUnresolved(t, got)
}

func TestResolveForeignPJLManufacturerFailsClosed(t *testing.T) {
	got := Resolve(evidence.Evidence{
		HTTPModelString: "HP LaserJet Pro M404dn",
		PJLID:           "MFG:Canon;MDL:imageCLASS LBP6230dw;CLS:PRINTER;",
	})
	assertUnresolved(t, got)
}

func TestResolveMultipleMACVendorsFailsClosed(t *testing.T) {
	got := Resolve(evidence.Evidence{
		HTTPModelString: "HP LaserJet Pro M404dn",
		MACVendor:       "Brother Industries / HP",
	})
	assertUnresolved(t, got)
}

func TestResolveMultipleAliasesInOneField(t *testing.T) {
	t.Run("same model", func(t *testing.T) {
		got := Resolve(evidence.Evidence{
			HTTPModelString: "Brother HL-L2350DW and HL-L2350DW",
		})
		if got.Family == nil || got.Family.ID != "brother-hl-l2xxx" {
			t.Fatalf("Family = %#v, want Brother family", got.Family)
		}
		if got.Driver == nil || got.NormalizedModel != "Brother HL-L2350DW" || got.Confidence != 1 || len(got.Uncertain) != 0 {
			t.Fatalf("Resolve() did not resolve the same-model aliases: %#v", got)
		}
	})

	t.Run("different models", func(t *testing.T) {
		got := Resolve(evidence.Evidence{
			HTTPModelString: "HP LaserJet Pro M404 and HP LaserJet Pro M404dn",
		})
		assertUnresolved(t, got)
	})
}

func assertUnresolved(t *testing.T, got ResolutionResult) {
	t.Helper()
	if got.Family != nil || got.Driver != nil {
		t.Fatalf("Resolve() returned actionable result: Family=%#v Driver=%#v", got.Family, got.Driver)
	}
	if got.Confidence >= 1 {
		t.Fatalf("Confidence = %v, want < 1", got.Confidence)
	}
	if len(got.Uncertain) == 0 {
		t.Fatal("Uncertain is empty for unresolved evidence")
	}
}

func loadFixture(t *testing.T, name string) evidence.Evidence {
	t.Helper()
	path := filepath.Join("..", "..", "fixtures", name)
	e, err := evidence.LoadFixture(path)
	if err != nil {
		t.Fatalf("LoadFixture(%q) error = %v", name, err)
	}
	return e
}
