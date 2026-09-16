package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spilloid/spoolsmith/internal/install"
)

// runRepoint points an existing queue at a different printer address.
func runRepoint(ctx context.Context, args []string, input io.Reader, stdout, stderr io.Writer, app application) int {
	options := install.RepointOptions{}
	positional := 0
	for index := 0; index < len(args); index++ {
		switch args[index] {
		case "--dry-run", "--what-if":
			options.DryRun = true
		case "--yes":
			options.Yes = true
			options.NonInteractive = true
		case "--non-interactive":
			options.NonInteractive = true
		case "--json":
			options.JSON = true
		default:
			if strings.HasPrefix(args[index], "-") {
				return usageError(stdout, stderr, "repoint", fmt.Errorf("unknown option %q", args[index]))
			}
			switch positional {
			case 0:
				options.PrinterName = args[index]
			case 1:
				options.NewAddress = args[index]
			default:
				return usageError(stdout, stderr, "repoint", errors.New("repoint takes one queue name and one IP address"))
			}
			positional++
		}
	}
	if options.PrinterName == "" || options.NewAddress == "" {
		return usageError(stdout, stderr, "repoint", errors.New("repoint requires <queue-name> <new-ip>"))
	}

	options.Compact = app.outputTerminal && !options.JSON
	outcome, code := app.workflow.RunRepoint(ctx, app.environment, input, stderr, app.inputTerminal, options)
	if outcome.Error != "" {
		fmt.Fprintf(stderr, "spoolsmith repoint: %s\n", outcome.Error)
	}
	if app.outputTerminal && !options.JSON {
		return int(code)
	}
	if err := encodeJSON(stdout, outcome); err != nil {
		fmt.Fprintf(stderr, "spoolsmith repoint: encode result: %v\n", err)
		return int(install.ExitGeneralError)
	}
	return int(code)
}
