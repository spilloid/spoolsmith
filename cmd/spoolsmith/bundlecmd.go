package main

import (
	"bufio"
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
)

// loadProfileFile opens a printer file for install/add/configure's --profile
// flag. There is exactly one on-disk format now (a bundle, extension .ssb),
// so this shares apply's own driver-payload handling instead of being a
// separate, weaker path: a profile loaded this way stages an embedded driver
// payload exactly as `apply` does, rather than only supporting the
// external driver_package reference a bare profile JSON used to be limited to.
func loadProfileFile(path string) (install.Profile, *install.BundleDriver, error) {
	opened, err := bundle.Open(path)
	if err != nil {
		return install.Profile{}, nil, err
	}
	defer opened.Close()
	profile := opened.Manifest.Profile
	if err := profile.ResolvePackagePath(path); err != nil {
		return install.Profile{}, nil, err
	}
	driver, _, err := opened.PrepareDriver()
	if err != nil {
		return install.Profile{}, nil, err
	}
	return profile, driver, nil
}

// runClone backs `spoolsmith copy`: it reads one already-working queue off
// this machine and writes a printer file (.ssb) another machine can apply, or
// with --all, every copyable queue into one printer set (.zip).
//
// The point of this command is that the operator stops being the integration
// point. `profile capture` asks for the exact registered driver name, which
// means reading it off Get-PrinterDriver and retyping it correctly. Here the
// queue that already prints is the source of truth for its own settings --
// and, whenever it can be copied, its driver.
func runClone(ctx context.Context, args []string, input io.Reader, stdout, stderr io.Writer, app application) int {
	var queueName, outPath, note string
	settingsOnly := false
	includeAll := false
	var positional []string
	for index := 0; index < len(args); index++ {
		switch args[index] {
		case "--include-driver":
			// Kept so existing scripts keep working; the driver is now
			// included by default whenever it can be.
			fmt.Fprintln(stderr, "Note: --include-driver is no longer needed; the driver is included whenever it can be. Use --settings-only to leave it out.")
		case "--settings-only":
			settingsOnly = true
		case "--all":
			includeAll = true
		case "--out":
			if index+1 >= len(args) || strings.TrimSpace(args[index+1]) == "" {
				return usageError(stdout, stderr, "copy", errors.New("--out requires a file name"))
			}
			if outPath != "" {
				return usageError(stdout, stderr, "copy", errors.New("--out given more than once"))
			}
			outPath = args[index+1]
			index++
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
			positional = append(positional, args[index])
		}
	}
	if includeAll {
		// copy --all [<printers.zip>] | copy --all --out <printers.zip>
		if len(positional) > 1 || (len(positional) == 1 && outPath != "") {
			return usageError(stdout, stderr, "copy", errors.New("copy --all takes one .zip file name"))
		}
		if len(positional) == 1 {
			outPath = positional[0]
		}
		if outPath == "" {
			outPath = bundle.DefaultSetName(time.Now())
			fmt.Fprintf(stderr, "Writing to %s\n", outPath)
		}
		return runCloneAll(ctx, outPath, settingsOnly, note, stdout, stderr, app)
	}
	// copy [<queue>] [<file.ssb>] | copy [<queue>] --out <file.ssb>
	if len(positional) > 2 || (len(positional) == 2 && outPath != "") {
		return usageError(stdout, stderr, "copy", errors.New("copy takes one printer name and one .ssb file name"))
	}
	if len(positional) > 0 {
		queueName = positional[0]
	}
	if len(positional) == 2 {
		outPath = positional[1]
	}
	if queueName == "" {
		selected, selectErr := selectInstalledQueue(ctx, input, stderr, app)
		if selectErr != nil {
			return commandError(stdout, stderr, "copy", selectErr, int(install.ExitUsageError))
		}
		queueName = selected
	}
	if outPath == "" {
		outPath = defaultBundleName(queueName)
		fmt.Fprintf(stderr, "Writing to %s\n", outPath)
	}

	created, err := bundle.Create(ctx, app.environment, bundle.Collector(app.collect), bundle.CreateOptions{
		QueueName:    queueName,
		Path:         outPath,
		Note:         note,
		SettingsOnly: settingsOnly,
		CreatedBy:    "spoolsmith " + versionString(),
		SourceHost:   hostName(),
		Progress:     func(step string) { fmt.Fprintln(stderr, step) },
	})
	if err != nil {
		return commandError(stdout, stderr, "copy", err, int(install.ExitGeneralError))
	}
	manifest := created.Manifest

	fmt.Fprintf(stderr, "Wrote %s. Apply it on another machine with: spoolsmith apply %s --dry-run\n", outPath, filepath.Base(outPath))
	if manifest.Driver != nil {
		fmt.Fprintf(stderr, "Driver included: %q (%d files, %d bytes).\n", manifest.Driver.WindowsDriverName, len(manifest.Driver.Files), manifest.TotalPayloadBytes())
	} else if created.DriverNotIncluded != "" {
		fmt.Fprintf(stderr, "Driver not included: %s.\n", created.DriverNotIncluded)
	}
	if manifest.Profile.Evidence.Provenance != "captured" {
		fmt.Fprintln(stderr, bundle.UnconfirmedIdentityNotice)
	}
	return encodeSuccess(stdout, stderr, "copy", manifest)
}

