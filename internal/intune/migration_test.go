package intune

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestPreparePrinterPayloadReproducible(t *testing.T) {
	options := testOptions(t)
	first, err := Prepare(options)
	if err != nil {
		t.Fatal(err)
	}
	// The archive writer's implicit timestamp has second precision.
	time.Sleep(1100 * time.Millisecond)
	second, err := Prepare(options)
	if err != nil {
		t.Fatal(err)
	}
	if first.Manifest.Format != 2 || second.Manifest.Format != 2 {
		t.Fatal("new endpoint packages must use format 2")
	}
	if !bytes.Equal(first.files["profile.ssb"], second.files["profile.ssb"]) || first.Manifest.ProfileSHA256 != second.Manifest.ProfileSHA256 {
		t.Fatal("identical inputs changed generated profile bytes or hash")
	}
}

func TestRuntimeRetainedRevisionProfileCompatibility(t *testing.T) {
	shell := powershell(t)
	root := t.TempDir()
	helper := filepath.Join(root, "helper.go")
	source := `package main
import("encoding/json";"os")
func main(){json.NewEncoder(os.Stdout).Encode(map[string]any{"argv":os.Args[1:]})}`
	mustWrite := func(path string, data []byte) {
		t.Helper()
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite(helper, []byte(source))
	goBinary := filepath.Join(runtime.GOROOT(), "bin", "go")
	if runtime.GOOS == "windows" {
		goBinary += ".exe"
	}
	binary := filepath.Join(root, "helper.exe")
	if out, err := exec.Command(goBinary, "build", "-o", binary, helper).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	binaryBytes, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	script, err := templates.ReadFile("templates/runtime.ps1")
	if err != nil {
		t.Fatal(err)
	}
	runtimePath := filepath.Join(root, "runtime.ps1")
	mustWrite(runtimePath, script)
	for _, format := range []int{1, 2} {
		t.Run(fmt.Sprintf("format%d", format), func(t *testing.T) {
			dir := filepath.Join(root, fmt.Sprintf("revision %d", format))
			if err := os.Mkdir(dir, 0700); err != nil {
				t.Fatal(err)
			}
			name := "profile.json"
			if format == 2 {
				name = "profile.ssb"
			}
			// The fake CLI echoes argv; real profile parsing belongs to each retained CLI.
			profile := []byte("pinned revision profile")
			mustWrite(filepath.Join(dir, name), profile)
			// Executable on every OS: the runtime starts it as the retained CLI.
			if err := os.WriteFile(filepath.Join(dir, "spoolsmith.exe"), binaryBytes, 0700); err != nil {
				t.Fatal(err)
			}
			manifest := Manifest{Format: format, BinarySHA256: digest(binaryBytes), ProfileSHA256: digest(profile)}
			data, err := json.Marshal(manifest)
			if err != nil {
				t.Fatal(err)
			}
			mustWrite(filepath.Join(dir, "deployment.json"), data)
			for _, operation := range []string{"status", "remove", "configure"} {
				command := ". " + psString(runtimePath) + "; Invoke-SpoolSmith " + psString(dir) + " " + psString(operation) + " $false " + psString(dir) + " | ConvertTo-Json -Depth 10"
				cmd := exec.Command(shell, "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", command)
				cmd.Env = shellEnv()
				out, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("%s: %v %s", operation, err, out)
				}
				var result struct {
					Code *int
					Data struct{ Argv []string }
				}
				if err := json.Unmarshal(out, &result); err != nil {
					t.Fatalf("%v %s", err, out)
				}
				argv := result.Data.Argv
				if result.Code == nil || *result.Code != 0 || len(argv) < 3 || argv[0] != operation || argv[1] != "--profile" || argv[2] != filepath.Join(dir, name) {
					t.Fatalf("%s format%d: %s", operation, format, out)
				}
			}
			// A retained revision must still enforce the original pinned profile bytes.
			mustWrite(filepath.Join(dir, name), []byte("tampered"))
			command := ". " + psString(runtimePath) + "; try { Assert-Payload " + psString(dir) + " (Read-JSON " + psString(filepath.Join(dir, "deployment.json")) + "); exit 9 } catch { if ($_.Exception.Message -like 'Payload hash mismatch:*') { exit 0 }; throw }"
			cmd := exec.Command(shell, "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", command)
			cmd.Env = shellEnv()
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("tamper check: %v %s", err, out)
			}
		})
	}
}
