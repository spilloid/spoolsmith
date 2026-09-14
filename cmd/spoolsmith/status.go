package main

import (
	"context"
	"errors"
	"github.com/spilloid/spoolsmith/internal/install"
	"io"
)

func runStatus(ctx context.Context, args []string, stdout, stderr io.Writer, app application) int {
	if len(args) == 3 && args[2] == "--json" {
		args = args[:2]
	}
	if len(args) != 2 || args[0] != "--profile" {
		return usageError(stdout, stderr, "status", errors.New("status requires --profile <file> [--json]"))
	}
	p, err := install.LoadProfile(args[1])
	if err != nil {
		return commandError(stdout, stderr, "status", err, int(install.ExitUsageError))
	}
	status, err := install.CheckStatus(ctx, app.environment, p)
	if err != nil {
		return commandError(stdout, stderr, "status", err, int(install.ExitGeneralError))
	}
	if err := encodeJSON(stdout, status); err != nil {
		return int(install.ExitGeneralError)
	}
	if !status.Compliant {
		return int(install.ExitUnresolved)
	}
	return 0
}
