package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spilloid/spoolsmith/internal/actionlog"
	"github.com/spilloid/spoolsmith/internal/bundle"
	"github.com/spilloid/spoolsmith/internal/catalog"
	"github.com/spilloid/spoolsmith/internal/inspect"
	"github.com/spilloid/spoolsmith/internal/install"
	"github.com/spilloid/spoolsmith/internal/intune"
	"github.com/spilloid/spoolsmith/internal/probe"
)

type application struct {
	workflow       install.Workflow
	environment    install.Environment
	collect        func(context.Context, string) (probe.Result, error)
	discover       func(context.Context, string) (probe.Discovery, error)
	inputTerminal  bool
	outputTerminal bool
	// prepToolDirs are searched (only directly, never recursively or via PATH)
	// for Microsoft's IntuneWinAppUtil.exe. Empty in tests.
	prepToolDirs []string
}

type errorResponse struct {
	Command string `json:"command"`
	Status  string `json:"status"`
	Error   string `json:"error"`
}

func main() {
	app := application{
		workflow:       install.NewWorkflow(),
		environment:    install.NewEnvironment(),
		collect:        probe.Collect,
		discover:       probe.Discover,
		inputTerminal:  isTerminal(os.Stdin),
		outputTerminal: isTerminal(os.Stdout),
		prepToolDirs:   executableDir(),
	}
	args := os.Args[1:]
	started := time.Now()
	code := run(context.Background(), args, os.Stdin, os.Stdout, os.Stderr, app)
	logRun(args, code, time.Since(started))
	os.Exit(code)
}

// logRun records one best-effort observability entry per CLI invocation. It
// wraps run() rather than instrumenting individual command branches, so the
// already-reviewed command logic and its tests are untouched. Logging never
// affects the command's own exit code.
func logRun(args []string, code int, duration time.Duration) {
	op := "help"
	var opArgs []string
	if len(args) > 0 {
		op = args[0]
		opArgs = args[1:]
	}
	status := "success"
	if code != 0 {
		status = "error"
	}
	_ = actionlog.Default().Record(actionlog.Entry{
		Source:   "cli",
		Op:       op,
		Args:     opArgs,
		Status:   status,
		ExitCode: &code,
		Duration: duration.Round(time.Millisecond).String(),
	})
}

