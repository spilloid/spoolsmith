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

// runClone backs `spoolsmith copy`: it reads one already-working queue off
// this machine and writes a bundle another machine can apply.
//
// The point of this command is that the operator stops being the integration
// point. `profile capture` asks for the exact registered driver name, which
// means reading it off Get-PrinterDriver and retyping it correctly. Here the
// queue that already prints is the source of truth for its own settings.
func runClone(ctx context.Context, args []string, input io.Reader, stdout, stderr io.Writer, app application) int {
	var queueName, bundlePath, note string
	includeDriver := false
	includeAll := false
	positional := 0
	for index := 0; index < len(args); index++ {
		switch args[index] {
		case "--include-driver":
			includeDriver = true
		case "--all":
			includeAll = true
		case "--note":
			if index+1 >= len(args) || strings.TrimSpace(args[index+1]) == "" {
				return usageError(stdout, stderr, "copy", errors.New("--note requires a value"))
			}
			note = args[index+1]
			index++
		default:
			if strings.HasPrefix(args[index], "-") {
				return usageError(stdout, stderr, "copy", fmt.Errorf("unknown option %q", args[index]))
			}
			switch positional {
			case 0:
				queueName = args[index]
			case 1:
				bundlePath = args[index]
			default:
				return usageError(stdout, stderr, "copy", errors.New("copy takes at most one queue name and one bundle path"))
			}
			positional++
		}
	}
	if includeAll {
		if positional > 1 {
			return usageError(stdout, stderr, "copy", errors.New("copy --all takes at most one output directory"))
		}
		outputDir := queueName
		if outputDir == "" {
			outputDir = "."
		}
		return runCloneAll(ctx, outputDir, includeDriver, note, stdout, stderr, app)
	}
	if queueName == "" {
		selected, selectErr := selectInstalledQueue(ctx, input, stderr, app)
		if selectErr != nil {
			return commandError(stdout, stderr, "copy", selectErr, int(install.ExitUsageError))
		}
		queueName = selected
	}
	if bundlePath == "" {
		bundlePath = defaultBundleName(queueName)
		fmt.Fprintf(stderr, "Writing to %s\n", bundlePath)
	}

	created, err := bundle.Create(ctx, app.environment, bundle.Collector(app.collect), bundle.CreateOptions{
		QueueName:     queueName,
		Path:          bundlePath,
		Note:          note,
		IncludeDriver: includeDriver,
		CreatedBy:     "spoolsmith " + versionString(),
		SourceHost:    hostName(),
		Progress:      func(step string) { fmt.Fprintln(stderr, step) },
	})
	if err != nil {
		code := install.ExitGeneralError
		if errors.Is(err, bundle.ErrNeedsAdministrator) {
			code = install.ExitPreflight
		}
		return commandError(stdout, stderr, "copy", err, int(code))
	}
	manifest := created.Manifest

	fmt.Fprintf(stderr, "Wrote %s. Apply it on another machine with: spoolsmith apply %s --dry-run\n", bundlePath, filepath.Base(bundlePath))
	if manifest.Driver == nil {
		fmt.Fprintln(stderr, "No driver payload: the target machine must already have this driver registered. Re-run with --include-driver to carry it.")
	}
	return encodeSuccess(stdout, stderr, "copy", manifest)
}

// copyAllResult reports what `copy --all` did with every installed queue.
type copyAllResult struct {
	OutputDir string         `json:"output_dir"`
	Requested int            `json:"requested"`
	Written   int            `json:"written"`
	Skipped   int            `json:"skipped"`
	Failed    int            `json:"failed"`
	Queues    []copyAllQueue `json:"queues"`
}

