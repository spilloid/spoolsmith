package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/spilloid/spoolsmith/internal/bundle"
	"github.com/spilloid/spoolsmith/internal/install"
	"github.com/spilloid/spoolsmith/internal/probe"
)

func TestCopyAllPartialSuccessKeepsJSONContractAndExplainsNextSteps(t *testing.T) {
	app, env := bundleTestApplication(t)
	env.elevated = false // drivers fall back to settings-only, and say so
	queues := sampleQueues()
	warehouse := queues[0]
	warehouse.PrinterName = "Warehouse"
	queues = append(queues, warehouse)
	all := &allQueuesFakeEnvironment{bundleFakeEnvironment: env, queues: queues}
	app.environment = all
	setPath := filepath.Join(t.TempDir(), "printers.zip")
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"copy", "--all", "--out", setPath}, strings.NewReader(""), &stdout, &stderr, app)
	if code != 0 {
		t.Fatalf("copy --all code = %d; stderr = %s", code, stderr.String())
	}
	var result copyAllResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Requested != 3 || result.Written != 2 || result.Skipped != 1 || result.Failed != 0 || len(result.Queues) != 3 || result.SetPath != setPath {
		t.Fatalf("result = %+v", result)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(stdout.Bytes(), &fields); err != nil {
		t.Fatal(err)
	}
	if len(fields) != 6 {
		t.Fatalf("JSON schema changed: %s", stdout.String())
	}
	for _, key := range []string{"set_path", "requested", "written", "skipped", "failed", "queues"} {
		if _, ok := fields[key]; !ok {
			t.Fatalf("JSON missing %q: %s", key, stdout.String())
		}
	}
	if !slices.Equal(all.requestedPrinters, []string{"Office", "Warehouse"}) || len(env.ran) != 0 {
		t.Fatalf("lookups = %v; Windows mutations = %v", all.requestedPrinters, env.ran)
	}
	for _, want := range []string{"Added Office as Office.ssb (settings only)", "administrator rights", "apply printers.zip --dry-run", "Saved 2 of 3 printers"} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("missing %q in stderr: %s", want, stderr.String())
		}
	}
	if isSet, err := bundle.IsSet(setPath); err != nil || !isSet {
		t.Fatalf("output is not a printer set: %v %v", isSet, err)
	}
}

func TestCopyAllRefusesAnExistingOrNonZipDestination(t *testing.T) {
	app, env := bundleTestApplication(t)
	all := &allQueuesFakeEnvironment{bundleFakeEnvironment: env, queues: sampleQueues()}
	app.environment = all
	existing := filepath.Join(t.TempDir(), "printers.zip")
	if err := os.WriteFile(existing, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{existing, t.TempDir()} {
		var stdout, stderr bytes.Buffer
		code := run(context.Background(), []string{"copy", "--all", target}, strings.NewReader(""), &stdout, &stderr, app)
		if code != int(install.ExitGeneralError) || !json.Valid(stdout.Bytes()) {
			t.Fatalf("copy --all %s code = %d; stderr = %s", target, code, stderr.String())
		}
	}
	if len(all.requestedPrinters) != 0 {
		t.Fatalf("copied queues before refusing the destination: %v", all.requestedPrinters)
	}
	if data, _ := os.ReadFile(existing); string(data) != "keep" {
		t.Fatal("existing file changed")
	}
}

func TestCopyAllCancellationReturnsPartialJSONAndWritesNoSet(t *testing.T) {
	app, env := bundleTestApplication(t)
	queues := make([]install.InstalledQueue, 3)
	for i, name := range []string{"Office", "Warehouse", "Lobby"} {
		queues[i] = sampleQueues()[0]
		queues[i].PrinterName = name
	}
	all := &allQueuesFakeEnvironment{bundleFakeEnvironment: env, queues: queues}
	app.environment = all
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	collect := app.collect
	probes := 0
	app.collect = func(ctx context.Context, address string) (probe.Result, error) {
		probes++
		if probes == 2 {
			cancel()
		}
		return collect(ctx, address)
	}
	setPath := filepath.Join(t.TempDir(), "printers.zip")
	var stdout, stderr bytes.Buffer
	code := run(ctx, []string{"copy", "--all", setPath}, strings.NewReader(""), &stdout, &stderr, app)
	if code != int(install.ExitGeneralError) {
		t.Fatalf("canceled copy --all code = %d; stderr = %s", code, stderr.String())
	}
	var result copyAllResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Requested != 3 || result.Written != 0 || result.Failed != 3 || len(result.Queues) != 3 || result.SetPath != "" {
		t.Fatalf("cancellation lost accounting: %+v", result)
	}
	if !slices.Equal(all.requestedPrinters, []string{"Office", "Warehouse"}) || probes != 2 || len(env.ran) != 0 {
		t.Fatalf("lookups = %v; probes = %d; Windows mutations = %v", all.requestedPrinters, probes, env.ran)
	}
	if !strings.Contains(result.Queues[0].Reason, "not saved") {
		t.Fatalf("copied-but-unsaved queue = %+v", result.Queues[0])
	}
	for _, outcome := range result.Queues[1:] {
		if outcome.Status != "error" || !strings.Contains(outcome.Reason, "context canceled") {
			t.Fatalf("canceled outcome = %+v", outcome)
		}
	}
	if _, err := os.Stat(setPath); !os.IsNotExist(err) {
		t.Fatalf("canceled copy wrote a set: %v", err)
	}
}
