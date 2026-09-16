package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spilloid/spoolsmith/internal/bundle"
	"github.com/spilloid/spoolsmith/internal/install"
)

// runClone reads one already-working queue off this machine and writes a
// bundle another machine can apply.
//
// The point of this command is that the operator stops being the integration
// point. `profile capture` asks for the exact registered driver name, which
// means reading it off Get-PrinterDriver and retyping it correctly. Here the
// queue that already prints is the source of truth for its own settings.
func runClone(ctx context.Context, args []string, stdout, stderr io.Writer, app application) int {
	var queueName, bundlePath, note string
	includeDriver := false
	positional := 0
	for index := 0; index < len(args); index++ {
		switch args[index] {
		case "--include-driver":
			includeDriver = true
		case "--note":
			if index+1 >= len(args) || strings.TrimSpace(args[index+1]) == "" {
				return usageError(stdout, stderr, "clone", errors.New("--note requires a value"))
			}
			note = args[index+1]
			index++
		default:
			if strings.HasPrefix(args[index], "-") {
				return usageError(stdout, stderr, "clone", fmt.Errorf("unknown option %q", args[index]))
			}
			switch positional {
			case 0:
				queueName = args[index]
			case 1:
				bundlePath = args[index]
			default:
				return usageError(stdout, stderr, "clone", errors.New("clone takes one queue name and one bundle path"))
			}
			positional++
		}
	}
	if queueName == "" || bundlePath == "" {
		return usageError(stdout, stderr, "clone", errors.New("clone requires <installed-queue-name> <bundle-file>"))
	}

	cloned, err := install.CloneQueue(ctx, app.environment, queueName)
	if err != nil {
		return commandError(stdout, stderr, "clone", err, int(install.ExitGeneralError))
	}
	fmt.Fprintf(stderr, "Queue %q maps %s using driver %q.\n", cloned.PrinterName, cloned.HostAddress, cloned.DriverName)

	// The printer's own evidence is captured here, not copied from the queue,
	// so applying the bundle elsewhere still checks it is talking to the same
	// device rather than trusting the file.
	probeResult, err := app.collect(ctx, cloned.HostAddress)
	if err != nil {
		return commandError(stdout, stderr, "clone", fmt.Errorf("capture evidence from %s: %w", cloned.HostAddress, err), int(install.ExitGeneralError))
	}
	profile := install.Profile{
		Version:     1,
		Target:      probeResult.Evidence.IP,
		PrinterName: cloned.PrinterName,
		DriverName:  cloned.DriverName,
		Evidence:    probeResult.Evidence,
	}
	if err := profile.Validate(); err != nil {
		return commandError(stdout, stderr, "clone", err, int(install.ExitGeneralError))
	}

	manifest := bundle.Manifest{
		Version:    bundle.Version,
		CreatedBy:  "spoolsmith " + versionString(),
		SourceHost: hostName(),
		Note:       note,
		Profile:    profile,
	}

	payloadRoot := ""
	if includeDriver {
		exportRoot, err := os.MkdirTemp("", "spoolsmith-export-")
		if err != nil {
			return commandError(stdout, stderr, "clone", err, int(install.ExitGeneralError))
		}
		// Retained rather than deleted: an export that produced an unusable
		// bundle is worth being able to look at, and this matches how the
		// existing driver-staging path treats its own working directory.
		fmt.Fprintf(stderr, "Exporting driver %q to %s\n", cloned.DriverName, exportRoot)
		export, err := install.ExportDriver(ctx, app.environment, cloned.DriverName, exportRoot)
		if err != nil {
			return commandError(stdout, stderr, "clone", err, int(install.ExitGeneralError))
		}
		infRelative, err := install.FindExportedINF(exportRoot, export.OriginalName)
		if err != nil {
			return commandError(stdout, stderr, "clone", err, int(install.ExitGeneralError))
		}
		manifest.Driver = &bundle.DriverPayload{
			WindowsDriverName: cloned.DriverName,
			INF:               infRelative,
			ExportedFrom:      export.PublishedName,
		}
		payloadRoot = exportRoot
	}

	if err := bundle.Write(bundlePath, manifest, payloadRoot); err != nil {
		return commandError(stdout, stderr, "clone", err, int(install.ExitGeneralError))
	}

	opened, err := bundle.Open(bundlePath)
	if err != nil {
		return commandError(stdout, stderr, "clone", fmt.Errorf("wrote %s but could not read it back: %w", bundlePath, err), int(install.ExitGeneralError))
	}
	defer opened.Close()
	if err := opened.Verify(); err != nil {
		return commandError(stdout, stderr, "clone", err, int(install.ExitGeneralError))
	}
	fmt.Fprintf(stderr, "Wrote %s. Apply it on another machine with: spoolsmith apply %s --dry-run\n", bundlePath, filepath.Base(bundlePath))
	if manifest.Driver == nil {
		fmt.Fprintln(stderr, "No driver payload: the target machine must already have this driver registered. Re-run with --include-driver to carry it.")
	}
	return encodeSuccess(stdout, stderr, "clone", opened.Manifest)
}