func run(ctx context.Context, args []string, input io.Reader, stdout, stderr io.Writer, app application) int {
	if len(args) == 0 {
		return usageError(stdout, stderr, "spoolsmith", errors.New("command is required"))
	}

	// "<command> --help"/"-h" is intercepted before that command's own
	// parser ever sees it, rather than being treated as an unknown flag
	// (most commands) or, for `inspect`, as the target to inspect.
	if len(args) > 1 && (args[1] == "--help" || args[1] == "-h") {
		if usage, ok := commandUsage[args[0]]; ok {
			fmt.Fprint(stdout, usage)
			return 0
		}
	}

	switch args[0] {
	// capabilities stays on the command table so it always names this build's
	// own intune-endpoint support: the packager identifies a compatible CLI by
	// scanning the executable for this marker string, so removing the only
	// reference to it also removes it from the compiled binary, and every
	// build-from-source packaging run would reject its own fresh CLI.
	case "capabilities":
		if len(args) != 1 {
			return usageError(stdout, stderr, "capabilities", errors.New("capabilities accepts no arguments"))
		}
		return encodeSuccess(stdout, stderr, "capabilities", []string{intune.EndpointCapability})
	case "drivers":
		if len(args) != 1 {
			return usageError(stdout, stderr, "drivers", errors.New("drivers accepts no arguments"))
		}
		names, err := install.DriverNames(ctx, app.environment)
		if err != nil {
			return commandError(stdout, stderr, "drivers", err, 1)
		}
		return encodeSuccess(stdout, stderr, "drivers", names)
	case "--help", "-h", "help":
		if len(args) > 1 {
			if usage, ok := commandUsage[args[1]]; ok {
				fmt.Fprint(stdout, usage)
				return 0
			}
			return usageError(stdout, stderr, "help", fmt.Errorf("unknown command %q", args[1]))
		}
		printUsage(stdout)
		return 0
	case "discover":
		return runDiscover(ctx, args[1:], stdout, stderr, app)
	case "profile":
		return runProfile(ctx, args[1:], stdout, stderr, app)
	case "copy", "clone":
		return runClone(ctx, args[1:], input, stdout, stderr, app)
	case "printers":
		return runPrinters(ctx, args[1:], stdout, stderr, app)
	case "repoint":
		return runRepoint(ctx, args[1:], input, stdout, stderr, app)
	case "apply":
		return runApply(ctx, args[1:], input, stdout, stderr, app)
	case "bundle":
		return runBundle(args[1:], stdout, stderr)
	case "intune":
		return runIntune(ctx, args[1:], input, stdout, stderr, app)
	case "status":
		return runStatus(ctx, args[1:], stdout, stderr, app)
	case "inspect":
		if len(args) != 2 {
			return usageError(stdout, stderr, "inspect", errors.New("inspect requires exactly one target"))
		}
		// A printer file (.ssb) or printer set (.zip) is the same job `bundle
		// inspect` already does -- one command name, not two commands that
		// overlap on one file type. runBundle itself dispatches on content.
		if ext := filepath.Ext(args[1]); strings.EqualFold(ext, ".ssb") || strings.EqualFold(ext, bundle.SetExt) {
			return runBundle([]string{"inspect", args[1]}, stdout, stderr)
		}
		result, err := inspect.Target(ctx, args[1])
		if err != nil {
			return commandError(stdout, stderr, "inspect", err, int(install.ExitGeneralError))
		}
		return encodeSuccess(stdout, stderr, "inspect", result)
	case "catalog":
		deprecationNotice(stderr, "catalog", "Copy a working printer with `copy` and `apply` instead.")
		return runCatalog(ctx, args[1:], stdout, stderr, app)
	case "install", "add", "configure":
		options, err := parseInstallArgs(args[1:])
		if err != nil {
			return usageError(stdout, stderr, args[0], err)
		}
		if args[0] == "install" && options.Profile == nil {
			deprecationNotice(stderr, "install <ip>", "Copy a working printer with `copy` and `apply`, or save one with `profile capture` and `add --profile`.")
		}
		if args[0] == "configure" {
			if options.Profile == nil {
				return usageError(stdout, stderr, args[0], errors.New("configure requires --profile <file>"))
			}
			options.UpdateExisting = true
		}
		if options.BundleDriver != nil {
			fmt.Fprintf(stderr, "Verified %d driver files from the bundle (%s)\n", options.BundleDriver.FileCount, options.BundleDriver.StageDirName)
		}
		options.Compact = app.outputTerminal && !options.JSON
		outcome, code := app.workflow.RunInstall(ctx, app.environment, input, stderr, app.inputTerminal, options)
		outcome.Operation = args[0]
		if outcome.Error != "" {
			fmt.Fprintf(stderr, "spoolsmith %s: %s\n", args[0], outcome.Error)
		}
		if app.outputTerminal && !options.JSON {
			return int(code)
		}
		if err := encodeJSON(stdout, outcome); err != nil {
			fmt.Fprintf(stderr, "spoolsmith install: encode result: %v\n", err)
			return int(install.ExitGeneralError)
		}
		return int(code)
	case "uninstall", "remove":
		options, err := parseUninstallArgs(args[1:])
		if err != nil {
			return usageError(stdout, stderr, args[0], err)
		}
		options.Compact = app.outputTerminal && !options.JSON
		outcome, code := app.workflow.RunUninstall(ctx, app.environment, input, stderr, app.inputTerminal, options)
		outcome.Operation = args[0]
		if outcome.Error != "" {
			fmt.Fprintf(stderr, "spoolsmith %s: %s\n", args[0], outcome.Error)
		}
		if app.outputTerminal && !options.JSON {
			return int(code)
		}
		if err := encodeJSON(stdout, outcome); err != nil {
			fmt.Fprintf(stderr, "spoolsmith uninstall: encode result: %v\n", err)
			return int(install.ExitGeneralError)
		}
		return int(code)
	default:
		return usageError(stdout, stderr, args[0], fmt.Errorf("unknown command %q", args[0]))
	}
}