// Keep the command's result names for callers and tests while sharing the
// result schema and execution with the desktop app.
type copyAllResult = bundle.AllResult
type copyAllQueue = bundle.QueueResult

func runCloneAll(ctx context.Context, setPath string, settingsOnly bool, note string, stdout, stderr io.Writer, app application) int {
	result, err := bundle.CreateAll(ctx, app.environment, bundle.Collector(app.collect), bundle.AllOptions{
		SetPath: setPath, SettingsOnly: settingsOnly, Note: note,
		CreatedBy: "spoolsmith " + versionString(), SourceHost: hostName(),
		Progress: func(queueName, step string) { fmt.Fprintf(stderr, "  %s: %s\n", queueName, step) },
	})
	if err != nil && len(result.Queues) == 0 {
		return commandError(stdout, stderr, "copy", err, int(install.ExitGeneralError))
	}
	for _, queue := range result.Queues {
		switch queue.Status {
		case "written":
			carried := "settings only"
			if queue.DriverIncluded {
				carried = "driver included"
			}
			fmt.Fprintf(stderr, "Added %s as %s (%s)\n", queue.Name, queue.Member, carried)
			if queue.Reason != "" {
				fmt.Fprintf(stderr, "  ! %s\n", queue.Reason)
			}
		case "skipped":
			fmt.Fprintf(stderr, "! %s: skipped -- %s\n", queue.Name, queue.Reason)
		case "error":
			fmt.Fprintf(stderr, "x %s: %s\n", queue.Name, queue.Reason)
		}
	}

	code := install.ExitSuccess
	switch {
	case err != nil:
		fmt.Fprintf(stderr, "spoolsmith copy --all: %v\n", err)
		code = install.ExitGeneralError
	case result.Written == 0:
		fmt.Fprintln(stderr, "spoolsmith copy --all: no printers were copied, so no file was written")
		code = install.ExitGeneralError
	default:
		fmt.Fprintf(stderr, "Saved %d of %d printers to %s (%d skipped, %d failed).\n", result.Written, result.Requested, result.SetPath, result.Skipped, result.Failed)
		fmt.Fprintf(stderr, "Take %s to the other PC and preview it with: spoolsmith apply %s --dry-run\n", filepath.Base(result.SetPath), filepath.Base(result.SetPath))
	}
	if err := encodeJSON(stdout, result); err != nil {
		fmt.Fprintf(stderr, "spoolsmith copy: encode result: %v\n", err)
		return int(install.ExitGeneralError)
	}
	return int(code)
}

// runApply maps a printer file (.ssb) -- or each printer in a printer set
// (.zip) -- onto this machine.
func runApply(ctx context.Context, args []string, input io.Reader, stdout, stderr io.Writer, app application) int {
	var path, planHash, only string
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
		case "--member":
			if index+1 >= len(args) || strings.TrimSpace(args[index+1]) == "" {
				return usageError(stdout, stderr, "apply", errors.New("--member requires a printer file name from the set"))
			}
			only = args[index+1]
			index++
		default:
			if strings.HasPrefix(args[index], "-") {
				return usageError(stdout, stderr, "apply", fmt.Errorf("unknown option %q", args[index]))
			}
			if path != "" {
				return usageError(stdout, stderr, "apply", errors.New("apply takes exactly one printer file (.ssb) or printer set (.zip)"))
			}
			path = args[index]
		}
	}
	if path == "" {
		return usageError(stdout, stderr, "apply", errors.New("apply requires a printer file (.ssb) or printer set (.zip)"))
	}
	options.ConfirmPlanHash = planHash
	options.Compact = app.outputTerminal && !options.JSON

	isSet, err := bundle.IsSet(path)
	if err != nil {
		return commandError(stdout, stderr, "apply", err, int(install.ExitUsageError))
	}
	if isSet {
		return runApplySet(ctx, path, only, options, input, stdout, stderr, app)
	}
	if only != "" {
		return usageError(stdout, stderr, "apply", errors.New("--member applies only to a printer set (.zip)"))
	}

	outcome, code, setupErr := applyBundleFile(ctx, path, options, input, stderr, app)
	if setupErr != nil {
		return commandError(stdout, stderr, "apply", setupErr, int(code))
	}
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

