package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/spilloid/spoolsmith/internal/intune"
)

func TestIntunePackagingUX(t *testing.T) {
	binaryPath := filepath.Join(t.TempDir(), "spoolsmith.exe")
	goBinary := filepath.Join(runtime.GOROOT(), "bin", "go")
	if runtime.GOOS == "windows" {
		goBinary += ".exe"
	}
	cmd := exec.Command(goBinary, "build", "-o", binaryPath, ".")
	cmd.Env = append(os.Environ(), "GOOS=windows", "GOARCH=amd64", "CGO_ENABLED=0")
	if data, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build test CLI: %v %s", err, data)
	}
	profileBytes, err := os.ReadFile("../../examples/intune/accounting.json")
	if err != nil {
		t.Fatal(err)
	}
	fixture := func(t *testing.T) (string, string) {
		t.Helper()
		profile := filepath.Join(t.TempDir(), "printer.json")
		if err := os.WriteFile(profile, profileBytes, 0600); err != nil {
			t.Fatal(err)
		}
		defaults, err := intune.ProfileDefaults(profile)
		if err != nil {
			t.Fatal(err)
		}
		output, err := intune.SuggestOutput(filepath.Dir(profile), defaults.ID, 1)
		if err != nil {
			t.Fatal(err)
		}
		return profile, output
	}
	runCommand := func(args []string, input string) (int, string, string) {
		var stdout, stderr bytes.Buffer
		app := testApplication()
		app.inputTerminal = true
		code := runIntune(context.Background(), args, strings.NewReader(input), &stdout, &stderr, app)
		return code, stdout.String(), stderr.String()
	}
	t.Run("build defaults preview and export", func(t *testing.T) {
		profile, output := fixture(t)
		args := []string{"build", "--profile", profile, "--binary", binaryPath, "--driver-prerequisite"}
		code, stdout, stderr := runCommand(append(args, "--dry-run"), "")
		if code != 0 || !strings.Contains(stderr, output) {
			t.Fatalf("preview: %d %s %s", code, stdout, stderr)
		}
		var m intune.Manifest
		if err := json.Unmarshal([]byte(stdout), &m); err != nil {
			t.Fatal(err)
		}
		pin, err := intune.HashBinary(binaryPath)
		if err != nil || m.BinarySHA256 != pin || m.Revision != 1 || m.DisplayName != "Example — Accounting Copier" || m.Offline || m.Adopt {
			t.Fatalf("incorrect default manifest: %+v %v", m, err)
		}
		if _, err := os.Stat(output); !os.IsNotExist(err) {
			t.Fatal("preview created output")
		}
		code, stdout, stderr = runCommand(args, "")
		if code != 0 {
			t.Fatalf("export: %d %s %s", code, stdout, stderr)
		}
		if _, err := os.Stat(filepath.Join(output, "deployment.json")); err != nil {
			t.Fatal(err)
		}
		// A repeated export suggests a new folder, preserving the first bundle.
		code, stdout, stderr = runCommand(args, "")
		if code != 0 || !strings.Contains(stderr, output+"-2") {
			t.Fatalf("repeat export: %d %s %s", code, stdout, stderr)
		}
	})
	t.Run("build overrides and invalid explicit values", func(t *testing.T) {
		profile, _ := fixture(t)
		args := []string{"build", "--profile", profile, "--binary", binaryPath, "--driver-prerequisite", "--dry-run"}
		code, stdout, stderr := runCommand(append(args, "--id", "existing-deployment", "--revision", "3", "--name", "Custom name", "--description=", "--location", "West", "--offline", "--adopt"), "")
		var m intune.Manifest
		if err := json.Unmarshal([]byte(stdout), &m); err != nil || code != 0 || m.ID != "existing-deployment" || m.Revision != 3 || m.DisplayName != "Custom name" || m.Description != "" || m.Location != "West" || !m.Offline || !m.Adopt {
			t.Fatalf("overrides: %d %s %s %v", code, stdout, stderr, err)
		}
		for _, extra := range [][]string{{"--revision=0"}, {"--id="}, {"--name="}, {"--binary-sha256=" + strings.Repeat("0", 64)}, {"--output="}, {"--driver-prerequisite=false"}} {
			if code, stdout, stderr := runCommand(append(args, extra...), ""); code != 2 {
				t.Fatalf("accepted %v: %d %s %s", extra, code, stdout, stderr)
			}
		}
	})
	for _, confirmation := range []string{"export\n", "no\n", ""} {
		t.Run("wizard confirmation "+strings.TrimSpace(confirmation), func(t *testing.T) {
			profile, output := fixture(t)
			input := profile + "\n" + binaryPath + "\nyes\n\n" + confirmation
			code, stdout, stderr := runCommand([]string{"wizard"}, input)
			wantCode := 5
			if confirmation == "export\n" {
				wantCode = 0
			}
			if code != wantCode || !strings.Contains(stderr, "Step 2 of 2") || !strings.Contains(stderr, "binary_sha256") || !strings.Contains(stderr, output) {
				t.Fatalf("wizard: %d %s %s", code, stdout, stderr)
			}
			_, err := os.Stat(filepath.Join(output, "deployment.json"))
			if wantCode == 0 && err != nil || wantCode != 0 && !os.IsNotExist(err) {
				t.Fatalf("unexpected export state: %v", err)
			}
		})
	}
	t.Run("wizard advanced settings", func(t *testing.T) {
		profile, _ := fixture(t)
		output := filepath.Join(t.TempDir(), "custom")
		input := strings.Join([]string{profile, binaryPath, "yes", "yes", "existing-id", "2", "Custom", "West", "Description", "", "offline", "yes", output, "export", ""}, "\n")
		code, stdout, stderr := runCommand([]string{"wizard"}, input)
		var m intune.Manifest
		if err := json.Unmarshal([]byte(stdout), &m); err != nil || code != 0 || m.ID != "existing-id" || m.Revision != 2 || !m.Offline || !m.Adopt || m.DisplayName != "Custom" {
			t.Fatalf("wizard overrides: %d %s %s %v", code, stdout, stderr, err)
		}
	})
}