func runCatalog(ctx context.Context, args []string, stdout, stderr io.Writer, app application) int {
	if len(args) == 1 && args[0] == "families" {
		return encodeSuccess(stdout, stderr, "catalog families", catalog.Families())
	}
	if len(args) == 2 && args[0] == "probe" {
		result, err := app.collect(ctx, args[1])
		if err != nil {
			return commandError(stdout, stderr, "catalog probe", err, int(install.ExitGeneralError))
		}
		return encodeSuccess(stdout, stderr, "catalog probe", result)
	}
	return usageError(stdout, stderr, "catalog", errors.New("catalog requires 'families' or 'probe <ip>'"))
}

func parseInstallArgs(args []string) (install.InstallOptions, error) {
	var options install.InstallOptions
	seen := make(map[string]bool)
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch arg {
		case "--yes", "--json", "--non-interactive", "--dry-run", "--what-if", "--offline":
			key := arg
			if arg == "--what-if" {
				key = "--dry-run"
			}
			if seen[key] {
				return options, fmt.Errorf("duplicate flag %s", arg)
			}
			seen[key] = true
			switch key {
			case "--offline":
				options.Offline = true
			case "--yes":
				options.Yes = true
				options.NonInteractive = true
			case "--json":
				options.JSON = true
			case "--non-interactive":
				options.NonInteractive = true
			case "--dry-run":
				options.DryRun = true
			}
		case "--profile":
			if seen[arg] || index+1 >= len(args) || strings.HasPrefix(args[index+1], "-") {
				return options, errors.New("--profile requires one file")
			}
			seen[arg] = true
			index++
			p, driver, err := loadProfileFile(args[index])
			if err != nil {
				return options, err
			}
			options.Profile = &p
			options.BundleDriver = driver
		case "--force-family":
			if seen[arg] || index+1 >= len(args) {
				return options, errors.New("--force-family requires one non-empty family ID")
			}
			seen[arg] = true
			index++
			options.ForceFamily = args[index]
			if options.ForceFamily == "" || strings.HasPrefix(options.ForceFamily, "-") {
				return options, errors.New("--force-family requires one non-empty family ID")
			}
		default:
			if strings.HasPrefix(arg, "--force-family=") {
				if seen["--force-family"] {
					return options, errors.New("duplicate flag --force-family")
				}
				seen["--force-family"] = true
				options.ForceFamily = strings.TrimPrefix(arg, "--force-family=")
				if options.ForceFamily == "" {
					return options, errors.New("--force-family requires one non-empty family ID")
				}
				continue
			}
			if strings.HasPrefix(arg, "-") {
				return options, fmt.Errorf("unknown flag %s", arg)
			}
			if options.Target != "" {
				return options, errors.New("install accepts exactly one target")
			}
			options.Target = arg
		}
	}
	if options.Profile != nil {
		if options.Target != "" || options.ForceFamily != "" {
			return options, errors.New("--profile cannot be combined with a target or --force-family")
		}
		return options, nil
	}
	if options.Offline {
		return options, errors.New("--offline requires an administrator-prevalidated --profile")
	}
	if options.Target == "" {
		return options, errors.New("install requires exactly one target")
	}
	return options, nil
}

func parseUninstallArgs(args []string) (install.UninstallOptions, error) {
	var options install.UninstallOptions
	seen := make(map[string]bool)
	for index := 0; index < len(args); index++ {
		arg := args[index]
		if arg == "--profile" {
			if seen[arg] || options.PrinterName != "" || index+1 >= len(args) || strings.HasPrefix(args[index+1], "-") {
				return options, errors.New("--profile requires one file and cannot be combined with a printer name")
			}
			seen[arg] = true
			index++
			p, err := bundle.LoadProfile(args[index])
			if err != nil {
				return options, err
			}
			options.PrinterName = p.PrinterName
			options.Profile = &p
			continue
		}
		if strings.HasPrefix(arg, "-") {
			if seen[arg] {
				return options, fmt.Errorf("duplicate flag %s", arg)
			}
			seen[arg] = true
			switch arg {
			case "--purge-driver":
				options.PurgeDriver = true
			case "--yes":
				options.Yes = true
				options.NonInteractive = true
			case "--json":
				options.JSON = true
			case "--non-interactive":
				options.NonInteractive = true
			case "--dry-run", "--what-if":
				if seen["dry-run-alias"] {
					return options, fmt.Errorf("duplicate flag %s", arg)
				}
				seen["dry-run-alias"] = true
				options.DryRun = true
			default:
				return options, fmt.Errorf("unknown flag %s", arg)
			}
			continue
		}
		if options.PrinterName != "" {
			return options, errors.New("uninstall accepts exactly one printer name")
		}
		options.PrinterName = arg
	}
	if options.PrinterName == "" {
		return options, errors.New("uninstall requires exactly one printer name")
	}
	return options, nil
}

