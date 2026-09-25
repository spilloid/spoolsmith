//go:build windows

package main

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// Ctrl+C on This PC puts printer files on the clipboard in the same form
// Explorer's Copy does, so Explorer can paste them and SpoolSmith's own
// Ctrl+V can read them back. It replaces the clipboard, so it runs only in
// CI (or when asked), never on a developer's desktop by surprise.
func TestClipboardFilesRoundTripInExplorersFormat(t *testing.T) {
	if os.Getenv("CI") == "" && os.Getenv("SPOOLSMITH_CLIPBOARD_TEST") == "" {
		t.Skip("replaces the clipboard; set SPOOLSMITH_CLIPBOARD_TEST=1 to run")
	}
	dir := t.TempDir()
	paths := []string{filepath.Join(dir, "Front desk.ssb"), filepath.Join(dir, "Café printer.ssb")}
	for _, path := range paths {
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := setClipboardFiles(0, paths); err != nil {
		t.Fatal(err)
	}
	if got := clipboardFiles(0); !slices.Equal(got, paths) {
		t.Fatalf("clipboard files = %v, want %v", got, paths)
	}
}