// applyBundleFile runs the single-printer apply flow -- its own plan and its
// own one confirmation -- for one printer file. A non-nil error means the file
// itself could not be prepared, before any plan was shown.
func applyBundleFile(ctx context.Context, path string, options install.InstallOptions, input io.Reader, stderr io.Writer, app application) (install.Outcome, install.ExitCode, error) {
	opened, err := bundle.Open(path)
	if err != nil {
		return install.Outcome{}, install.ExitUsageError, err
	}
	defer opened.Close()

	profile := opened.Manifest.Profile
	options.Profile = &profile

	driver, stageDir, err := opened.PrepareDriver()
	if err != nil {
		return install.Outcome{}, install.ExitGeneralError, err
	}
	if driver != nil {
		options.BundleDriver = driver
		fmt.Fprintf(stderr, "Verified %d driver files from the printer file into %s\n", driver.FileCount, stageDir)
	}

	outcome, code := app.workflow.RunInstall(ctx, app.environment, input, stderr, app.inputTerminal, options)
	outcome.Operation = "apply"
	return outcome, code, nil
}

// applySetMember is one member's result in `apply <set>`'s JSON output.
type applySetMember struct {
	Member  string           `json:"member"`
	Status  string           `json:"status"`
	Error   string           `json:"error,omitempty"`
	Outcome *install.Outcome `json:"outcome,omitempty"`
}

type applySetResult struct {
	Operation string           `json:"operation"`
	Set       string           `json:"set"`
	Members   []applySetMember `json:"members"`
}

