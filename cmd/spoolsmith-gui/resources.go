package main

// The desktop app's application manifest is embedded in the binary as an
// RT_MANIFEST resource rather than shipped as a "spoolsmith-gui.exe.manifest"
// side-car file. Windows caches an executable's activation context per path, so
// a side-car that arrives after the first launch never takes effect: the GUI
// keeps failing InitCommonControlsEx and exits without painting a window, with
// nothing on screen to explain why. Embedding removes the ordering hazard, and
// removes the possibility of the manifest being separated from the binary when
// a package is copied, renamed or repackaged.
//
// spoolsmith-gui.exe.manifest stays the source of truth; rsrc_windows_amd64.syso
// is generated from it and committed so release builds need no extra tooling.
// Regenerate with "go generate ./cmd/spoolsmith-gui"; the drift guard in
// manifest_test.go fails if the committed object stops matching the manifest.
//
//go:generate go run github.com/spilloid/spoolsmith/internal/winres/mkwinres -manifest spoolsmith-gui.exe.manifest -arch amd64 -o rsrc_windows_amd64.syso
