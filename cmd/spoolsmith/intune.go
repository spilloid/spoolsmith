package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spilloid/spoolsmith/internal/intune"
)

func runIntune(ctx context.Context, args []string, input io.Reader, stdout, stderr io.Writer, app application) int {
	if len(args) == 0 {
		return usageError(stdout, stderr, "intune", errors.New("use intune wizard or intune build --help"))
	}
	var opts intune.Options
	var output, prepTool, prepOutput string
	var dryRun bool
	if args[0] == "wizard" {
		if len(args) != 1 || !app.inputTerminal {
			return usageError(stdout, stderr, "intune wizard", errors.New("wizard requires an interactive terminal; use intune build for automation"))
		}
		reader := bufio.NewReader(input)
		var promptErr error
		ask := func(label string) string {
			if promptErr != nil {
				return ""
			}
			fmt.Fprint(stderr, label+": ")
			value, err := reader.ReadString('\n')
			if err != nil {
				promptErr = err
			}
			return strings.TrimSpace(value)
		}
		askDefault := func(label, fallback string) string {
			value := ask(label + " [" + fallback + "]")
			if value == "" {
				return fallback
			}
			return value
		}
		fmt.Fprintln(stderr, "Step 1 of 2: Select a validated profile or SpoolSmith bundle, and approved Windows x64 CLI. Hashing is automatic.")
		profilePath := ask("Profile JSON or .ssb bundle path")
		var err error
		opts, err = intune.ProfileDefaults(profilePath)
		if err != nil {
			return commandError(stdout, stderr, "intune wizard", err, 2)
		}
		opts.BinaryPath = ask("SpoolSmith Windows CLI binary path")
		hasPayload, err := intune.HasLocalPayload(profilePath)
		if err != nil {
			return commandError(stdout, stderr, "intune wizard", err, 2)
		}
		if !hasPayload {
			opts.DriverPrerequisite = ask("This profile has no local driver payload. Driver will be registered separately before installation? Type yes to accept") == "yes"
		}
		fmt.Fprintf(stderr, "App: %s\nDeployment ID: %s\nRevision: 1; live identity validation; no queue adoption.\n", opts.DisplayName, opts.ID)
		fmt.Fprintln(stderr, "For updates, keep the existing ID and queue name and increase the revision. Reuse any previously chosen custom ID.")
		advanced := askDefault("Edit advanced settings (updates, metadata, policy, destination)? yes/no", "no")
		if advanced != "yes" && advanced != "no" {
			return usageError(stdout, stderr, "intune wizard", errors.New("select yes or no for advanced settings"))
		}
		if advanced == "yes" {
			opts.ID = askDefault("Stable deployment ID", opts.ID)
			opts.Revision, _ = strconv.Atoi(askDefault("Positive deployment revision", "1"))
			opts.DisplayName = askDefault("Company Portal display name", opts.DisplayName)
			opts.Location = ask("Location (optional)")
			opts.Description = askDefault("Description", opts.Description)
			opts.BinarySHA256 = ask("Approved CLI SHA-256 (Enter to calculate automatically)")
			mode := askDefault("Validation: strict or offline (offline skips live identity checks; printing needs connectivity)", "strict")
			if mode != "strict" && mode != "offline" {
				return usageError(stdout, stderr, "intune wizard", errors.New("select strict or offline validation"))
			}
			opts.Offline = mode == "offline"
			opts.Adopt = ask("Adopt an existing exactly matching unmanaged queue? Type yes to allow") == "yes"
		}
		output, err = intune.SuggestOutput(filepath.Dir(opts.ProfilePath), opts.ID, opts.Revision)
		if err != nil {
			return commandError(stdout, stderr, "intune wizard", err, 2)
		}
		if advanced == "yes" {
			output = askDefault("Export folder (must not already exist)", output)
		}
		if promptErr != nil {
			return commandError(stdout, stderr, "intune wizard", promptErr, 2)
		}
		prepared, err := intune.Prepare(opts)
		if err != nil {
			return commandError(stdout, stderr, "intune wizard", err, 2)
		}
		fmt.Fprintln(stderr, "Step 2 of 2: Review package contents, commands, hashes and policy.")
		fmt.Fprintln(stderr, "Export folder:", output)
		enc := json.NewEncoder(stderr)
		enc.SetIndent("", "  ")
		if err = enc.Encode(prepared.Manifest); err != nil {
			return 1
		}
		if ask("Type export to create this local package") != "export" || promptErr != nil {
			fmt.Fprintln(stderr, "Export cancelled.")
			return 5
		}
		if err = prepared.Export(output); err != nil {
			return commandError(stdout, stderr, "intune wizard", err, 1)
		}
		fmt.Fprintln(stderr, "Exported. Follow README.txt to prepare and upload the Win32 app.")
		return encodeSuccess(stdout, stderr, "intune wizard", prepared.Manifest)
	}
	if args[0] != "build" {
		return usageError(stdout, stderr, "intune", errors.New("use wizard or build"))
	}
	flags := flag.NewFlagSet("intune build", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&opts.ProfilePath, "profile", "", "administrator-prevalidated profile (.json) or bundle (.ssb)")
	flags.StringVar(&opts.BinaryPath, "binary", "", "Windows x64 CLI binary containing offline/status commands")
	flags.StringVar(&opts.BinarySHA256, "binary-sha256", "", "optional approved binary SHA-256 (default: calculate from selected CLI)")
	flags.StringVar(&opts.ID, "id", "", "stable deployment ID (default: derived from profile queue name)")
	flags.IntVar(&opts.Revision, "revision", 1, "positive deployment revision; increase for updates")
	flags.StringVar(&opts.DisplayName, "name", "", "Company Portal display name (default: profile queue name)")
	flags.StringVar(&opts.Location, "location", "", "location")
	flags.StringVar(&opts.Description, "description", "", "description (default: printer name and address)")
	flags.BoolVar(&opts.Offline, "offline", false, "explicitly skip live identity validation at install")
	flags.BoolVar(&opts.DriverPrerequisite, "driver-prerequisite", false, "accept separately managed registered driver requirement")
	flags.BoolVar(&opts.Adopt, "adopt", false, "allow adoption of an exactly matching unmanaged queue")
	flags.BoolVar(&dryRun, "dry-run", false, "preview manifest without creating files or invoking content prep")
	flags.StringVar(&output, "output", "", "new bundle directory (default: unused <id>-r<revision> folder beside profile)")
	flags.StringVar(&prepTool, "content-prep-tool", "", "optional path to Microsoft IntuneWinAppUtil.exe")
	flags.StringVar(&prepOutput, "content-prep-output", "", "separate .intunewin output directory")
	if err := flags.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 || (prepTool == "") != (prepOutput == "") {
		return usageError(stdout, stderr, "intune build", errors.New("provide no positional arguments, and both content-prep options if used"))
	}
	defaults, err := intune.ProfileDefaults(opts.ProfilePath)
	if err != nil {
		return commandError(stdout, stderr, "intune build", err, 2)
	}
	provided := make(map[string]bool)
	flags.Visit(func(f *flag.Flag) { provided[f.Name] = true })
	if !provided["id"] {
		opts.ID = defaults.ID
	}
	if !provided["name"] {
		opts.DisplayName = defaults.DisplayName
	}
	if !provided["description"] {
		opts.Description = defaults.Description
	}
	if !provided["output"] {
		output, err = intune.SuggestOutput(filepath.Dir(opts.ProfilePath), opts.ID, opts.Revision)
		if err != nil {
			return commandError(stdout, stderr, "intune build", err, 2)
		}
	} else if output == "" {
		return usageError(stdout, stderr, "intune build", errors.New("--output must name a new bundle directory"))
	}

	prepared, err := intune.Prepare(opts)
	if err != nil {
		return commandError(stdout, stderr, "intune build", err, 2)
	}
	fmt.Fprintln(stderr, "Export folder:", output)
	if !dryRun {
		if err = prepared.Export(output); err != nil {
			return commandError(stdout, stderr, "intune build", err, 1)
		}
		if prepTool != "" {
			prepCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
			defer cancel()
			result, e := intune.PrepareContent(prepCtx, prepTool, output, prepOutput)
			fmt.Fprint(stderr, result)
			if e != nil {
				return commandError(stdout, stderr, "intune build", fmt.Errorf("bundle exported; content prep failed: %w", e), 1)
			}
		} else {
			fmt.Fprintln(stderr, "Bundle exported. Microsoft Win32 Content Prep Tool is still required; see README.txt for the command.")
		}
	}
	return encodeSuccess(stdout, stderr, "intune build", prepared.Manifest)
}
