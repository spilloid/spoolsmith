package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/spilloid/spoolsmith/internal/inspect"
	"github.com/spilloid/spoolsmith/internal/install"
)

func runDiscover(ctx context.Context, args []string, stdout, stderr io.Writer, app application) int {
	if len(args) != 1 {
		return usageError(stdout, stderr, "discover", errors.New("discover requires one explicit IPv4 CIDR, such as 192.168.1.0/24"))
	}
	if app.discover == nil {
		return commandError(stdout, stderr, "discover", errors.New("discovery unavailable"), 1)
	}
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	fmt.Fprintln(stderr, "Scanning for printer candidates; open ports do not establish driver compatibility.")
	result, err := app.discover(ctx, args[0])
	response := struct {
		Network    string                  `json:"network"`
		Scanned    int                     `json:"scanned"`
		Candidates []inspect.InspectResult `json:"candidates"`
		Error      string                  `json:"error,omitempty"`
	}{Network: result.Network, Scanned: result.Scanned, Candidates: []inspect.InspectResult{}}
	for _, candidate := range result.Candidates {
		response.Candidates = append(response.Candidates, inspect.Inspect(candidate.Evidence))
	}
	if err != nil {
		response.Error = err.Error()
		fmt.Fprintln(stderr, err)
	}
	code := encodeSuccess(stdout, stderr, "discover", response)
	if err != nil {
		return 1
	}
	return code
}

func runProfile(ctx context.Context, args []string, stdout, stderr io.Writer, app application) int {
	if len(args) > 0 && args[0] == "edit" {
		return runProfileEdit(args[1:], stdout, stderr)
	}
	if len(args) < 3 || args[0] != "capture" {
		return usageError(stdout, stderr, "profile", errors.New("profile requires capture <target> <file> --name <queue> --driver <installed-driver-name>"))
	}
	p := install.Profile{Version: 1}
	seen := map[string]bool{}
	for i := 3; i < len(args); i += 2 {
		flag := args[i]
		if (flag != "--name" && flag != "--driver") || seen[flag] || i+1 >= len(args) || strings.TrimSpace(args[i+1]) == "" {
			return usageError(stdout, stderr, "profile", fmt.Errorf("invalid or duplicate profile option %q", flag))
		}
		seen[flag] = true
		if flag == "--name" {
			p.PrinterName = args[i+1]
		} else {
			p.DriverName = args[i+1]
		}
	}
	if !seen["--name"] || !seen["--driver"] {
		return usageError(stdout, stderr, "profile", errors.New("--name and --driver are required"))
	}
	result, err := app.collect(ctx, args[1])
	if err != nil {
		return commandError(stdout, stderr, "profile", err, 1)
	}
	p.Target, p.Evidence = result.Evidence.IP, result.Evidence
	if err := install.SaveProfile(args[2], p); err != nil {
		return commandError(stdout, stderr, "profile", err, 1)
	}
	fmt.Fprintln(stderr, "Saved printer profile. The driver name is operator-selected; installation checks that it is registered locally.")
	return encodeSuccess(stdout, stderr, "profile", p)
}

func runProfileEdit(args []string, stdout, stderr io.Writer) int {
	if len(args) < 2 {
		return usageError(stdout, stderr, "profile edit", errors.New("profile edit requires <file> and at least one of --name, --driver, --target"))
	}
	p, err := install.LoadProfile(args[0])
	if err != nil {
		return commandError(stdout, stderr, "profile edit", err, 1)
	}
	seen := map[string]bool{}
	for i := 1; i < len(args); i += 2 {
		flag := args[i]
		if flag == "--clear-package" {
			if seen[flag] || seen["--package"] || seen["--archive"] {
				return usageError(stdout, stderr, "profile edit", errors.New("--clear-package cannot be combined with package options"))
			}
			seen[flag] = true
			p.DriverPackage = nil
			i--
			continue
		}
		if seen[flag] || i+1 >= len(args) || strings.TrimSpace(args[i+1]) == "" {
			return usageError(stdout, stderr, "profile edit", fmt.Errorf("invalid or duplicate option %q", flag))
		}
		seen[flag] = true
		switch flag {
		case "--name":
			p.PrinterName = args[i+1]
		case "--driver":
			p.DriverName = args[i+1]
		case "--target":
			p.Target = args[i+1]
		case "--package", "--archive":
			if seen["--clear-package"] {
				return usageError(stdout, stderr, "profile edit", errors.New("--clear-package cannot be combined with package options"))
			}
			if p.DriverPackage == nil {
				p.DriverPackage = &install.PackageSelection{}
			}
			if flag == "--package" {
				p.DriverPackage.ID = args[i+1]
			} else {
				p.DriverPackage.Archive = args[i+1]
			}
		default:
			return usageError(stdout, stderr, "profile edit", fmt.Errorf("unknown option %q", flag))
		}
	}
	backup, err := install.EditProfile(args[0], p)
	if err != nil {
		return commandError(stdout, stderr, "profile edit", err, 1)
	}
	fmt.Fprintf(stderr, "Profile updated; previous version saved to %s. Preview with configure --profile %q --dry-run. Changing the queue name creates a separate queue; remove the old queue by name if needed.\n", backup, args[0])
	return encodeSuccess(stdout, stderr, "profile edit", p)
}
