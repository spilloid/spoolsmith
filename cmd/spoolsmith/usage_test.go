package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spilloid/spoolsmith/internal/bundle"
	"github.com/spilloid/spoolsmith/internal/evidence"
	"github.com/spilloid/spoolsmith/internal/install"
	"github.com/spilloid/spoolsmith/internal/probe"
)

func TestUsageErrorsShowOnlyTheCommandsOwnSyntax(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "profile edit", args: []string{"profile", "edit", "only-a-file.ssb"}, want: profileEditUsage},
		{name: "profile export-all", args: []string{"profile", "export-all", "one-path"}, want: profileExportAllUsage},
		{name: "profile import-all", args: []string{"profile", "import-all", "one-path"}, want: profileImportAllUsage},
		{name: "intune build", args: []string{"intune", "build", "positional"}, want: intuneBuildUsage},
		{name: "intune wizard", args: []string{"intune", "wizard"}, want: intuneWizardUsage},
		{name: "bundle", args: []string{"bundle"}, want: bundleInspectUsage},
		{name: "help", args: []string{"help", "nosuch"}, want: commandUsage["help"]},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := run(context.Background(), tt.args, strings.NewReader(""), &stdout, &stderr, testApplication())
			if code != int(install.ExitUsageError) {
				t.Fatalf("run() code = %d, want %d; stderr=%q", code, install.ExitUsageError, stderr.String())
			}
			assertValidJSON(t, stdout.Bytes())
			if !strings.Contains(stderr.String(), tt.want) {
				t.Fatalf("stderr does not show the command's own syntax %q:\n%s", tt.want, stderr.String())
			}
			if strings.Contains(stderr.String(), "SpoolSmith: find, save") {
				t.Fatalf("stderr dumped the whole catalog:\n%s", stderr.String())
			}
		})
	}
}

func TestLookupUsageFallsBackToTheParentCommand(t *testing.T) {
	if usage, ok := lookupUsage("intune upload"); !ok || usage != commandUsage["intune"] {
		t.Fatalf("lookupUsage(intune upload) = %q, %v; want the intune usage", usage, ok)
	}
	if _, ok := lookupUsage("nosuch thing"); ok {
		t.Fatal("an unknown command found a usage entry")
	}
}

func TestHelpNamesASubcommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), []string{"help", "profile", "edit"}, strings.NewReader(""), &stdout, &stderr, testApplication()); code != 0 {
		t.Fatalf("run() code = %d; stderr=%q", code, stderr.String())
	}
	if stdout.String() != profileEditUsage {
		t.Fatalf("help profile edit = %q, want %q", stdout.String(), profileEditUsage)
	}
}

func TestRemoveAndUninstallShareOneCompleteUsage(t *testing.T) {
	var removeHelp, uninstallHelp bytes.Buffer
	for args, out := range map[string]*bytes.Buffer{"remove": &removeHelp, "uninstall": &uninstallHelp} {
		if code := run(context.Background(), []string{args, "--help"}, strings.NewReader(""), out, &bytes.Buffer{}, testApplication()); code != 0 {
			t.Fatalf("%s --help code = %d", args, code)
		}
	}
	if removeHelp.String() != uninstallHelp.String() {
		t.Fatalf("remove and uninstall help differ:\n%s\n%s", removeHelp.String(), uninstallHelp.String())
	}
	// Every flag parseUninstallArgs accepts is documented.
	for _, flag := range []string{"<printer-name>", "--profile", "--purge-driver", "--dry-run", "--what-if", "--yes", "--non-interactive", "--json"} {
		if !strings.Contains(removeHelp.String(), flag) {
			t.Errorf("remove --help omits %s:\n%s", flag, removeHelp.String())
		}
	}
	var full bytes.Buffer
	printUsage(&full)
	if !strings.Contains(full.String(), "--windows-driver\n") || !strings.Contains(full.String(), "uninstall|remove --profile") {
		t.Fatalf("full listing omits profile capture --windows-driver or remove --profile:\n%s", full.String())
	}
}

func TestTakeValue(t *testing.T) {
	args := []string{"--out", "file.ssb", "--note", "--settings-only", "--name", " ", "--last"}
	for _, tt := range []struct {
		index int
		want  string
		ok    bool
	}{{0, "file.ssb", true}, {2, "", false}, {4, "", false}, {6, "", false}} {
		if got, ok := takeValue(args, tt.index); got != tt.want || ok != tt.ok {
			t.Errorf("takeValue(%s) = %q, %v; want %q, %v", args[tt.index], got, ok, tt.want, tt.ok)
		}
	}
}

