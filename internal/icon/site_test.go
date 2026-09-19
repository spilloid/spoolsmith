package icon

import (
	"bytes"
	"image/png"
	"os"
	"testing"
)

// TestSiteFaviconMatchesMasterIcon fails when the master icon changes but the
// site's favicon.ico was not regenerated. The favicon is raw DIBs, so unlike the
// site's PNGs it is byte-reproducible and can be compared exactly.
func TestSiteFaviconMatchesMasterIcon(t *testing.T) {
	const (
		masterPath  = "../../assets/icon/spoolsmith.png"
		faviconPath = "../../docs/favicon.ico"
	)
	master, err := os.Open(masterPath)
	if err != nil {
		t.Fatalf("opening %s: %v", masterPath, err)
	}
	defer master.Close()
	source, err := png.Decode(master)
	if err != nil {
		t.Fatalf("decoding %s: %v", masterPath, err)
	}
	entries, err := Entries(source, FaviconSizes)
	if err != nil {
		t.Fatalf("rendering favicon: %v", err)
	}
	committed, err := os.ReadFile(faviconPath)
	if err != nil {
		t.Fatalf("reading %s: %v", faviconPath, err)
	}
	if !bytes.Equal(committed, ICO(entries)) {
		t.Errorf("%s is stale relative to %s; regenerate it with:\n\tgo generate ./internal/icon", faviconPath, masterPath)
	}
}

// The master must be big enough for the largest embedded size and square, or
// every render would fail; check it once with a message that names the file.
func TestMasterIconCanFeedEverySize(t *testing.T) {
	master, err := os.Open("../../assets/icon/spoolsmith.png")
	if err != nil {
		t.Fatalf("opening master icon: %v", err)
	}
	defer master.Close()
	config, err := png.DecodeConfig(master)
	if err != nil {
		t.Fatalf("decoding master icon: %v", err)
	}
	if config.Width != config.Height {
		t.Errorf("master icon is %dx%d, want square", config.Width, config.Height)
	}
	largest := WindowsSizes[len(WindowsSizes)-1]
	if config.Width < largest {
		t.Errorf("master icon is %dpx, smaller than the %dpx Windows size", config.Width, largest)
	}
}
