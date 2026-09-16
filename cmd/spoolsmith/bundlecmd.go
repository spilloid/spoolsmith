package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spilloid/spoolsmith/internal/bundle"
	"github.com/spilloid/spoolsmith/internal/install"
	"github.com/spilloid/spoolsmith/internal/probe"
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
	positional := 0
	for index := 0; index < len(args); index++ {
		switch args[index] {
		case "--include-driver":
			includeDriver = true
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

	// Exporting a driver reads the protected driver store through pnputil,
	// which requires Administrator. Checking here turns what would otherwise
	// be an opaque pnputil failure -- after the queue has been read and the
	// printer probed -- into the actual reason, before any of that work.
	if includeDriver {
		elevated, elevErr := app.environment.IsElevated(ctx)
		if elevErr != nil {
			return commandError(stdout, stderr, "copy", fmt.Errorf("check administrator privileges: %w", elevErr), int(install.ExitGeneralError))
		}
		if !elevated {
			return commandError(stdout, stderr, "copy", errors.New("--include-driver exports the driver package with pnputil, which requires Administrator; re-run from an elevated prompt, or omit --include-driver to copy the mapping only"), int(install.ExitPreflight))
		}
	}

	cloned, err := install.CloneQueue(ctx, app.environment, queueName)
	if err != nil {
		return commandError(stdout, stderr, "copy", err, int(install.ExitGeneralError))
	}
	fmt.Fprintf(stderr, "Queue %q maps %s using driver %q.\n", cloned.PrinterName, cloned.HostAddress, cloned.DriverName)

	// The printer's own evidence is captured here, not copied from the queue,
	// so applying the bundle elsewhere still checks it is talking to the same
	// device rather than trusting the file.
	probeResult, err := collectIdentity(ctx, app, cloned.HostAddress, stderr)
	if err != nil {
		return commandError(stdout, stderr, "copy", err, int(install.ExitGeneralError))
	}
	profile := install.Profile{
		Version:     1,
		Target:      probeResult.Evidence.IP,
		PrinterName: cloned.PrinterName,
		DriverName:  cloned.DriverName,
		Evidence:    probeResult.Evidence,
	}
	if err := profile.Validate(); err != nil {
		return commandError(stdout, stderr, "copy", err, int(install.ExitGeneralError))
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
			return commandError(stdout, stderr, "copy", err, int(install.ExitGeneralError))
		}
		// Retained rather than deleted: an export that produced an unusable
		// bundle is worth being able to look at, and this matches how the
		// existing driver-staging path treats its own working directory.
		fmt.Fprintf(stderr, "Exporting driver %q to %s\n", cloned.DriverName, exportRoot)
		export, err := install.ExportDriver(ctx, app.environment, cloned.DriverName, exportRoot)
		if err != nil {
			return commandError(stdout, stderr, "copy", err, int(install.ExitGeneralError))
		}
		infRelative, err := install.FindExportedINF(exportRoot, export.OriginalName)
		if err != nil {
			return commandError(stdout, stderr, "copy", err, int(install.ExitGeneralError))
		}
		manifest.Driver = &bundle.DriverPayload{
			WindowsDriverName: cloned.DriverName,
			INF:               infRelative,
			ExportedFrom:      export.PublishedName,
		}
		payloadRoot = exportRoot
	}

	if err := bundle.Write(bundlePath, manifest, payloadRoot); err != nil {
		return commandError(stdout, stderr, "copy", err, int(install.ExitGeneralError))
	}

	opened, err := bundle.Open(bundlePath)
	if err != nil {
		return commandError(stdout, stderr, "copy", fmt.Errorf("wrote %s but could not read it back: %w", bundlePath, err), int(install.ExitGeneralError))
	}
	defer opened.Close()
	if err := opened.Verify(); err != nil {
		return commandError(stdout, stderr, "copy", err, int(install.ExitGeneralError))
	}
	fmt.Fprintf(stderr, "Wrote %s. Apply it on another machine with: spoolsmith apply %s --dry-run\n", bundlePath, filepath.Base(bundlePath))
	if manifest.Driver == nil {
		fmt.Fprintln(stderr, "No driver payload: the target machine must already have this driver registered. Re-run with --include-driver to carry it.")
	}
	return encodeSuccess(stdout, stderr, "copy", opened.Manifest)
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

// collectIdentity probes the printer for the identity evidence a bundle needs,
// retrying once when the first answer carries no identity at all.
//
// A printer's first contact after idling routinely answers thinner than the
// same printer seconds later -- this repo recorded exactly that against real
// hardware, where the first probe returned one open port and the next returned
// four. Failing the whole copy on that first thin answer would send an
// operator away from a machine that was about to work.
//
// The retry only repeats the probe. It never lowers the bar for what counts as
// identity, and a second thin answer still fails.
func collectIdentity(ctx context.Context, app application, address string, stderr io.Writer) (probe.Result, error) {
	result, err := app.collect(ctx, address)
	if err == nil && hasIdentity(result) {
		return result, nil
	}
	fmt.Fprintf(stderr, "First probe of %s returned no usable identity; waking it and retrying once.\n", address)
	select {
	case <-ctx.Done():
		return probe.Result{}, ctx.Err()
	case <-time.After(2 * time.Second):
	}
	retry, retryErr := app.collect(ctx, address)
	if retryErr != nil {
		return probe.Result{}, fmt.Errorf("capture evidence from %s: %w", address, retryErr)
	}
	if !hasIdentity(retry) {
		return probe.Result{}, fmt.Errorf("captured no HTTP, PJL, or SNMP identity from %s after two attempts; the printer may be asleep or unreachable from this PC. Wake it and retry, or map the queue directly with `profile capture`", address)
	}
	return retry, nil
}

// hasIdentity reports whether a probe found any of the three identity sources
// a profile requires.
func hasIdentity(result probe.Result) bool {
	return strings.TrimSpace(result.Evidence.HTTPTitle) != "" ||
		strings.TrimSpace(result.Evidence.PJLID) != "" ||
		strings.TrimSpace(result.Evidence.SNMPSysDescr) != ""
}
