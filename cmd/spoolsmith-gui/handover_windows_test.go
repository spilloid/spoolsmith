//go:build windows

package main

import (
	"errors"
	"testing"

	"golang.org/x/sys/windows"
)

// Local, UNC and relative paths must pass through the real resolver unchanged
// and without error. (A mapped drive needs a real share; see the hardware
// checklist in the dev log.)
func TestUniversalNameLeavesLocalAndUNCPathsAlone(t *testing.T) {
	for _, path := range []string{`C:\Windows\notepad.exe`, `\\server\share\a.ssb`, `\\?\UNC\server\share\a.ssb`, `relative.ssb`, ``} {
		if got, err := universalName(path); got != "" || err != nil {
			t.Errorf("universalName(%q) = %q, %v; want unchanged", path, got, err)
		}
	}
}

// WNetGetUniversalNameW answers ERROR_MORE_DATA with the size it needs when
// the buffer is too small; the call is retried with that size instead of
// giving up and handing over the drive-letter path.
func TestCallGrowingRetriesWithTheSizeWindowsAsksFor(t *testing.T) {
	const need = 9000
	var sizes []int
	buf, err := callGrowing(4096, func(buf []byte, size *uint32) error {
		sizes = append(sizes, len(buf))
		if len(buf) < need {
			*size = need
			return windows.ERROR_MORE_DATA
		}
		return nil
	})
	if err != nil || len(buf) != need || len(sizes) != 2 {
		t.Fatalf("got %d bytes, %v after calls with %v", len(buf), err, sizes)
	}

	// Any other error, or a size that does not grow, is returned rather than looped on.
	_, err = callGrowing(4096, func([]byte, *uint32) error { return windows.ERROR_NOT_CONNECTED })
	if !errors.Is(err, windows.ERROR_NOT_CONNECTED) {
		t.Fatalf("other error: %v", err)
	}
	calls := 0
	_, err = callGrowing(4096, func([]byte, *uint32) error { calls++; return windows.ERROR_MORE_DATA })
	if !errors.Is(err, windows.ERROR_MORE_DATA) || calls != 1 {
		t.Fatalf("no growth: %v after %d calls", err, calls)
	}
}
