// Command mkwinres generates the Windows resource object that embeds an
// application manifest into a Go-linked PE binary. It is a build-time tool, not
// part of any shipped executable.
//
// The generated file is committed alongside its source manifest so that release
// builds stay reproducible and need no network access or third-party tooling;
// TestGeneratedResourceObjectMatchesManifest in cmd/spoolsmith-gui fails if the
// two ever drift apart.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/spilloid/spoolsmith/internal/winres"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "mkwinres:", err)
		os.Exit(1)
	}
}

func run() error {
	manifestPath := flag.String("manifest", "", "path to the application manifest to embed")
	arch := flag.String("arch", "amd64", fmt.Sprintf("target GOARCH, one of %v", winres.SupportedArches()))
	out := flag.String("o", "", "path of the .syso object to write")
	flag.Parse()

	if *manifestPath == "" || *out == "" {
		flag.Usage()
		return fmt.Errorf("both -manifest and -o are required")
	}

	manifest, err := os.ReadFile(*manifestPath)
	if err != nil {
		return err
	}
	object, err := winres.ManifestObject(manifest, *arch)
	if err != nil {
		return err
	}
	return os.WriteFile(*out, object, 0o644)
}
