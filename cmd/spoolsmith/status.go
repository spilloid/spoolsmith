package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spilloid/spoolsmith/internal/bundle"
	"github.com/spilloid/spoolsmith/internal/install"
)

func runStatus(ctx context.Context, args []string, stdout, stderr io.Writer, app application) int {
	// --json is accepted in any position and changes nothing: the result on
	// stdout is always JSON.
	var path string
	jsonSeen := false
	for index := 0; index < len(args); index++ {
		switch args[index] {
		case "--json":
			if jsonSeen {
				return usageError(stdout, stderr, "status", errors.New("duplicate flag --json"))
			}
			jsonSeen = true
		case "--profile":
			value, ok := takeValue(args, index)
			if !ok || path != "" {
				return usageError(stdout, stderr, "status", errors.New("status requires --profile <file> [--json]"))
			}
			path = value
			index++
		default:
			return usageError(stdout, stderr, "status", fmt.Errorf("unexpected argument %q; status requires --profile <file> [--json]", args[index]))
		}
	}
	if path == "" {
		return usageError(stdout, stderr, "status", errors.New("status requires --profile <file> [--json]"))
	}
	p, err := bundle.LoadProfile(path)
	if err != nil {
		return commandError(stdout, stderr, "status", err, int(install.ExitUsageError))
	}
	status, err := install.CheckStatus(ctx, app.environment, p)
	if err != nil {
		return commandError(stdout, stderr, "status", err, int(install.ExitGeneralError))
	}
	// Local configuration only: this never contacts the printer or proves it
	// prints, matching the GUI's own status explanation.
	if status.Compliant {
		fmt.Fprintf(stderr, "%s: local configuration matches the saved profile.\n", p.PrinterName)
	} else {
		fmt.Fprintf(stderr, "%s: local configuration does not match the saved profile:\n  %s\n", p.PrinterName, strings.Join(status.Mismatches, "\n  "))
	}
	if err := encodeJSON(stdout, status); err != nil {
		return int(install.ExitGeneralError)
	}
	if !status.Compliant {
		return int(install.ExitUnresolved)
	}
	return 0
}
