package main

// The command-line tool carries the product icon as an embedded Windows
// resource, so spoolsmith.exe shows it in Explorer and in a pinned shortcut. It
// has no manifest of its own: unlike the desktop app it needs no comctl32
// activation context.
//
// assets/icon/spoolsmith.png is the source of truth; rsrc_windows_amd64.syso is
// generated from it and committed so release builds need no extra tooling.
// Regenerate with "go generate ./cmd/spoolsmith"; the drift guard in
// resources_test.go fails if the committed object stops matching the icon.
//
//go:generate go run github.com/spilloid/spoolsmith/internal/winres/mkwinres -icon ../../assets/icon/spoolsmith.png -arch amd64 -o rsrc_windows_amd64.syso