// runApply maps the bundled setup onto this machine.
func runApply(ctx context.Context, args []string, input io.Reader, stdout, stderr io.Writer, app application) int {
	var bundlePath, planHash string
	options := install.InstallOptions{}
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
		case "--update":
			options.UpdateExisting = true
		case "--plan-hash":
			if index+1 >= len(args) || strings.TrimSpace(args[index+1]) == "" {
				return usageError(stdout, stderr, "apply", errors.New("--plan-hash requires the reviewed plan's fingerprint"))
			}
			planHash = strings.TrimSpace(args[index+1])
			index++
		default:
			if strings.HasPrefix(args[index], "-") {
				return usageError(stdout, stderr, "apply", fmt.Errorf("unknown option %q", args[index]))
			}
			if bundlePath != "" {
				return usageError(stdout, stderr, "apply", errors.New("apply takes exactly one bundle path"))
			}
			bundlePath = args[index]
		}
	}
	if bundlePath == "" {
		return usageError(stdout, stderr, "apply", errors.New("apply requires <bundle-file>"))
	}
	options.ConfirmPlanHash = planHash

	opened, err := bundle.Open(bundlePath)
	if err != nil {
		return commandError(stdout, stderr, "apply", err, int(install.ExitUsageError))
	}
	defer opened.Close()

	profile := opened.Manifest.Profile
	options.Profile = &profile

	if opened.Manifest.Driver != nil {
		stageDir := opened.StageDir()
		// Extraction happens before the plan is shown so the plan can state
		// what is actually on disk, and so a corrupt payload fails here rather
		// than halfway through a privileged staging step. Nothing is staged
		// into Windows until the plan is confirmed.
		if err := opened.EnsureExtracted(stageDir); err != nil {
			return commandError(stdout, stderr, "apply", err, int(install.ExitGeneralError))
		}
		options.BundleDriver = &install.BundleDriver{
			WindowsDriverName: opened.Manifest.Driver.WindowsDriverName,
			PublishedName:     opened.Manifest.Driver.ExportedFrom,
			SourceHost:        opened.Manifest.SourceHost,
			INF:               opened.Manifest.Driver.INF,
			StageDirName:      opened.StageDirName(),
			FileCount:         len(opened.Manifest.Driver.Files),
			TotalBytes:        opened.Manifest.TotalPayloadBytes(),
			PayloadDigest:     opened.PayloadDigest(),
		}
		fmt.Fprintf(stderr, "Verified %d driver files from the bundle into %s\n", len(opened.Manifest.Driver.Files), stageDir)
	}

	options.Compact = app.outputTerminal && !options.JSON
	outcome, code := app.workflow.RunInstall(ctx, app.environment, input, stderr, app.inputTerminal, options)
	outcome.Operation = "apply"
	if outcome.Error != "" {
		fmt.Fprintf(stderr, "spoolsmith apply: %s\n", outcome.Error)
	}
	if app.outputTerminal && !options.JSON {
		if outcome.PlanHash != "" && options.DryRun {
			fmt.Fprintf(stderr, "Reviewed plan fingerprint: %s\n", outcome.PlanHash)
		}
		return int(code)
	}
	if err := encodeJSON(stdout, outcome); err != nil {
		fmt.Fprintf(stderr, "spoolsmith apply: encode result: %v\n", err)
		return int(install.ExitGeneralError)
	}
	return int(code)
}

// runBundle reads a bundle without touching the network or this machine.
func runBundle(args []string, stdout, stderr io.Writer) int {
	if len(args) != 2 || args[0] != "inspect" {
		return usageError(stdout, stderr, "bundle", errors.New("bundle requires inspect <bundle-file>"))
	}
	opened, err := bundle.Open(args[1])
	if err != nil {
		return commandError(stdout, stderr, "bundle inspect", err, int(install.ExitUsageError))
	}
	defer opened.Close()
	if err := opened.Verify(); err != nil {
		return commandError(stdout, stderr, "bundle inspect", err, int(install.ExitGeneralError))
	}
	m := opened.Manifest
	fmt.Fprintf(stderr, "Bundle: %s\n  Created: %s by %s\n  Source machine: %s\n", args[1], m.Created, shown(m.CreatedBy), shown(m.SourceHost))
	if m.Note != "" {
		fmt.Fprintf(stderr, "  Note: %s\n", m.Note)
	}
	fmt.Fprintf(stderr, "  Queue: %s\n  Target: %s (RAW TCP 9100)\n  Driver: %s\n", m.Profile.PrinterName, m.Profile.Target, m.Profile.DriverName)
	if m.Driver == nil {
		fmt.Fprintln(stderr, "  Driver payload: none — the target machine must already have this driver registered.")
	} else {
		fmt.Fprintf(stderr, "  Driver payload: %d files, %d bytes, INF %s (from %s)\n", len(m.Driver.Files), m.TotalPayloadBytes(), m.Driver.INF, shown(m.Driver.ExportedFrom))
		fmt.Fprintln(stderr, "  All payload files match the manifest's hashes.")
	}
	fmt.Fprintln(stderr, "  This checks the bundle's integrity, not the printer. Preview against a real target with: spoolsmith apply <bundle> --dry-run")
	return encodeSuccess(stdout, stderr, "bundle inspect", m)
}

func shown(value string) string {
	if strings.TrimSpace(value) == "" {
		return "(not recorded)"
	}
	return value
}

func hostName() string {
	name, err := os.Hostname()
	if err != nil {
		return ""
	}
	return name
}