func encodeSuccess(stdout, stderr io.Writer, command string, value any) int {
	if err := encodeJSON(stdout, value); err != nil {
		fmt.Fprintf(stderr, "spoolsmith %s: encode result: %v\n", command, err)
		return int(install.ExitGeneralError)
	}
	return int(install.ExitSuccess)
}

func commandError(stdout, stderr io.Writer, command string, err error, code int) int {
	response := errorResponse{Command: command, Status: "error", Error: err.Error()}
	if encodeErr := encodeJSON(stdout, response); encodeErr != nil {
		fmt.Fprintf(stderr, "spoolsmith %s: encode result: %v\n", command, encodeErr)
		return int(install.ExitGeneralError)
	}
	fmt.Fprintf(stderr, "spoolsmith %s: %v\n", command, err)
	return code
}

func usageError(stdout, stderr io.Writer, command string, err error) int {
	code := commandError(stdout, stderr, command, err, int(install.ExitUsageError))
	// Show the one command's own syntax, not the entire catalog, whenever a
	// mistake can be attributed to a specific command; fall back to the full
	// listing only for a genuinely unknown or unnamed command.
	if usage, ok := commandUsage[command]; ok {
		fmt.Fprint(stderr, usage)
	} else {
		printUsage(stderr)
	}
	return code
}

