//go:build !windows

// Package main hosts SpoolSmith's native GUI. The GUI itself (tailscale/walk,
// a maintained fork of lxn/walk) is a thin wrapper over the Win32 API and
// only builds on Windows; this stub keeps
// `go build ./...`/`go vet ./...` green on non-Windows CI legs, mirroring
// internal/install's existing windows.go/other.go split.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "spoolsmith-gui: the SpoolSmith GUI is Windows-only; use the spoolsmith CLI on this platform.")
	os.Exit(1)
}
