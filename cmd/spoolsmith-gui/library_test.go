//go:build windows

package main

import (
	"os"
	"path/filepath"
	"testing"
)

// The saved-setups folder used to be "profiles" beside the program. A
// machine-wide install lives under Program Files, where a standard user cannot
// write, so the default has to fall back to a per-user folder, and a folder the
// operator picked has to survive a restart.
func TestResolveProfileDirectory(t *testing.T) {
	remembered := t.TempDir()
	missing := filepath.Join(t.TempDir(), "gone")
	portable := filepath.Join(`C:\Tools\SpoolSmith`, "profiles")
	perUser := filepath.Join(`C:\Users\op\AppData\Local`, "SpoolSmith", "profiles")
	writable := func(dir string) bool { return dir == portable }
	notWritable := func(string) bool { return false }
	local := func() string { return `C:\Users\op\AppData\Local` }

	for _, tc := range []struct {
		name       string
		remembered string
		writable   func(string) bool
		want       string
	}{
		{"a remembered folder wins", remembered, writable, remembered},
		{"a remembered folder that is gone is ignored", missing, writable, portable},
		{"portable copy keeps its folder beside the program", "", writable, portable},
		{"read-only install falls back to the user's folder", "", notWritable, perUser},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveProfileDirectory(`C:\Tools\SpoolSmith\spoolsmith-gui.exe`, tc.remembered, tc.writable, local)
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRememberedProfileDirectoryRoundTrips(t *testing.T) {
	file := filepath.Join(t.TempDir(), "SpoolSmith", "library-folder.txt")
	if got := readRememberedFolder(file); got != "" {
		t.Fatalf("nothing remembered yet, got %q", got)
	}
	dir := t.TempDir()
	if err := writeRememberedFolder(file, dir); err != nil {
		t.Fatal(err)
	}
	if got := readRememberedFolder(file); got != dir {
		t.Fatalf("got %q, want %q", got, dir)
	}
	if err := os.WriteFile(file, []byte("\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := readRememberedFolder(file); got != "" {
		t.Fatalf("blank file should read as nothing, got %q", got)
	}
}

func TestRememberedFolderIsAlwaysAbsolute(t *testing.T) {
	file := filepath.Join(t.TempDir(), "library-folder.txt")
	if err := writeRememberedFolder(file, "library"); err != nil {
		t.Fatal(err)
	}
	got := readRememberedFolder(file)
	if !filepath.IsAbs(got) {
		t.Fatalf("a relative folder was remembered as %q", got)
	}
	// A hand-edited relative entry would mean a different folder next launch.
	if err := os.WriteFile(file, []byte("library\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := readRememberedFolder(file); got != "" {
		t.Fatalf("relative entry should be ignored, got %q", got)
	}
}

func TestDirWritable(t *testing.T) {
	existing := t.TempDir()
	if !dirWritable(existing) {
		t.Error("an existing writable folder should be writable")
	}
	if !dirWritable(filepath.Join(existing, "not", "yet", "created")) {
		t.Error("a folder that can be created under a writable parent should be writable")
	}
	if entries, _ := os.ReadDir(existing); len(entries) != 0 {
		t.Errorf("probing left files behind: %v", entries)
	}
	file := filepath.Join(existing, "afile")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if dirWritable(file) || dirWritable(filepath.Join(file, "sub")) {
		t.Error("a path that is (or is under) a file is not a usable folder")
	}
}
