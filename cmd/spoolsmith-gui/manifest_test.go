package main

import (
	"bytes"
	"os"
	"testing"

	"github.com/spilloid/spoolsmith/internal/winres"
)

const (
	manifestSource  = "spoolsmith-gui.exe.manifest"
	generatedObject = "rsrc_windows_amd64.syso"
)

// TestGeneratedResourceObjectMatchesManifest keeps the committed .syso honest.
// It is the only thing standing between an edited manifest and a release binary
// that still carries the old one, since nothing in a normal build regenerates
// the object.
func TestGeneratedResourceObjectMatchesManifest(t *testing.T) {
	manifest, err := os.ReadFile(manifestSource)
	if err != nil {
		t.Fatalf("reading %s: %v", manifestSource, err)
	}
	committed, err := os.ReadFile(generatedObject)
	if err != nil {
		t.Fatalf("reading %s: %v", generatedObject, err)
	}
	expected, err := winres.ManifestObject(manifest, "amd64")
	if err != nil {
		t.Fatalf("generating the resource object: %v", err)
	}
	if !bytes.Equal(committed, expected) {
		t.Errorf("%s is stale relative to %s; regenerate it with:\n\tgo generate ./cmd/spoolsmith-gui", generatedObject, manifestSource)
	}
}

// TestManifestDeclaresTheCommonControlsDependency guards the reason the
// manifest exists at all. tailscale/walk calls InitCommonControlsEx during
// package init; without the comctl32 v6 activation context that call fails and
// the GUI exits before showing a window.
func TestManifestDeclaresTheCommonControlsDependency(t *testing.T) {
	manifest, err := os.ReadFile(manifestSource)
	if err != nil {
		t.Fatalf("reading %s: %v", manifestSource, err)
	}
	for _, required := range []string{
		"Microsoft.Windows.Common-Controls",
		`version="6.0.0.0"`,
		"6595b64144ccf1df",
	} {
		if !bytes.Contains(manifest, []byte(required)) {
			t.Errorf("%s no longer contains %q; the GUI will fail InitCommonControlsEx at startup", manifestSource, required)
		}
	}
}