// runApplySet applies each printer in a set as its own, independent apply:
// every member gets its own plan and its own single confirmation, exactly as
// if its .ssb had been applied alone. A set never widens one confirmation to
// cover several printers.
func runApplySet(ctx context.Context, path, only string, options install.InstallOptions, input io.Reader, stdout, stderr io.Writer, app application) int {
	set, err := bundle.OpenSet(path)
	if err != nil {
		return commandError(stdout, stderr, "apply", err, int(install.ExitUsageError))
	}
	defer set.Close()

	members := set.Members
	if only != "" {
		found := ""
		for _, name := range set.Members {
			if strings.EqualFold(name, only) || strings.EqualFold(strings.TrimSuffix(name, filepath.Ext(name)), only) {
				found = name
				break
			}
		}
		if found == "" {
			return usageError(stdout, stderr, "apply", fmt.Errorf("%s has no printer file named %q; it holds: %s", filepath.Base(path), only, strings.Join(set.Members, ", ")))
		}
		members = []string{found}
	}
	if options.ConfirmPlanHash != "" && len(members) > 1 {
		return usageError(stdout, stderr, "apply", errors.New("--plan-hash names one reviewed plan; with a printer set, also choose that printer with --member <name>"))
	}

	fmt.Fprintf(stderr, "Printer set %s holds %d printer files:\n", filepath.Base(path), len(set.Members))
	if set.Note != "" {
		fmt.Fprintf(stderr, "  Note: %s\n", set.Note)
	}
	for _, name := range set.Members {
		fmt.Fprintf(stderr, "  - %s\n", name)
	}
	if len(members) > 1 {
		fmt.Fprintln(stderr, "Each printer is reviewed and confirmed on its own.")
	}

	work, err := os.MkdirTemp("", "spoolsmith-apply-set-")
	if err != nil {
		return commandError(stdout, stderr, "apply", err, int(install.ExitGeneralError))
	}
	defer os.RemoveAll(work)

	// One shared reader, so each member's confirmation reads its own answer
	// rather than a buffer that swallowed the next member's.
	reader := bufio.NewReader(input)
	result := applySetResult{Operation: "apply", Set: path, Members: make([]applySetMember, 0, len(members))}
	exit := install.ExitSuccess
	fail := func(code install.ExitCode) {
		if exit == install.ExitSuccess {
			exit = code
		}
	}
	for index, name := range members {
		if err := ctx.Err(); err != nil {
			result.Members = append(result.Members, applySetMember{Member: name, Status: "error", Error: "not started: " + err.Error()})
			fail(install.ExitGeneralError)
			continue
		}
		fmt.Fprintf(stderr, "\n[%d/%d] %s\n", index+1, len(members), name)
		extracted, err := set.Extract(name, work)
		if err != nil {
			fmt.Fprintf(stderr, "spoolsmith apply: %v\n", err)
			result.Members = append(result.Members, applySetMember{Member: name, Status: "error", Error: err.Error()})
			fail(install.ExitGeneralError)
			continue
		}
		outcome, code, setupErr := applyBundleFile(ctx, extracted, options, reader, stderr, app)
		if setupErr != nil {
			fmt.Fprintf(stderr, "spoolsmith apply: %s: %v\n", name, setupErr)
			result.Members = append(result.Members, applySetMember{Member: name, Status: "error", Error: setupErr.Error()})
			fail(code)
			continue
		}
		if outcome.Error != "" {
			fmt.Fprintf(stderr, "spoolsmith apply: %s: %s\n", name, outcome.Error)
		}
		if outcome.PlanHash != "" && options.DryRun {
			fmt.Fprintf(stderr, "Reviewed plan fingerprint for %s: %s\n", name, outcome.PlanHash)
		}
		outcomeCopy := outcome
		result.Members = append(result.Members, applySetMember{Member: name, Status: outcome.Status, Error: outcome.Error, Outcome: &outcomeCopy})
		if code != install.ExitSuccess {
			fail(code)
		}
	}

	counts := map[string]int{}
	for _, m := range result.Members {
		counts[m.Status]++
	}
	fmt.Fprintf(stderr, "\nPrinter set summary (%d of %d printers):\n", len(result.Members), len(set.Members))
	for _, m := range result.Members {
		mark := "ok"
		if m.Status != "success" && m.Status != "dry-run" {
			mark = "x "
		}
		line := fmt.Sprintf("  %s %s: %s", mark, m.Member, m.Status)
		if m.Error != "" {
			line += " -- " + m.Error
		}
		fmt.Fprintln(stderr, line)
	}
	if options.DryRun {
		fmt.Fprintf(stderr, "%d previewed, %d failed. No changes made.\n", counts["dry-run"], len(result.Members)-counts["dry-run"])
	} else {
		fmt.Fprintf(stderr, "%d applied, %d not confirmed, %d failed.\n", counts["success"], counts["not-confirmed"], len(result.Members)-counts["success"]-counts["not-confirmed"])
	}

	if app.outputTerminal && !options.JSON {
		return int(exit)
	}
	if err := encodeJSON(stdout, result); err != nil {
		fmt.Fprintf(stderr, "spoolsmith apply: encode result: %v\n", err)
		return int(install.ExitGeneralError)
	}
	return int(exit)
}

