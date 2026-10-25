package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spilloid/spoolsmith/internal/actionlog"
)

func TestErrorCapturePassesStderrThroughAndKeepsTheReason(t *testing.T) {
	var console bytes.Buffer
	capture := newErrorCapture(&console)
	for _, chunk := range []string{"Install plan\n  Queue: X\n", "spoolsmith apply: install: step 2 of 2 failed: The spooler ", "service is not reachable.\nPrinter set summary\n"} {
		if _, err := capture.Write([]byte(chunk)); err != nil {
			t.Fatal(err)
		}
	}
	if !strings.Contains(console.String(), "Install plan") || !strings.Contains(console.String(), "Printer set summary") {
		t.Fatalf("stderr was not passed through: %q", console.String())
	}
	if got, want := capture.Error(), "spoolsmith apply: install: step 2 of 2 failed: The spooler service is not reachable."; got != want {
		t.Fatalf("captured %q, want %q", got, want)
	}
	if got := newErrorCapture(&console).Error(); got != "" {
		t.Fatalf("no error written but captured %q", got)
	}
}

// The CLI's action-log entry for a failed run must say why it failed. The
// desktop always recorded the error text; the CLI recorded only the exit code.
func TestCLIActionLogRecordsTheErrorText(t *testing.T) {
	path := filepath.Join(t.TempDir(), "actions.log")
	logger, err := actionlog.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	capture := newErrorCapture(&stderr)
	code := run(t.Context(), []string{"apply", filepath.Join(t.TempDir(), "missing.ssb")}, strings.NewReader(""), &stdout, capture, application{})
	if code == 0 {
		t.Fatal("applying a missing file succeeded")
	}
	recordRun(logger, []string{"apply", "missing.ssb"}, code, time.Second, capture.Error())
	logger.Close()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var entry actionlog.Entry
	if err := json.Unmarshal(bytes.TrimSpace(data), &entry); err != nil {
		t.Fatal(err)
	}
	if entry.Status != "error" || entry.ExitCode == nil || *entry.ExitCode != code || !strings.Contains(entry.Error, "missing.ssb") {
		t.Fatalf("action-log entry lost the failure reason: %+v (stderr %q)", entry, stderr.String())
	}
}