func TestValueFlagsRefuseTheNextFlagAsTheirValue(t *testing.T) {
	app := testApplication()
	app.collect = func(context.Context, string) (probe.Result, error) { panic("a usage error probed the network") }
	dir := t.TempDir()
	saved := filepath.Join(dir, "saved.ssb")
	if err := bundle.SaveProfile(saved, install.Profile{Version: 1, PrinterName: "Accounting", DriverName: "Verified Windows Driver", Target: "192.0.2.10", Evidence: evidence.Evidence{IP: "192.0.2.10", Provenance: "captured", HTTPTitle: "Test Model"}}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(saved)
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"copy", "Accounting", "--out", "--settings-only"},
		{"copy", "Accounting", "--note", "--settings-only"},
		{"copy", "--all", "--out", "--settings-only"},
		{"apply", saved, "--plan-hash", "--yes"},
		{"apply", saved, "--member", "--dry-run"},
		{"profile", "capture", "192.0.2.10", filepath.Join(dir, "new.ssb"), "--name", "--driver", "D"},
		{"profile", "capture", "192.0.2.10", filepath.Join(dir, "new.ssb"), "--name", "Q", "--driver", "--windows-driver"},
		{"profile", "edit", saved, "--name", "--driver", "D"},
		{"profile", "edit", saved, "--target", "--name"},
	} {
		var stdout, stderr bytes.Buffer
		if code := run(context.Background(), args, strings.NewReader(""), &stdout, &stderr, app); code != int(install.ExitUsageError) {
			t.Errorf("%v: code = %d, want %d; stderr=%q", args, code, install.ExitUsageError, stderr.String())
		}
		if !strings.Contains(stderr.String(), "requires") {
			t.Errorf("%v: stderr does not say what the flag requires: %q", args, stderr.String())
		}
	}
	after, err := os.ReadFile(saved)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("a refused edit changed the saved file (err %v)", err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Fatalf("a refused command wrote files: %v", entries)
	}
}

func TestHelpAnywhereInTheArgumentsShowsThatCommandsUsage(t *testing.T) {
	app := testApplication()
	app.collect = func(context.Context, string) (probe.Result, error) { panic("--help probed the network") }
	tests := []struct {
		args []string
		want string
	}{
		{args: []string{"copy", "Accounting", "--settings-only", "--help"}, want: copyUsage},
		{args: []string{"apply", "x.ssb", "--dry-run", "-h"}, want: commandUsage["apply"]},
		{args: []string{"uninstall", "Accounting", "--purge-driver", "--help"}, want: uninstallUsage},
		{args: []string{"profile", "edit", "x.ssb", "--name", "Q", "--help"}, want: profileEditUsage},
		{args: []string{"profile", "capture", "192.0.2.10", "x.ssb", "--help"}, want: profileCaptureUsage},
		{args: []string{"intune", "wizard", "--help"}, want: intuneWizardUsage},
		{args: []string{"inspect", "--help"}, want: commandUsage["inspect"]},
	}
	for _, tt := range tests {
		var stdout, stderr bytes.Buffer
		if code := run(context.Background(), tt.args, strings.NewReader(""), &stdout, &stderr, app); code != 0 {
			t.Errorf("%v: code = %d; stderr=%q", tt.args, code, stderr.String())
		}
		if stdout.String() != tt.want {
			t.Errorf("%v: stdout = %q, want %q", tt.args, stdout.String(), tt.want)
		}
	}
}

func TestHelpAsAFlagValueIsNotAHelpRequest(t *testing.T) {
	for _, args := range [][]string{
		{"copy", "Accounting", "--out", "--help"},
		{"apply", "x.ssb", "--member", "-h"},
		{"profile", "edit", "x.ssb", "--name", "--help"},
	} {
		if usage, ok := helpRequested(args); ok {
			t.Errorf("%v was taken as a help request: %q", args, usage)
		}
		var stdout, stderr bytes.Buffer
		if code := run(context.Background(), args, strings.NewReader(""), &stdout, &stderr, testApplication()); code == 0 {
			t.Errorf("%v: code = 0; a flag value of --help must not succeed", args)
		}
	}
	// intune build keeps its own flag listing.
	if _, ok := helpRequested([]string{"intune", "build", "--help"}); ok {
		t.Error("intune build --help was intercepted instead of listing every flag")
	}
}

