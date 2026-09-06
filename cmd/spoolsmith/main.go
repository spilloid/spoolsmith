package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spilloid/spoolsmith/internal/actionlog"
	"github.com/spilloid/spoolsmith/internal/catalog"
	"github.com/spilloid/spoolsmith/internal/inspect"
	"github.com/spilloid/spoolsmith/internal/install"
	"github.com/spilloid/spoolsmith/internal/probe"
)

type application struct {
	workflow       install.Workflow
	environment    install.Environment
	collect        func(context.Context, string) (probe.Result, error)
	discover       func(context.Context, string) (probe.Discovery, error)
	inputTerminal  bool
	outputTerminal bool
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

	switch args[0] {
	case "--help", "-h", "help":
		printUsage(stdout)
		return 0
	case "discover":
		return runDiscover(ctx, args[1:], stdout, stderr, app)
	case "profile":
		return runProfile(ctx, args[1:], stdout, stderr, app)
	case "inspect":
		if len(args) != 2 {
			return usageError(stdout, stderr, "inspect", errors.New("inspect requires exactly one target"))
		}
		result, err := inspect.Target(ctx, args[1])
		if err != nil {
			return commandError(stdout, stderr, "inspect", err, int(install.ExitGeneralError))
		}
		return encodeSuccess(stdout, stderr, "inspect", result)
	case "catalog":
		return runCatalog(ctx, args[1:], stdout, stderr, app)
	case "install", "add", "configure":
		options, err := parseInstallArgs(args[1:])
		if err != nil {
			return usageError(stdout, stderr, args[0], err)
		}
		if args[0] == "configure" {
			if options.Profile == nil {
				return usageError(stdout, stderr, args[0], errors.New("configure requires --profile <file>"))
			}
			options.UpdateExisting = true
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
		case "--yes", "--json", "--non-interactive", "--dry-run", "--what-if":
			key := arg
			if arg == "--what-if" {
				key = "--dry-run"
			}
			if seen[key] {
				return options, fmt.Errorf("duplicate flag %s", arg)
			}
			seen[key] = true
			switch key {
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
			p, err := install.LoadProfile(args[index])
			if err != nil {
				return options, err
			}
			if err := p.ResolvePackagePath(args[index]); err != nil {
				return options, err
			}
			options.Profile = &p
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
			p, err := install.LoadProfile(args[index])
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
	printUsage(stderr)
	return code
}

func encodeJSON(writer io.Writer, value any) error {
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func printUsage(writer io.Writer) {
	fmt.Fprintln(writer, "SpoolSmith: discover, save, and map network printers")
	fmt.Fprintln(writer, "usage: spoolsmith discover <IPv4-CIDR> (/24 through /32)")
	fmt.Fprintln(writer, "       spoolsmith profile capture <target> <file> --name <queue> --driver <installed-driver-name>")
	fmt.Fprintln(writer, "       spoolsmith profile edit <file> [--name <queue>] [--driver <name>] [--target <ip>]")
	fmt.Fprintln(writer, "       spoolsmith profile edit <file> [--package <recipe-id> --archive <local-file> | --clear-package]")
	fmt.Fprintln(writer, "       spoolsmith add --profile <file> [--dry-run] [--json]")
	fmt.Fprintln(writer, "       spoolsmith configure --profile <file> [--dry-run] [--json]")
	fmt.Fprintln(writer, "       spoolsmith remove --profile <file> [--dry-run] [--json]")
	fmt.Fprintln(writer, "       spoolsmith inspect <target>")
	fmt.Fprintln(writer, "       spoolsmith catalog probe <ip>")
	fmt.Fprintln(writer, "       spoolsmith catalog families")
	fmt.Fprintln(writer, "       spoolsmith install <ip> [--force-family <id>] [--yes|--non-interactive|--json] [--dry-run|--what-if]")
	fmt.Fprintln(writer, "       spoolsmith uninstall <printer-name> [--purge-driver] [--yes|--non-interactive|--json] [--dry-run|--what-if]")
	fmt.Fprintln(writer, "       --dry-run/--what-if takes precedence over --yes and never prompts or mutates")
}

func isTerminal(file *os.File) bool {
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}
