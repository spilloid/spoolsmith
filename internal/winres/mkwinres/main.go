// Command mkwinres generates the Windows resource object that embeds an
// application manifest and/or the product icon into a Go-linked PE binary. It is
// a build-time tool, not part of any shipped executable.
//
// The generated file is committed alongside its sources so that release builds
// stay reproducible and need no network access or third-party tooling; the drift
// tests in cmd/spoolsmith and cmd/spoolsmith-gui fail if a committed object ever
// stops matching what this tool would produce from its inputs.
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
	manifestPath := flag.String("manifest", "", "path to the application manifest to embed (optional)")
	iconPath := flag.String("icon", "", "path to the square PNG product icon to embed (optional)")
	arch := flag.String("arch", "amd64", fmt.Sprintf("target GOARCH, one of %v", winres.SupportedArches()))
	out := flag.String("o", "", "path of the .syso object to write")
	flag.Parse()

	if *out == "" || (*manifestPath == "" && *iconPath == "") {
		flag.Usage()
		return fmt.Errorf("-o and at least one of -manifest or -icon are required")
	}

	var resources []winres.Resource
	if *manifestPath != "" {
		manifest, err := os.ReadFile(*manifestPath)
		if err != nil {
			return err
		}
		if len(manifest) == 0 {
			return fmt.Errorf("manifest %s is empty", *manifestPath)
		}
		resources = append(resources, winres.ManifestResource(manifest))
	}
	if *iconPath != "" {
		file, err := os.Open(*iconPath)
		if err != nil {
			return err
		}
		defer file.Close()
		iconResources, err := winres.IconResourcesFromPNG(file)
		if err != nil {
			return fmt.Errorf("%s: %w", *iconPath, err)
		}
		resources = append(resources, iconResources...)
	}

	object, err := winres.Object(resources, *arch)
	if err != nil {
		return err
	}
	return os.WriteFile(*out, object, 0o644)
}
