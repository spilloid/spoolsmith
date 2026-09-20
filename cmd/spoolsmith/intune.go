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
	var dryRun, noPrep bool
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
		prepTool = intune.FindContentPrepTool(app.prepToolDirs...)
		if advanced == "yes" {
			shown := prepTool
			if shown == "" {
				shown = "none"
			}
			chosen := askDefault("Microsoft Content Prep tool to also create the .intunewin (path, or none)", shown)
			if strings.EqualFold(chosen, "none") {
				prepTool = ""
			} else {
				prepTool = chosen
			}
		}
		if prepTool != "" {
			prepOutput = intune.SuggestPrepOutput(output)
			if advanced == "yes" {
				prepOutput = askDefault(".intunewin output folder", prepOutput)
			}
			if promptErr == nil {
				if _, _, err = intune.CheckContentPrep(prepTool, output, prepOutput); err != nil {
					return commandError(stdout, stderr, "intune wizard", err, 2)
				}
			}
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
		if prepTool != "" {
			fmt.Fprintf(stderr, "Then create %s with Microsoft's tool %s in %s\n", intune.PreparedPackageName, prepTool, prepOutput)
		} else {
			fmt.Fprintf(stderr, "Tip: put %s beside spoolsmith.exe and this wizard also creates the .intunewin file for you.\n", intune.ContentPrepToolName)
		}
		if ask("Type export to create this local package") != "export" || promptErr != nil {
			fmt.Fprintln(stderr, "Export cancelled.")
			return 5
		}
		if err = prepared.Export(output); err != nil {
			return commandError(stdout, stderr, "intune wizard", err, 1)
		}
		if prepTool != "" {
			if e := runContentPrep(ctx, stderr, prepTool, output, prepOutput); e != nil {
				return commandError(stdout, stderr, "intune wizard", e, 1)
			}
		} else {
			fmt.Fprintln(stderr, "Exported. Follow README.txt to prepare and upload the Win32 app.")
		}
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
	flags.StringVar(&prepTool, "content-prep-tool", "", "path to Microsoft IntuneWinAppUtil.exe (default: the copy beside spoolsmith.exe, if present)")
	flags.StringVar(&prepOutput, "content-prep-output", "", "separate .intunewin output directory (default: <export folder>-intunewin)")
	flags.BoolVar(&noPrep, "no-content-prep", false, "export the bundle only; do not run Microsoft's tool even if it is beside spoolsmith.exe")
	if err := flags.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 {
		return usageError(stdout, stderr, "intune build", errors.New("provide no positional arguments"))
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

	// Content prep is on whenever the tool is named or sits beside this program;
	// a bad tool or output is refused here, before anything is exported.
	toolGiven, outputGiven := provided["content-prep-tool"], provided["content-prep-output"]
	switch {
	case toolGiven && prepTool == "", outputGiven && prepOutput == "":
		return usageError(stdout, stderr, "intune build", errors.New("--content-prep-tool and --content-prep-output must name a path"))
	case noPrep && (toolGiven || outputGiven):
		return usageError(stdout, stderr, "intune build", errors.New("--no-content-prep cannot be combined with the other content-prep options"))
	case noPrep:
		prepTool, prepOutput = "", ""
	default:
		autoTool := false
		if prepTool == "" {
			prepTool = intune.FindContentPrepTool(app.prepToolDirs...)
			autoTool = prepTool != ""
		}
		if prepTool == "" && outputGiven {
			return usageError(stdout, stderr, "intune build", fmt.Errorf("--content-prep-output needs %s: pass --content-prep-tool or put it beside spoolsmith.exe", intune.ContentPrepToolName))
		}
		if prepTool != "" {
			if prepOutput == "" {
				prepOutput = intune.SuggestPrepOutput(output)
			}
			if _, _, err = intune.CheckContentPrep(prepTool, output, prepOutput); err != nil {
				return commandError(stdout, stderr, "intune build", err, 2)
			}
			if autoTool {
				fmt.Fprintf(stderr, "Using %s (--no-content-prep to skip)\n", prepTool)
			}
		}
	}

	prepared, err := intune.Prepare(opts)
	if err != nil {
		return commandError(stdout, stderr, "intune build", err, 2)
	}
	fmt.Fprintln(stderr, "Export folder:", output)
	if prepTool != "" {
		fmt.Fprintf(stderr, "%s folder: %s\n", intune.PreparedPackageName, prepOutput)
	}
	if !dryRun {
		if err = prepared.Export(output); err != nil {
			return commandError(stdout, stderr, "intune build", err, 1)
		}
		if prepTool != "" {
			if e := runContentPrep(ctx, stderr, prepTool, output, prepOutput); e != nil {
				return commandError(stdout, stderr, "intune build", e, 1)
			}
		} else {
			fmt.Fprintf(stderr, "Bundle exported. To create the .intunewin, put %s beside spoolsmith.exe or pass --content-prep-tool; README.txt has the manual command.\n", intune.ContentPrepToolName)
		}
	}
	return encodeSuccess(stdout, stderr, "intune build", prepared.Manifest)
}

// runContentPrep runs Microsoft's tool over an already exported folder. The
// export is never rolled back: a failure here leaves a complete, usable bundle.
func runContentPrep(ctx context.Context, stderr io.Writer, tool, exported, output string) error {
	prepCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	result, err := intune.PrepareContent(prepCtx, tool, exported, output)
	fmt.Fprint(stderr, result)
	if err != nil {
		return fmt.Errorf("bundle exported; content prep failed: %w", err)
	}
	fmt.Fprintln(stderr, "Created", filepath.Join(output, intune.PreparedPackageName))
	return nil
}
