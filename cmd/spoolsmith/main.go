package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/spilloid/spoolsmith/internal/evidence"
	"github.com/spilloid/spoolsmith/internal/inspect"
	"github.com/spilloid/spoolsmith/internal/probe"
)

func main() {
	if len(os.Args) == 3 && os.Args[1] == "inspect" {
		runInspect(os.Args[2])
		return
	}
	if len(os.Args) == 4 && os.Args[1] == "catalog" && os.Args[2] == "probe" {
		runCatalogProbe(os.Args[3])
		return
	}
	fmt.Fprintln(os.Stderr, "usage: spoolsmith inspect <target>\n       spoolsmith catalog probe <ip>")
	os.Exit(2)
}

func runInspect(target string) {
	var e evidence.Evidence
	info, statErr := os.Stat(target)
	var err error
	if statErr == nil && info.Mode().IsRegular() {
		e, err = evidence.LoadFixture(target)
	} else {
		var result probe.Result
		result, err = probe.Collect(context.Background(), target)
		e = result.Evidence
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "spoolsmith inspect: %v\n", err)
		os.Exit(1)
	}

	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(inspect.Inspect(e)); err != nil {
		fmt.Fprintf(os.Stderr, "spoolsmith inspect: encode result: %v\n", err)
		os.Exit(1)
	}
}

func runCatalogProbe(target string) {
	result, err := probe.Collect(context.Background(), target)
	if err != nil {
		fmt.Fprintf(os.Stderr, "spoolsmith catalog probe: %v\n", err)
		os.Exit(1)
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(result); err != nil {
		fmt.Fprintf(os.Stderr, "spoolsmith catalog probe: encode result: %v\n", err)
		os.Exit(1)
	}
}