func encodeJSON(writer io.Writer, value any) error {
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

// commandUsage gives each top-level command its own syntax, so `<command>
// --help`/`-h` and `help <command>` show only what's relevant instead of
// silently falling into that command's own argument parser (which used to
// treat "--help" as an unknown flag, or as an inspect target) or dumping the
// entire command catalog on every usage mistake.
var commandUsage = map[string]string{
	"capabilities": "spoolsmith capabilities                    this build's intune-endpoint capability marker\n",
	"drivers":      "spoolsmith drivers                         exact registered Windows driver names\n",
	"discover":     "spoolsmith discover <IPv4-CIDR>            /24 through /32\n",
	"inspect":      "spoolsmith inspect <target>                target is an IP address, a fixture file, a printer file (.ssb) or a printer set (.zip)\n",
	"catalog":      "spoolsmith catalog probe <ip> | catalog families\n",
	"printers":     "spoolsmith printers [--copyable] [--json]   installed queues, and which can be copied\n",
	"status":       "spoolsmith status --profile <file> [--json] local configuration only, no network\n",
	"copy":         copyUsage,
	"apply": "spoolsmith apply <file.ssb|set.zip> [--member <name>] [--dry-run] [--offline] [--update]\n" +
		"                                [--plan-hash <fingerprint>] [--yes] [--non-interactive] [--json]\n" +
		"A .ssb is one printer; a .zip is a printer set. Each printer in a set gets its own plan and\n" +
		"its own confirmation; --member applies just one of them.\n",
	"bundle": "spoolsmith bundle inspect <file.ssb|set.zip>  read a printer file or printer set, touching nothing\n",
	"profile": "spoolsmith profile export-all <folder> <set.zip> [--dry-run]\n" +
		"spoolsmith profile import-all <set.zip> <folder> [--dry-run]\n" +
		"--dry-run reviews filenames, printer settings and destination conflicts without writing files.\n" +
		"spoolsmith profile capture <target> <file> --name <queue> --driver <installed-driver-name>\n" +
		"spoolsmith profile edit <file> [--name <queue>] [--driver <name>] [--target <ip>]\n" +
		"spoolsmith profile edit <file> [--package <recipe-id> --archive <local-file>] [--clear-package]\n",
	"add":       "spoolsmith add --profile <file> [--offline] [--dry-run] [--yes] [--json]\n",
	"configure": "spoolsmith configure --profile <file> [--offline] [--dry-run] [--yes] [--json]\n",
	"install":   "spoolsmith install <ip> [--force-family <id>] [--dry-run|--what-if] [--yes] [--non-interactive] [--json]\n",
	"repoint":   "spoolsmith repoint <queue> <new-ip> [--dry-run] [--yes] [--non-interactive] [--json]\n",
	"remove":    "spoolsmith remove --profile <file> [--dry-run] [--json]\n",
	"uninstall": "spoolsmith uninstall <printer-name> [--purge-driver] [--dry-run|--what-if] [--yes] [--non-interactive] [--json]\n",
	"intune": "spoolsmith intune wizard                   interactive, one export directory at a time\n" +
		"spoolsmith intune build --profile <file> --binary <exe> [--revision <n>] [--dry-run]\n" +
		"--binary-sha256, --id, --name, --description and --output are all derived from the profile\n" +
		"when omitted; give them explicitly only to override the suggested value.\n" +
		"If Microsoft's IntuneWinAppUtil.exe is beside spoolsmith.exe it also creates the .intunewin\n" +
		"(--content-prep-tool/--content-prep-output override it; --no-content-prep skips it).\n" +
		"Never signs in to a tenant or uploads anything; `intune build --help` lists every flag.\n",
}

// copyUsage is shared by `copy --help` and the full listing.
const copyUsage = "spoolsmith copy [<queue>] [--out <file.ssb>] [--settings-only] [--note <text>]\n" +
	"spoolsmith copy --all [--out <printers.zip>] [--settings-only] [--note <text>]\n" +
	"A .ssb is one printer; a .zip is a printer set holding several .ssb files.\n" +
	"The driver is included whenever it can be (it needs administrator rights); otherwise the\n" +
	"copy carries the settings only and says why. --settings-only always leaves the driver out.\n" +
	"Omit <queue> to choose from a numbered list; omit --out to name the file after the queue.\n" +
	"--all copies every copyable queue into one printer set (default name:\n" +
	"SpoolSmith-printers-<date>-<time>.zip in the current folder), skipping and reporting a\n" +
	"reason for any queue that can't be reproduced.\n"

func init() {
	commandUsage["clone"] = commandUsage["copy"]
}

func printUsage(writer io.Writer) {
	fmt.Fprintln(writer, "SpoolSmith: find, save, copy, and map network printers")
	fmt.Fprintln(writer, "")
	fmt.Fprintln(writer, "Look at this PC")
	fmt.Fprintln(writer, "  spoolsmith printers [--copyable] [--json]   installed queues, and which can be copied")
	fmt.Fprintln(writer, "  spoolsmith drivers                         exact registered Windows driver names")
	fmt.Fprintln(writer, "  spoolsmith status --profile <file> [--json] local configuration only, no network")
	fmt.Fprintln(writer, "")
	fmt.Fprintln(writer, "Copy a printer from one PC to another")
	for _, line := range strings.Split(strings.TrimSuffix(copyUsage, "\n"), "\n") {
		fmt.Fprintln(writer, "  "+line)
	}
	fmt.Fprintln(writer, "  spoolsmith apply <file.ssb|set.zip> [--member <name>] [--dry-run] [--offline] [--update]")
	fmt.Fprintln(writer, "                                 [--plan-hash <fingerprint>] [--yes] [--non-interactive] [--json]")
	fmt.Fprintln(writer, "  spoolsmith bundle inspect <file.ssb|set.zip>  read a printer file or printer set, touching nothing")
	fmt.Fprintln(writer, "  Each printer in a set gets its own plan and its own confirmation.")
	fmt.Fprintln(writer, "")
	fmt.Fprintln(writer, "Find and save a printer")
	fmt.Fprintln(writer, "  spoolsmith discover <IPv4-CIDR>            /24 through /32")
	fmt.Fprintln(writer, "  spoolsmith inspect <target>")
	fmt.Fprintln(writer, "  spoolsmith profile export-all <folder> <set.zip> [--dry-run]")
	fmt.Fprintln(writer, "  spoolsmith profile import-all <set.zip> <folder> [--dry-run]")
	fmt.Fprintln(writer, "  spoolsmith profile capture <target> <file> --name <queue> --driver <installed-driver-name>")
	fmt.Fprintln(writer, "  spoolsmith profile edit <file> [--name <queue>] [--driver <name>] [--target <ip>]")
	fmt.Fprintln(writer, "  spoolsmith profile edit <file> [--package <recipe-id> --archive <local-file> | --clear-package]")
	fmt.Fprintln(writer, "")
	fmt.Fprintln(writer, "Map, change, and remove queues")
	fmt.Fprintln(writer, "  spoolsmith add|configure --profile <file> [--offline] [--dry-run] [--yes] [--json]")
	fmt.Fprintln(writer, "  spoolsmith repoint <queue> <new-ip> [--dry-run] [--yes] [--non-interactive] [--json]")
	fmt.Fprintln(writer, "  spoolsmith remove --profile <file> [--dry-run] [--json]")
	fmt.Fprintln(writer, "  spoolsmith uninstall <printer-name> [--purge-driver] [--dry-run|--what-if] [--yes] [--non-interactive] [--json]")
	fmt.Fprintln(writer, "")
	fmt.Fprintln(writer, "Package a validated profile for Intune (local only; never contacts a tenant)")
	fmt.Fprintln(writer, "  spoolsmith intune wizard                   interactive, one export directory at a time")
	fmt.Fprintln(writer, "  spoolsmith intune build --profile <file> --binary <exe> [--revision <n>] [--dry-run]")
	fmt.Fprintln(writer, "  --binary-sha256, --id, --name, --description and --output are all derived from the")
	fmt.Fprintln(writer, "  profile when omitted; give them explicitly only to override the suggested value.")
	fmt.Fprintln(writer, "  Produces install.ps1/uninstall.ps1/detect.ps1 and a README with the exact commands")
	fmt.Fprintln(writer, "  to paste into Intune's Win32 app. If Microsoft's IntuneWinAppUtil.exe is beside")
	fmt.Fprintln(writer, "  spoolsmith.exe it also creates the .intunewin (--content-prep-tool/--content-prep-output")
	fmt.Fprintln(writer, "  override it; --no-content-prep skips it). Uploading and assigning the app in")
	fmt.Fprintln(writer, "  Intune remains a manual step; this does not sign in to a tenant.")
	fmt.Fprintln(writer, "")
	fmt.Fprintln(writer, "Pending deprecation (removed in a future build; copy and apply replace them)")
	fmt.Fprintln(writer, "  spoolsmith catalog probe <ip> | catalog families")
	fmt.Fprintln(writer, "  spoolsmith install <ip> [--force-family <id>] [--dry-run|--what-if] [--yes] [--non-interactive] [--json]")
	fmt.Fprintln(writer, "")
	fmt.Fprintln(writer, "--dry-run/--what-if takes precedence over --yes and never prompts or mutates.")
	fmt.Fprintln(writer, "--offline skips the live identity check; the plan says so before you confirm it.")
}

// executableDir is the folder holding this program, where an administrator
// would naturally drop IntuneWinAppUtil.exe next to it.
func executableDir() []string {
	if exe, err := os.Executable(); err == nil {
		return []string{filepath.Dir(exe)}
	}
	return nil
}

func isTerminal(file *os.File) bool {
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// pendingDeprecationNote is appended to the help of commands that belong to
// the original catalog-driven design. SpoolSmith copies a printer that
// already works instead of identifying a model and choosing its OEM driver;
// these stay working until a future build removes them.
const pendingDeprecationNote = "Pending deprecation: this catalog-driven command will be removed in a future build.\n" +
	"Copy a working printer with `copy` and `apply` instead.\n"

// deprecationNotice warns on stderr, leaving stdout (and any JSON on it)
// unchanged.
func deprecationNotice(stderr io.Writer, command, instead string) {
	fmt.Fprintf(stderr, "Note: `spoolsmith %s` is pending deprecation and will be removed in a future build. %s\n", command, instead)
}
