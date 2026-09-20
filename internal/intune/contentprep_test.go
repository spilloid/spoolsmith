package intune

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestFindContentPrepToolOnlyLooksBesideGivenDirectories(t *testing.T) {
	beside, elsewhere := t.TempDir(), t.TempDir()
	if got := FindContentPrepTool(beside, elsewhere); got != "" {
		t.Fatalf("found %q in empty directories", got)
	}
	tool := filepath.Join(elsewhere, ContentPrepToolName)
	if err := os.WriteFile(tool, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := FindContentPrepTool("", beside, elsewhere); got != tool {
		t.Fatalf("got %q, want %q", got, tool)
	}
	// A directory that merely has the tool's name is not a tool.
	dirNamed := t.TempDir()
	if err := os.Mkdir(filepath.Join(dirNamed, ContentPrepToolName), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := FindContentPrepTool(dirNamed); got != "" {
		t.Fatalf("directory accepted as tool: %q", got)
	}
}

func TestSuggestPrepOutputIsOutsideExportFolder(t *testing.T) {
	export := filepath.Join(t.TempDir(), "hp-lobby-r1")
	out := SuggestPrepOutput(export)
	if out == export || strings.HasPrefix(out, export+string(filepath.Separator)) {
		t.Fatalf("suggested output %q is not outside %q", out, export)
	}
	if SuggestPrepOutput("") != "" {
		t.Fatal("empty export folder must not produce a suggestion")
	}
	// The suggestion must actually be accepted by PrepareContent's outside-source rule.
	if _, err := PrepareContent(context.Background(), filepath.Join(t.TempDir(), "missing.exe"), export, out); err != nil && strings.Contains(err.Error(), "outside") {
		t.Fatalf("suggestion rejected as inside source: %v", err)
	}
}

// fakeTool writes a stand-in for IntuneWinAppUtil.exe: a batch file that
// receives Microsoft's real argument order (-c src -s setup -o out -q).
func fakeTool(t *testing.T, body string) string {
	t.Helper()
	if runtime.GOOS != "windows" {
		t.Skip("batch-file stand-in for IntuneWinAppUtil.exe is Windows-only")
	}
	path := filepath.Join(t.TempDir(), "fake-prep.cmd")
	if err := os.WriteFile(path, []byte("@echo off\r\n"+body+"\r\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestPrepareContentReportsCreatedPackage(t *testing.T) {
	source := t.TempDir()
	output := filepath.Join(t.TempDir(), "out")
	tool := fakeTool(t, `mkdir "%~6" && echo package> "%~6\install.intunewin"`)
	if _, err := PrepareContent(context.Background(), tool, source, output); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(output, PreparedPackageName)); err != nil {
		t.Fatal(err)
	}
}

func TestPrepareContentFailsWhenToolProducesNothing(t *testing.T) {
	source := t.TempDir()
	output := filepath.Join(t.TempDir(), "out")
	tool := fakeTool(t, `exit /b 0`)
	_, err := PrepareContent(context.Background(), tool, source, output)
	if err == nil || !strings.Contains(err.Error(), PreparedPackageName) {
		t.Fatalf("exit-0 tool with no package was accepted: %v", err)
	}
}

func TestPrepareContentSurfacesToolFailureAndOutput(t *testing.T) {
	source := t.TempDir()
	output := filepath.Join(t.TempDir(), "out")
	tool := fakeTool(t, `echo boom-from-tool & exit /b 3`)
	text, err := PrepareContent(context.Background(), tool, source, output)
	if err == nil || !strings.Contains(text, "boom-from-tool") {
		t.Fatalf("output %q, err %v", text, err)
	}
}