// runBundle reads a printer file or printer set without touching the network
// or this machine.
func runBundle(args []string, stdout, stderr io.Writer) int {
	if len(args) != 2 || args[0] != "inspect" {
		return usageError(stdout, stderr, "bundle", errors.New("bundle requires inspect <file.ssb|set.zip>"))
	}
	isSet, err := bundle.IsSet(args[1])
	if err != nil {
		return commandError(stdout, stderr, "bundle inspect", err, int(install.ExitUsageError))
	}
	if isSet {
		return inspectSet(args[1], stdout, stderr)
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
	fmt.Fprintf(stderr, "Printer file: %s\n  Created: %s by %s\n  Source machine: %s\n", args[1], m.Created, shown(m.CreatedBy), shown(m.SourceHost))
	if m.Note != "" {
		fmt.Fprintf(stderr, "  Note: %s\n", m.Note)
	}
	fmt.Fprintf(stderr, "  Queue: %s\n  Target: %s (RAW TCP 9100)\n  Driver: %s\n", m.Profile.PrinterName, m.Profile.Target, m.Profile.DriverName)
	if m.Profile.Evidence.Provenance == "captured" {
		fmt.Fprintln(stderr, "  Identity: confirmed against the printer when copied.")
	} else {
		fmt.Fprintf(stderr, "  Identity: unconfirmed — %s. Applying it runs offline.\n", shown(m.Profile.Evidence.ProvenanceNote))
	}
	if m.Driver == nil {
		fmt.Fprintln(stderr, "  Driver payload: none — the target machine must already have this driver registered.")
	} else {
		fmt.Fprintf(stderr, "  Driver payload: %d files, %d bytes, INF %s (from %s)\n", len(m.Driver.Files), m.TotalPayloadBytes(), m.Driver.INF, shown(m.Driver.ExportedFrom))
		fmt.Fprintln(stderr, "  All payload files match the manifest's hashes.")
	}
	fmt.Fprintln(stderr, "  This checks the file's integrity, not the printer. Preview against a real target with: spoolsmith apply <file> --dry-run")
	return encodeSuccess(stdout, stderr, "bundle inspect", m)
}

// setInspectMember describes one member of a printer set.
type setInspectMember struct {
	Member            string `json:"member"`
	PrinterName       string `json:"printer_name,omitempty"`
	Target            string `json:"target,omitempty"`
	DriverName        string `json:"driver_name,omitempty"`
	DriverEmbedded    bool   `json:"driver_embedded"`
	DriverBytes       int64  `json:"driver_bytes,omitempty"`
	IdentityConfirmed bool   `json:"identity_confirmed"`
	Error             string `json:"error,omitempty"`
}

type setInspectResult struct {
	Set     string             `json:"set"`
	Note    string             `json:"note,omitempty"`
	Members []setInspectMember `json:"members"`
}

// inspectSet lists a printer set's members, each verified as its own printer
// file. Members are extracted to a private working folder that is removed
// afterwards; nothing is applied.
func inspectSet(path string, stdout, stderr io.Writer) int {
	set, err := bundle.OpenSet(path)
	if err != nil {
		return commandError(stdout, stderr, "bundle inspect", err, int(install.ExitUsageError))
	}
	defer set.Close()
	work, err := os.MkdirTemp("", "spoolsmith-inspect-set-")
	if err != nil {
		return commandError(stdout, stderr, "bundle inspect", err, int(install.ExitGeneralError))
	}
	defer os.RemoveAll(work)

	result := setInspectResult{Set: path, Note: set.Note, Members: make([]setInspectMember, 0, len(set.Members))}
	fmt.Fprintf(stderr, "Printer set: %s (%d printer files)\n", path, len(set.Members))
	if set.Note != "" {
		fmt.Fprintf(stderr, "  Note: %s\n", set.Note)
	}
	invalid := 0
	for _, name := range set.Members {
		entry := setInspectMember{Member: name}
		extracted, err := set.Extract(name, work)
		if err == nil {
			var opened *bundle.Bundle
			opened, err = bundle.Open(extracted)
			if err == nil {
				m := opened.Manifest
				entry.PrinterName, entry.Target, entry.DriverName = m.Profile.PrinterName, m.Profile.Target, m.Profile.DriverName
				entry.DriverEmbedded = m.Driver != nil
				entry.DriverBytes = m.TotalPayloadBytes()
				entry.IdentityConfirmed = m.Profile.Evidence.Provenance == "captured"
				opened.Close()
			}
			os.Remove(extracted)
		}
		if err != nil {
			invalid++
			entry.Error = err.Error()
			fmt.Fprintf(stderr, "  x %s: %v\n", name, err)
		} else {
			driver := "not included (the target machine must already have it)"
			if entry.DriverEmbedded {
				driver = fmt.Sprintf("included, %d bytes", entry.DriverBytes)
			}
			identity := ""
			if !entry.IdentityConfirmed {
				identity = " [identity unconfirmed; applies offline]"
			}
			fmt.Fprintf(stderr, "  - %s: %s at %s\n      Driver: %s -- %s%s\n", name, entry.PrinterName, entry.Target, entry.DriverName, driver, identity)
		}
		result.Members = append(result.Members, entry)
	}
	fmt.Fprintln(stderr, "  This checks each file's integrity, not the printers. Preview against a real target with: spoolsmith apply <set.zip> --dry-run")
	if invalid > 0 {
		if err := encodeJSON(stdout, result); err != nil {
			fmt.Fprintf(stderr, "spoolsmith bundle inspect: encode result: %v\n", err)
		}
		return int(install.ExitGeneralError)
	}
	return encodeSuccess(stdout, stderr, "bundle inspect", result)
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

// defaultBundleName preserves the CLI helper while both front ends use the
// shared bundle naming rule.
func defaultBundleName(queueName string) string {
	return bundle.FileName(queueName)
}
