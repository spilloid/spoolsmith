package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakePrepTool compiles a stand-in for Microsoft's IntuneWinAppUtil.exe that
// honors the real argument order (-c src -s setup -o out -q) and either writes
// <out>\install.intunewin or fails, so the CLI's autodetect, defaults and error
// paths run against a real child process.
func fakePrepTool(t *testing.T, dir string, succeed bool) string {
	t.Helper()
	flag := "false"
	if succeed {
		flag = "true"
	}
	body := `package main

import (
	"fmt"
	"os"
	"path/filepath"
)

func main() {
	var out, setup string
	for i, a := range os.Args {
		if a == "-o" {
			out = os.Args[i+1]
		}
		if a == "-s" {
			setup = os.Args[i+1]
		}
	}
	if setup != "install.ps1" || out == "" {
		fmt.Println("unexpected arguments", os.Args)
		os.Exit(2)
	}
	if !` + flag + ` {
		fmt.Println("tool exploded")
		os.Exit(3)
	}
	os.MkdirAll(out, 0o755)
	os.WriteFile(filepath.Join(out, "install.intunewin"), []byte("package"), 0o644)
}
`
	src := filepath.Join(t.TempDir(), "main.go")
	if err := os.WriteFile(src, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	tool := filepath.Join(dir, "IntuneWinAppUtil.exe")
	goBinary := filepath.Join(runtime.GOROOT(), "bin", "go")
	if runtime.GOOS == "windows" {
		goBinary += ".exe"
	}
	cmd := exec.Command(goBinary, "build", "-o", tool, src)
	cmd.Dir = filepath.Dir(src)
	cmd.Env = append(os.Environ(), "GO111MODULE=off", "CGO_ENABLED=0")
	if data, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build fake content prep tool: %v %s", err, data)
	}
	return tool
}

