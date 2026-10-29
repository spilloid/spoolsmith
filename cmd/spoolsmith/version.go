package main

import "strings"

// version is stamped at release time with
// -ldflags "-X main.version=$Tag". An unstamped build says so rather than
// claiming a release number it may not be.
var version = ""

// versionResponse is `spoolsmith version`'s JSON shape when stdout is
// redirected.
type versionResponse struct {
	Version string `json:"version"`
}

func versionString() string {
	if strings.TrimSpace(version) == "" {
		return "(development build)"
	}
	return version
}
