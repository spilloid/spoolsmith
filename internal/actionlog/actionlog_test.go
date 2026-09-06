package actionlog

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenRecordAppendsJSONLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "spoolsmith", "actions.log")
	logger, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer logger.Close()

	code := 0
	if err := logger.Record(Entry{Source: "cli", Op: "inspect", Args: []string{"192.168.1.5"}, Status: "success", ExitCode: &code}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	if err := logger.Record(Entry{Source: "gui", Op: "install", Status: "not-confirmed"}); err != nil {
		t.Fatalf("Record: %v", err)
	}

	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open written log: %v", err)
	}
	defer file.Close()

	var lines []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d: %v", len(lines), lines)
	}

	var first Entry
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatalf("unmarshal first line: %v", err)
	}
	if first.Op != "inspect" || first.Source != "cli" || first.Status != "success" || first.ExitCode == nil || *first.ExitCode != 0 {
		t.Fatalf("unexpected first entry: %+v", first)
	}
	if first.Time.IsZero() {
		t.Fatal("expected Record to stamp a timestamp")
	}

	var second Entry
	if err := json.Unmarshal([]byte(lines[1]), &second); err != nil {
		t.Fatalf("unmarshal second line: %v", err)
	}
	if second.Op != "install" || second.Source != "gui" || second.Status != "not-confirmed" {
		t.Fatalf("unexpected second entry: %+v", second)
	}
}

func TestOpenCreatesParentDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "dir", "actions.log")
	logger, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer logger.Close()
	if _, err := os.Stat(filepath.Dir(path)); err != nil {
		t.Fatalf("expected parent directory to exist: %v", err)
	}
}

func TestRotateIfLargeRotatesPastThreshold(t *testing.T) {
	path := filepath.Join(t.TempDir(), "actions.log")
	oversized := strings.Repeat("x", maxSizeBytes+1)
	if err := os.WriteFile(path, []byte(oversized), 0o644); err != nil {
		t.Fatalf("seed oversized file: %v", err)
	}

	logger, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer logger.Close()

	if _, err := os.Stat(path + ".1"); err != nil {
		t.Fatalf("expected rotated file %s.1: %v", path, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat fresh log: %v", err)
	}
	if info.Size() != 0 {
		t.Fatalf("expected fresh log after rotation, got size %d", info.Size())
	}
}

func TestNilLoggerRecordIsNoop(t *testing.T) {
	var logger *Logger
	if err := logger.Record(Entry{Op: "noop"}); err != nil {
		t.Fatalf("expected nil-safe Record, got %v", err)
	}
	if err := logger.Close(); err != nil {
		t.Fatalf("expected nil-safe Close, got %v", err)
	}
}

func TestNoopLoggerRecordIsNoop(t *testing.T) {
	if err := noopLogger.Record(Entry{Op: "noop"}); err != nil {
		t.Fatalf("expected no-op Record, got %v", err)
	}
}

func TestDefaultReturnsUsableLogger(t *testing.T) {
	logger := Default()
	if logger == nil {
		t.Fatal("Default returned nil")
	}
	if err := logger.Record(Entry{Source: "cli", Op: "test", Status: "success"}); err != nil {
		t.Fatalf("Record via Default: %v", err)
	}
}