func TestIntuneContentPrepErgonomics(t *testing.T) {
	binaryPath := filepath.Join(t.TempDir(), "spoolsmith.exe")
	goBinary := filepath.Join(runtime.GOROOT(), "bin", "go")
	if runtime.GOOS == "windows" {
		goBinary += ".exe"
	}
	build := exec.Command(goBinary, "build", "-o", binaryPath, ".")
	build.Env = append(os.Environ(), "GOOS=windows", "GOARCH=amd64", "CGO_ENABLED=0")
	if data, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build test CLI: %v %s", err, data)
	}
	goodDir, badDir := filepath.Join(t.TempDir(), "good"), filepath.Join(t.TempDir(), "bad")
	goodTool := fakePrepTool(t, goodDir, true)
	fakePrepTool(t, badDir, false)
	emptyDir := t.TempDir()

	profileBytes, err := os.ReadFile("../../examples/intune/accounting.ssb")
	if err != nil {
		t.Fatal(err)
	}
	// Each case gets its own profile folder so export and package folders never collide.
	newProfile := func(t *testing.T) string {
		t.Helper()
		profile := filepath.Join(t.TempDir(), "printer.ssb")
		if err := os.WriteFile(profile, profileBytes, 0o600); err != nil {
			t.Fatal(err)
		}
		return profile
	}
	run := func(dirs []string, args []string, input string) (int, string) {
		var stdout, stderr bytes.Buffer
		app := testApplication()
		app.inputTerminal = true
		app.prepToolDirs = dirs
		code := runIntune(context.Background(), args, strings.NewReader(input), &stdout, &stderr, app)
		return code, stderr.String()
	}
	// exportOf finds the export folder and the package folder next to the profile.
	exportOf := func(t *testing.T, profile string) (export, pkg string) {
		t.Helper()
		entries, err := os.ReadDir(filepath.Dir(profile))
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			if e.IsDir() && strings.HasSuffix(e.Name(), "-intunewin") {
				pkg = filepath.Join(filepath.Dir(profile), e.Name())
			} else if e.IsDir() {
				export = filepath.Join(filepath.Dir(profile), e.Name())
			}
		}
		return export, pkg
	}
	base := func(profile string, extra ...string) []string {
		return append([]string{"build", "--profile", profile, "--binary", binaryPath, "--driver-prerequisite"}, extra...)
	}
	created := func(pkg string) bool {
		_, err := os.Stat(filepath.Join(pkg, "install.intunewin"))
		return err == nil
	}

	t.Run("tool beside the CLI is found and used with a default output", func(t *testing.T) {
		profile := newProfile(t)
		code, stderr := run([]string{emptyDir, goodDir}, base(profile), "")
		export, pkg := exportOf(t, profile)
		if code != 0 || export == "" || !created(pkg) || !strings.Contains(stderr, "Using "+goodTool) || !strings.Contains(stderr, "--no-content-prep") {
			t.Fatalf("%d %q export=%q pkg=%q", code, stderr, export, pkg)
		}
		if pkg != export+"-intunewin" {
			t.Fatalf("package folder %q is not the suggested sibling of %q", pkg, export)
		}
	})
	t.Run("no tool anywhere exports only and says how to get one", func(t *testing.T) {
		profile := newProfile(t)
		code, stderr := run([]string{emptyDir}, base(profile), "")
		export, pkg := exportOf(t, profile)
		if code != 0 || export == "" || pkg != "" || !strings.Contains(stderr, "IntuneWinAppUtil.exe beside spoolsmith.exe") {
			t.Fatalf("%d %q export=%q pkg=%q", code, stderr, export, pkg)
		}
	})
	t.Run("no-content-prep skips a tool that is present", func(t *testing.T) {
		profile := newProfile(t)
		code, stderr := run([]string{goodDir}, base(profile, "--no-content-prep"), "")
		export, pkg := exportOf(t, profile)
		if code != 0 || export == "" || pkg != "" || strings.Contains(stderr, "Using") {
			t.Fatalf("%d %q export=%q pkg=%q", code, stderr, export, pkg)
		}
	})
	t.Run("dry run reports the package folder and writes nothing", func(t *testing.T) {
		profile := newProfile(t)
		code, stderr := run([]string{goodDir}, base(profile, "--dry-run"), "")
		export, pkg := exportOf(t, profile)
		if code != 0 || export != "" || pkg != "" || !strings.Contains(stderr, "install.intunewin folder:") {
			t.Fatalf("%d %q export=%q pkg=%q", code, stderr, export, pkg)
		}
	})
	t.Run("either flag may be given alone", func(t *testing.T) {
		profile := newProfile(t)
		// Tool alone: nothing beside the CLI, output defaults.
		if code, stderr := run([]string{emptyDir}, base(profile, "--content-prep-tool", goodTool), ""); code != 0 {
			t.Fatalf("tool alone: %d %q", code, stderr)
		} else if _, pkg := exportOf(t, profile); !created(pkg) {
			t.Fatalf("tool alone created no package: %q", stderr)
		}
		// Output alone: the tool beside the CLI is used.
		profile2 := newProfile(t)
		out := filepath.Join(t.TempDir(), "elsewhere")
		if code, stderr := run([]string{goodDir}, base(profile2, "--content-prep-output", out), ""); code != 0 || !created(out) {
			t.Fatalf("output alone: %d %q", code, stderr)
		}
	})
	t.Run("misuse is refused before anything is exported", func(t *testing.T) {
		profile := newProfile(t)
		for name, tc := range map[string]struct {
			dirs  []string
			extra []string
		}{
			"output with no tool available": {[]string{emptyDir}, []string{"--content-prep-output", filepath.Join(t.TempDir(), "o")}},
			"opt-out plus tool":             {[]string{goodDir}, []string{"--no-content-prep", "--content-prep-tool", goodTool}},
			"missing tool file":             {[]string{emptyDir}, []string{"--content-prep-tool", filepath.Join(t.TempDir(), "nope.exe")}},
			"empty tool value":              {[]string{goodDir}, []string{"--content-prep-tool="}},
			"output inside the export":      {[]string{goodDir}, []string{"--output", filepath.Join(filepath.Dir(profile), "exp"), "--content-prep-output", filepath.Join(filepath.Dir(profile), "exp", "inner")}},
		} {
			if code, stderr := run(tc.dirs, base(profile, tc.extra...), ""); code != 2 {
				t.Fatalf("%s: code %d %q", name, code, stderr)
			}
		}
		if export, pkg := exportOf(t, profile); export != "" || pkg != "" {
			t.Fatalf("a refused request still exported %q %q", export, pkg)
		}
	})
	t.Run("a failing tool keeps the export and reports it", func(t *testing.T) {
		profile := newProfile(t)
		code, stderr := run([]string{badDir}, base(profile), "")
		export, pkg := exportOf(t, profile)
		if code != 1 || export == "" || pkg != "" || !strings.Contains(stderr, "bundle exported; content prep failed") || !strings.Contains(stderr, "tool exploded") {
			t.Fatalf("%d %q export=%q pkg=%q", code, stderr, export, pkg)
		}
		if _, err := os.Stat(filepath.Join(export, "deployment.json")); err != nil {
			t.Fatalf("export was not preserved: %v", err)
		}
	})
	t.Run("wizard uses the detected tool", func(t *testing.T) {
		profile := newProfile(t)
		code, stderr := run([]string{goodDir}, []string{"wizard"}, profile+"\n"+binaryPath+"\nyes\n\nexport\n")
		export, pkg := exportOf(t, profile)
		if code != 0 || export == "" || !created(pkg) || !strings.Contains(stderr, "Then create install.intunewin") {
			t.Fatalf("%d %q export=%q pkg=%q", code, stderr, export, pkg)
		}
	})
	t.Run("wizard without a tool tips the user and still exports", func(t *testing.T) {
		profile := newProfile(t)
		code, stderr := run([]string{emptyDir}, []string{"wizard"}, profile+"\n"+binaryPath+"\nyes\n\nexport\n")
		export, pkg := exportOf(t, profile)
		if code != 0 || export == "" || pkg != "" || !strings.Contains(stderr, "Tip: put IntuneWinAppUtil.exe beside spoolsmith.exe") {
			t.Fatalf("%d %q export=%q pkg=%q", code, stderr, export, pkg)
		}
	})
	t.Run("wizard advanced settings can decline a detected tool", func(t *testing.T) {
		profile := newProfile(t)
		exportDir := filepath.Join(t.TempDir(), "custom")
		input := strings.Join([]string{profile, binaryPath, "yes", "yes", "", "", "", "", "", "", "", "", exportDir, "none", "export", ""}, "\n")
		code, stderr := run([]string{goodDir}, []string{"wizard"}, input)
		if code != 0 || created(exportDir+"-intunewin") || !strings.Contains(stderr, "Tip:") {
			t.Fatalf("%d %q", code, stderr)
		}
	})
}