// copyAllQueue is one queue's outcome within a `copy --all` run.
type copyAllQueue struct {
	Name   string `json:"name"`
	Status string `json:"status"` // "written", "skipped", or "error"
	Bundle string `json:"bundle,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// runCloneAll bundles every copyable queue on this machine into outputDir,
// one file per queue, reusing the same bundle.Create the single-queue path
// uses. Unlike a single copy, this is a set of independent, non-mutating
// exports rather than one transaction: a queue that can't be copied, or
// fails partway (an admin-rights or driver-store error), is reported and
// skipped rather than aborting the rest of the batch -- an MSP tech running
// this over thirty queues should not lose the other twenty-nine to one bad
// one.
func runCloneAll(ctx context.Context, outputDir string, includeDriver bool, note string, stdout, stderr io.Writer, app application) int {
	queues, err := install.ListPrinters(ctx, app.environment)
	if err != nil {
		return commandError(stdout, stderr, "copy", err, int(install.ExitGeneralError))
	}
	if len(queues) == 0 {
		return commandError(stdout, stderr, "copy", errors.New("this PC has no printer queues installed"), int(install.ExitGeneralError))
	}
	if err := os.MkdirAll(outputDir, 0o700); err != nil {
		return commandError(stdout, stderr, "copy", fmt.Errorf("copy --all: %w", err), int(install.ExitGeneralError))
	}

	result := copyAllResult{OutputDir: outputDir, Requested: len(queues)}
	for _, queue := range queues {
		if reason := queue.CopyBlockedReason(); reason != "" {
			fmt.Fprintf(stderr, "! %s: skipped -- %s\n", queue.PrinterName, reason)
			result.Skipped++
			result.Queues = append(result.Queues, copyAllQueue{Name: queue.PrinterName, Status: "skipped", Reason: reason})
			continue
		}
		path := filepath.Join(outputDir, defaultBundleName(queue.PrinterName))
		_, err := bundle.Create(ctx, app.environment, bundle.Collector(app.collect), bundle.CreateOptions{
			QueueName:     queue.PrinterName,
			Path:          path,
			Note:          note,
			IncludeDriver: includeDriver,
			CreatedBy:     "spoolsmith " + versionString(),
			SourceHost:    hostName(),
			Progress:      func(step string) { fmt.Fprintf(stderr, "  %s: %s\n", queue.PrinterName, step) },
		})
		if err != nil {
			fmt.Fprintf(stderr, "x %s: %v\n", queue.PrinterName, err)
			result.Failed++
			result.Queues = append(result.Queues, copyAllQueue{Name: queue.PrinterName, Status: "error", Reason: err.Error()})
			continue
		}
		fmt.Fprintf(stderr, "Wrote %s -> %s\n", queue.PrinterName, path)
		result.Written++
		result.Queues = append(result.Queues, copyAllQueue{Name: queue.PrinterName, Status: "written", Bundle: path})
	}
	fmt.Fprintf(stderr, "Wrote %d of %d queues to %s (%d skipped, %d failed).\n", result.Written, result.Requested, outputDir, result.Skipped, result.Failed)

	code := install.ExitSuccess
	if result.Written == 0 {
		fmt.Fprintln(stderr, "spoolsmith copy --all: no queues were copied")
		code = install.ExitGeneralError
	}
	if err := encodeJSON(stdout, result); err != nil {
		fmt.Fprintf(stderr, "spoolsmith copy: encode result: %v\n", err)
		return int(install.ExitGeneralError)
	}
	return int(code)
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
		case "--offline":
			options.Offline = true
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

	driver, stageDir, err := opened.PrepareDriver()
	if err != nil {
		return commandError(stdout, stderr, "apply", err, int(install.ExitGeneralError))
	}
	if driver != nil {
		options.BundleDriver = driver
		fmt.Fprintf(stderr, "Verified %d driver files from the bundle into %s\n", driver.FileCount, stageDir)
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

// defaultBundleName derives a bundle file name from the queue being copied, so
// the common case needs no path at all.
//
// Two differently punctuated queue names can still derive the same file name.
// That is safe rather than merely tolerated: bundle.Write refuses to overwrite
// an existing file, so a collision stops with an error naming the path instead
// of quietly replacing a bundle the operator still needed.
func defaultBundleName(queueName string) string {
	var builder strings.Builder
	for _, r := range queueName {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			builder.WriteRune(r)
		default:
			builder.WriteRune('-')
		}
	}
	name := strings.Trim(builder.String(), "-")
	if name == "" {
		name = "printer"
	}
	return name + ".ssb"
}
