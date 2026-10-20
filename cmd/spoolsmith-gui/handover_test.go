package main

import (
	"errors"
	"strings"
	"testing"
)

// Windows does not show an unelevated session's mapped drives to the elevated
// token UAC creates (unless EnableLinkedConnections is set), so a printer file
// opened from Z:\ and handed to the elevated instance as "Z:\..." is "not
// found" there, after the original window has already closed. The hand-over
// must carry the UNC path instead.
func TestElevatedHandOverCarriesUNCPathsForMappedDrives(t *testing.T) {
	resolve := func(path string) (string, error) {
		if strings.HasPrefix(strings.ToUpper(path), `Z:\`) {
			return `\\fileserver\it` + path[2:], nil
		}
		return "", nil
	}
	op := operation{Kind: opApply, BundlePath: `Z:\printers\Accounting.ssb`, ProfilePath: `C:\local\Front.ssb`}
	got, err := handOverPaths(op, resolve)
	if err != nil {
		t.Fatal(err)
	}
	if got.BundlePath != `\\fileserver\it\printers\Accounting.ssb` {
		t.Fatalf("mapped-drive bundle path not rewritten: %q", got.BundlePath)
	}
	if got.ProfilePath != `C:\local\Front.ssb` {
		t.Fatalf("local path changed: %q", got.ProfilePath)
	}
	unc := operation{Kind: opApply, BundlePath: `\\fileserver\it\printers\Accounting.ssb`}
	if got, err := handOverPaths(unc, resolve); err != nil || got.BundlePath != unc.BundlePath {
		t.Fatalf("a UNC path must pass through unchanged: %q, %v", got.BundlePath, err)
	}
	if got, _ := handOverPaths(operation{Kind: opApply}, resolve); got.BundlePath != "" {
		t.Fatal("empty path invented")
	}
}

// A mapped drive Windows cannot resolve (ERROR_MORE_DATA past the retries, a
// disconnected share) is reported before the relaunch, not discovered as "not
// found" by the elevated copy after this window has closed. Paths that did
// resolve are still rewritten.
func TestElevatedHandOverReportsAMappedDriveItCouldNotResolve(t *testing.T) {
	cause := errors.New("The network connection could not be found.")
	resolve := func(path string) (string, error) {
		switch {
		case strings.HasPrefix(path, `Z:\`):
			return "", cause
		case strings.HasPrefix(path, `Y:\`):
			return `\\fileserver\profiles` + path[2:], nil
		}
		return "", nil
	}
	op := operation{Kind: opApply, BundlePath: `Z:\printers\Accounting.ssb`, ProfilePath: `Y:\Front.ssb`}
	got, err := handOverPaths(op, resolve)
	if !errors.Is(err, cause) || !strings.Contains(err.Error(), `Z:\printers\Accounting.ssb`) {
		t.Fatalf("unresolved drive not reported with its path and reason: %v", err)
	}
	if got.BundlePath != `Z:\printers\Accounting.ssb` || got.ProfilePath != `\\fileserver\profiles\Front.ssb` {
		t.Fatalf("got %q, %q", got.BundlePath, got.ProfilePath)
	}
}