func TestStatusFlagOrderDoesNotMatter(t *testing.T) {
	saved := filepath.Join(t.TempDir(), "saved.ssb")
	if err := bundle.SaveProfile(saved, install.Profile{Version: 1, PrinterName: "Test Printer", DriverName: "Verified Windows Driver", Target: "192.0.2.10", Evidence: evidence.Evidence{IP: "192.0.2.10", Provenance: "captured", HTTPTitle: "Test Model"}}); err != nil {
		t.Fatal(err)
	}
	var first, second, stderr bytes.Buffer
	codeFirst := run(context.Background(), []string{"status", "--profile", saved, "--json"}, strings.NewReader(""), &first, &stderr, testApplication())
	codeSecond := run(context.Background(), []string{"status", "--json", "--profile", saved}, strings.NewReader(""), &second, &stderr, testApplication())
	if codeFirst == int(install.ExitUsageError) || codeFirst != codeSecond || first.String() != second.String() {
		t.Fatalf("status depends on flag order: %d %q vs %d %q; stderr=%q", codeFirst, first.String(), codeSecond, second.String(), stderr.String())
	}
	for _, args := range [][]string{
		{"status"},
		{"status", "--json"},
		{"status", "--profile"},
		{"status", "--profile", "--json"},
		{"status", "--profile", saved, "--profile", saved},
		{"status", "--profile", saved, "--json", "--json"},
		{"status", "--profile", saved, "extra"},
	} {
		var stdout bytes.Buffer
		if code := run(context.Background(), args, strings.NewReader(""), &stdout, &bytes.Buffer{}, testApplication()); code != int(install.ExitUsageError) {
			t.Errorf("%v: code = %d, want %d", args, code, install.ExitUsageError)
		}
		assertValidJSON(t, stdout.Bytes())
	}
}

func TestVersionPrintsTheBuildStamp(t *testing.T) {
	saved := version
	defer func() { version = saved }()
	for _, stamp := range []string{"", "v1.5.0"} {
		version = stamp
		for _, command := range []string{"version", "--version"} {
			var stdout, stderr bytes.Buffer
			if code := run(context.Background(), []string{command}, strings.NewReader(""), &stdout, &stderr, testApplication()); code != 0 {
				t.Fatalf("%s: code = %d; stderr=%q", command, code, stderr.String())
			}
			if stdout.String() != versionString()+"\n" || stderr.Len() != 0 {
				t.Fatalf("%s: stdout = %q stderr = %q, want %q", command, stdout.String(), stderr.String(), versionString())
			}
		}
	}
	var stdout bytes.Buffer
	if code := run(context.Background(), []string{"version", "extra"}, strings.NewReader(""), &stdout, &bytes.Buffer{}, testApplication()); code != int(install.ExitUsageError) {
		t.Fatalf("version extra: code = %d", code)
	}
}

func TestInstallArgumentErrorsComeBeforeLoadingThePrinterFile(t *testing.T) {
	// The file does not exist: if it were loaded during parsing, every one of
	// these would fail with "no such file" instead of naming the bad argument.
	missing := filepath.Join(t.TempDir(), "big.ssb")
	for _, tt := range []struct {
		args []string
		want string
	}{
		{[]string{"--profile", missing, "--bogus"}, "unknown flag --bogus"},
		{[]string{"--profile", missing, "192.0.2.10"}, "cannot be combined with a target"},
		{[]string{"--profile", missing, "--force-family", "test-family"}, "cannot be combined with a target or --force-family"},
		{[]string{"--profile", missing, "--profile", missing}, "--profile requires one file"},
		{[]string{"--profile", missing, "--name", "Q"}, "--name requires --windows-driver"},
		{[]string{"192.0.2.10", "--windows-driver", "--name", "Q", "--profile", missing}, "cannot combine"},
	} {
		_, err := parseInstallArgs(tt.args)
		if err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%v: err = %v, want %q", tt.args, err, tt.want)
		}
	}
	// Accepted arguments still load the file, in either order.
	for _, args := range [][]string{{"--profile", missing, "--dry-run"}, {"--dry-run", "--profile", missing}} {
		if _, err := parseInstallArgs(args); err == nil || !strings.Contains(err.Error(), "big.ssb") {
			t.Errorf("%v: err = %v, want the file's own error", args, err)
		}
	}
}
