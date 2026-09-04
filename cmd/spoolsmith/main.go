package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spilloid/spoolsmith/internal/evidence"
	"github.com/spilloid/spoolsmith/internal/inspect"
)

func main() {
	if len(os.Args) != 3 || os.Args[1] != "inspect" {
		fmt.Fprintln(os.Stderr, "usage: spoolsmith inspect <fixture.json>")
		os.Exit(2)
	}

	e, err := evidence.LoadFixture(os.Args[2])
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
