package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spilloid/spoolsmith/internal/bundle"
	"github.com/spilloid/spoolsmith/internal/evidence"
	"github.com/spilloid/spoolsmith/internal/install"
	"github.com/spilloid/spoolsmith/internal/profileset"
)

func TestProfileTransferDryRunAndExecutionContracts(t *testing.T) {
	source := t.TempDir()
	p := install.Profile{Version: 1, Target: "192.0.2.10", PrinterName: "Office", DriverName: "Test driver", Evidence: evidence.Evidence{IP: "192.0.2.10", Provenance: "captured", HTTPTitle: "Test printer"}}
	if err := bundle.SaveProfile(filepath.Join(source, "office.ssb"), p); err != nil {
		t.Fatal(err)
	}
	collection := filepath.Join(t.TempDir(), "all.ssb")
	dest := filepath.Join(t.TempDir(), "new-folder")
	for _, step := range []struct{ operation, source, destination string }{
		{"export-all", source, collection},
		{"import-all", collection, dest},
	} {
		var stdout, stderr bytes.Buffer
		args := []string{"profile", step.operation, step.source, step.destination, "--dry-run"}
		if code := run(context.Background(), args, strings.NewReader(""), &stdout, &stderr, testApplication()); code != 0 {
			t.Fatalf("preview %v: %d %s", args, code, stderr.String())
		}
		var preview profileset.Preview
		if err := json.Unmarshal(stdout.Bytes(), &preview); err != nil {
			t.Fatalf("stdout must remain JSON: %v: %s", err, stdout.String())
		}
		if preview.Count != 1 || preview.Destination != step.destination || preview.Profiles[0].PrinterName != p.PrinterName || len(preview.Conflicts) != 0 {
			t.Fatalf("incomplete preview: %#v", preview)
		}
		if _, err := os.Stat(step.destination); !os.IsNotExist(err) {
			t.Fatalf("dry-run wrote files: %v", err)
		}
		stdout.Reset()
		stderr.Reset()
		if code := run(context.Background(), args[:len(args)-1], strings.NewReader(""), &stdout, &stderr, testApplication()); code != 0 {
			t.Fatalf("execute %v: %d %s", args, code, stderr.String())
		}
		var result map[string]any
		if err := json.Unmarshal(stdout.Bytes(), &result); err != nil || len(result) != 2 || result["count"] != float64(1) || result["destination"] != step.destination {
			t.Fatalf("execution JSON contract changed: %s %v", stdout.String(), err)
		}
		for _, text := range []string{"no printers are installed", "copy the archive too"} {
			if !strings.Contains(stderr.String(), text) {
				t.Fatalf("missing completion guidance %q: %s", text, stderr.String())
			}
		}
	}
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), []string{"profile", "import-all", collection, dest, "--dry-run"}, strings.NewReader(""), &stdout, &stderr, testApplication()); code != 1 {
		t.Fatalf("conflicting preview should fail: %d %s", code, stderr.String())
	}
	var preview profileset.Preview
	if err := json.Unmarshal(stdout.Bytes(), &preview); err != nil || len(preview.Conflicts) != 1 || preview.Conflicts[0] != "office.ssb" {
		t.Fatalf("missing structured conflicts: %#v %v", preview, err)
	}
}

func TestProfileTransferRejectsUnknownAndDuplicateOptionsBeforeWriting(t *testing.T) {
	for _, options := range [][]string{{"--dry-run", "--dry-run"}, {"--yes"}} {
		destination := filepath.Join(t.TempDir(), "not-created")
		args := append([]string{"profile", "import-all", "missing.ssb", destination}, options...)
		var stdout, stderr bytes.Buffer
		if code := run(context.Background(), args, strings.NewReader(""), &stdout, &stderr, testApplication()); code != 2 || !json.Valid(stdout.Bytes()) {
			t.Fatalf("expected JSON usage failure: %d %s %s", code, stdout.String(), stderr.String())
		}
		if _, err := os.Stat(destination); !os.IsNotExist(err) {
			t.Fatalf("created destination: %v", err)
		}
	}
}
