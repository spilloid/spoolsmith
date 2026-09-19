package main

// The desktop app's application manifest and product icon are embedded in the
// binary as Windows resources rather than shipped as side-car files. Windows
// caches an executable's activation context per path, so a "spoolsmith-gui.exe.manifest"
// side-car that arrives after the first launch never takes effect: the GUI keeps
// failing InitCommonControlsEx and exits without painting a window, with nothing
// on screen to explain why. Embedding removes the ordering hazard, and removes
// the possibility of the manifest being separated from the binary when a
// package is copied, renamed or repackaged. The icon is embedded for the same
// last reason: it is what Explorer, the taskbar and every window title bar show.
//
// spoolsmith-gui.exe.manifest and assets/icon/spoolsmith.png stay the sources
// of truth; rsrc_windows_amd64.syso is generated from them and committed so
// release builds need no extra tooling. Regenerate with
// "go generate ./cmd/spoolsmith-gui"; the drift guard in manifest_test.go fails
// if the committed object stops matching either source.
//
//go:generate go run github.com/spilloid/spoolsmith/internal/winres/mkwinres -manifest spoolsmith-gui.exe.manifest -icon ../../assets/icon/spoolsmith.png -arch amd64 -o rsrc_windows_amd64.syso
