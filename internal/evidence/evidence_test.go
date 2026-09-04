package evidence

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadFixture(t *testing.T) {
	fixture := filepath.Join("..", "..", "fixtures", "hp-laserjet-m404-synthetic.json")
	got, err := LoadFixture(fixture)
	if err != nil {
		t.Fatalf("LoadFixture() error = %v", err)
	}
	if got.Provenance != "synthetic" {
		t.Fatalf("Provenance = %q, want synthetic", got.Provenance)
	}
	if got.ProvenanceNote == "" {
		t.Fatal("ProvenanceNote is empty")
	}
}

func TestLoadFixtureRejectsInvalidProvenance(t *testing.T) {
	tests := []struct {
		name        string
		contents    string
		wantMessage string
	}{
		{
			name:        "missing provenance",
			contents:    `{"http_model_string":"HP LaserJet Pro M404dn"}`,
			wantMessage: "provenance must be exactly",
		},
		{
			name:        "unknown provenance",
			contents:    `{"provenance":"estimated"}`,
			wantMessage: "provenance must be exactly",
		},
		{
			name:        "synthetic without note",
			contents:    `{"provenance":"synthetic"}`,
			wantMessage: "provenance_note is required",
		},
		{
			name:        "synthetic with blank note",
			contents:    `{"provenance":"synthetic","provenance_note":"  "}`,
			wantMessage: "provenance_note is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "fixture.json")
			if err := os.WriteFile(path, []byte(tt.contents), 0o600); err != nil {
				t.Fatalf("WriteFile() error = %v", err)
			}
			_, err := LoadFixture(path)
			if err == nil {
				t.Fatal("LoadFixture() error = nil, want validation error")
			}
			if !strings.Contains(err.Error(), tt.wantMessage) {
				t.Fatalf("LoadFixture() error = %q, want substring %q", err, tt.wantMessage)
			}
		})
	}
}

func TestLoadFixtureRejectsMalformedJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fixture.json")
	if err := os.WriteFile(path, []byte(`{"provenance":`), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	_, err := LoadFixture(path)
	if err == nil || !strings.Contains(err.Error(), "decode fixture") {
		t.Fatalf("LoadFixture() error = %v, want clear decode error", err)
	}
}
