package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
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
		fmt.Fprintln(stderr, "Step 1 of 3: Select the validated profile and approved Windows x64 payloads.")
		opts.ProfilePath = ask("Profile JSON path")
		opts.BinaryPath = ask("SpoolSmith Windows CLI binary path")
		opts.BinarySHA256 = ask("Reviewed CLI SHA-256")
		choice := ask("Driver source: profile-payload or registered-prerequisite")
		if choice != "profile-payload" && choice != "registered-prerequisite" {
			return usageError(stdout, stderr, "intune wizard", errors.New("select a driver source"))
		}
		opts.DriverPrerequisite = choice == "registered-prerequisite"
		fmt.Fprintln(stderr, "Step 2 of 3: Deployment identity and policy. Offline skips live identity checks; printing needs connectivity.")
		opts.ID = ask("Stable deployment ID (lowercase letters/digits/hyphens)")
		revision := ask("Positive deployment revision")
		opts.Revision, _ = strconv.Atoi(revision)
		opts.DisplayName = ask("Company Portal display name")
		opts.Location = ask("Location")
		opts.Description = ask("Description")
		mode := ask("Validation: strict or offline")
		if mode != "strict" && mode != "offline" {
			return usageError(stdout, stderr, "intune wizard", errors.New("select strict or offline validation"))
		}
		opts.Offline = mode == "offline"
		opts.Adopt = ask("Adopt an existing exactly matching unmanaged queue? Type yes to allow") == "yes"
		output = ask("New export directory")
		if promptErr != nil {
			return commandError(stdout, stderr, "intune wizard", promptErr, 2)
		}
		prepared, err := intune.Prepare(opts)
		if err != nil {
			return commandError(stdout, stderr, "intune wizard", err, 2)
		}
		fmt.Fprintln(stderr, "Step 3 of 3: Review package contents, commands and policy.")
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
	flags.StringVar(&opts.ProfilePath, "profile", "", "administrator-prevalidated profile")
	flags.StringVar(&opts.BinaryPath, "binary", "", "Windows x64 CLI binary containing offline/status commands")
	flags.StringVar(&opts.BinarySHA256, "binary-sha256", "", "reviewed binary SHA-256")
	flags.StringVar(&opts.ID, "id", "", "stable deployment ID")
	flags.IntVar(&opts.Revision, "revision", 0, "positive deployment revision")
	flags.StringVar(&opts.DisplayName, "name", "", "Company Portal display name")
	flags.StringVar(&opts.Location, "location", "", "location")
	flags.StringVar(&opts.Description, "description", "", "description")
	flags.BoolVar(&opts.Offline, "offline", false, "explicitly skip live identity validation at install")
	flags.BoolVar(&opts.DriverPrerequisite, "driver-prerequisite", false, "accept separately managed registered driver requirement")
	flags.BoolVar(&opts.Adopt, "adopt", false, "allow adoption of an exactly matching unmanaged queue")
	flags.BoolVar(&dryRun, "dry-run", false, "preview manifest without creating files or invoking content prep")
	flags.StringVar(&output, "output", "", "new bundle directory")
	flags.StringVar(&prepTool, "content-prep-tool", "", "optional path to Microsoft IntuneWinAppUtil.exe")
	flags.StringVar(&prepOutput, "content-prep-output", "", "separate .intunewin output directory")
	if err := flags.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 || output == "" || (prepTool == "") != (prepOutput == "") {
		return usageError(stdout, stderr, "intune build", errors.New("provide --output, no positional arguments, and both content-prep options if used"))
	}
	prepared, err := intune.Prepare(opts)
	if err != nil {
		return commandError(stdout, stderr, "intune build", err, 2)
	}
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
