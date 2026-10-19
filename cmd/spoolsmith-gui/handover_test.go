package main

import (
	"strings"
	"testing"
)

// Windows does not show an unelevated session's mapped drives to the elevated
// token UAC creates (unless EnableLinkedConnections is set), so a printer file
// opened from Z:\ and handed to the elevated instance as "Z:\..." is "not
// found" there, after the original window has already closed. The hand-over
// must carry the UNC path instead.
func TestElevatedHandOverCarriesUNCPathsForMappedDrives(t *testing.T) {
	resolve := func(path string) string {
		if strings.HasPrefix(strings.ToUpper(path), `Z:\`) {
			return `\fileserver\it` + path[2:]
		}
		return ""
	}
	op := operation{Kind: opApply, BundlePath: `Z:\printers\Accounting.ssb`, ProfilePath: `C:\local\Front.ssb`}
	got := handOverPaths(op, resolve)
	if got.BundlePath != `\fileserver\it\printers\Accounting.ssb` {
		t.Fatalf("mapped-drive bundle path not rewritten: %q", got.BundlePath)
	}
	if got.ProfilePath != `C:\local\Front.ssb` {
		t.Fatalf("local path changed: %q", got.ProfilePath)
	}
	if handOverPaths(operation{Kind: opApply}, resolve).BundlePath != "" {
		t.Fatal("empty path invented")
	}
}
