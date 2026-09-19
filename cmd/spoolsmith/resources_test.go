package main

import (
	"bytes"
	"os"
	"testing"

	"github.com/spilloid/spoolsmith/internal/winres"
)

// TestGeneratedIconObjectMatchesSource keeps the committed .syso honest: nothing
// in a normal build regenerates it, so without this a replaced icon would ship
// as the old artwork.
func TestGeneratedIconObjectMatchesSource(t *testing.T) {
	const (
		iconSource      = "../../assets/icon/spoolsmith.png"
		generatedObject = "rsrc_windows_amd64.syso"
	)
	iconFile, err := os.Open(iconSource)
	if err != nil {
		t.Fatalf("opening %s: %v", iconSource, err)
	}
	defer iconFile.Close()
	resources, err := winres.IconResourcesFromPNG(iconFile)
	if err != nil {
		t.Fatalf("rendering %s: %v", iconSource, err)
	}
	committed, err := os.ReadFile(generatedObject)
	if err != nil {
		t.Fatalf("reading %s: %v", generatedObject, err)
	}
	expected, err := winres.Object(resources, "amd64")
	if err != nil {
		t.Fatalf("generating the resource object: %v", err)
	}
	if !bytes.Equal(committed, expected) {
		t.Errorf("%s is stale relative to %s; regenerate it with:\n\tgo generate ./cmd/spoolsmith", generatedObject, iconSource)
	}
}
