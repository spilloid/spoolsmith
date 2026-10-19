//go:build windows

package main

import "testing"

// Local and UNC paths must pass through the real resolver unchanged. (A mapped
// drive needs a real share; see the hardware checklist in the dev log.)
func TestUniversalNameLeavesLocalAndUNCPathsAlone(t *testing.T) {
	for _, path := range []string{`C:\Windows\notepad.exe`, `\server\share\a.ssb`, `relative.ssb`, ``} {
		if got := universalName(path); got != "" {
			t.Errorf("universalName(%q) = %q, want unchanged", path, got)
		}
	}
}
