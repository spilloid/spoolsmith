package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/spilloid/spoolsmith/internal/install"
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
