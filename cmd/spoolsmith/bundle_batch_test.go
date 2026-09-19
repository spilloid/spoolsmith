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

	"github.com/spilloid/spoolsmith/internal/install"
	"github.com/spilloid/spoolsmith/internal/probe"
)

func TestCopyAllPartialSuccessKeepsJSONContractAndExplainsNextSteps(t *testing.T) {
	app, env := bundleTestApplication(t)
	queues := sampleQueues()
	warehouse := queues[0]
	warehouse.PrinterName = "Warehouse"
	queues = append(queues, warehouse)
	all := &allQueuesFakeEnvironment{bundleFakeEnvironment: env, queues: queues}
	app.environment = all
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Office.ssb"), []byte("existing bundle"), 0600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"copy", "--all", dir}, strings.NewReader(""), &stdout, &stderr, app)
	if code != 0 {
		t.Fatalf("copy --all code = %d; stderr = %s", code, stderr.String())
	}
	var result copyAllResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Requested != 3 || result.Written != 1 || result.Skipped != 1 || result.Failed != 1 || len(result.Queues) != 3 {
		t.Fatalf("result = %+v", result)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(stdout.Bytes(), &fields); err != nil {
		t.Fatal(err)
	}
	if len(fields) != 6 {
		t.Fatalf("JSON schema changed: %s", stdout.String())
	}
	for _, key := range []string{"output_dir", "requested", "written", "skipped", "failed", "queues"} {
		if _, ok := fields[key]; !ok {
			t.Fatalf("JSON missing %q: %s", key, stdout.String())
		}
	}
	if !slices.Equal(all.requestedPrinters, []string{"Warehouse"}) || len(env.ran) != 0 {
		t.Fatalf("lookups = %v; Windows mutations = %v", all.requestedPrinters, env.ran)
	}
	for _, want := range []string{"explicit, unused bundle filename", "apply <bundle-file> --dry-run", "must already have these drivers", "--include-driver into a new folder"} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("missing %q in stderr: %s", want, stderr.String())
		}
	}
}

// TestCopyAllOutputDirFailureStillKeepsJSONContract guards against a
// refactor regression where a failure setting up the output directory
// (after queues are already known) discarded CreateAll's populated
// AllResult in favor of the generic {command,status,error} shape,
// breaking the documented 6-field schema this command's own docs and the
// GUI both rely on.
func TestCopyAllOutputDirFailureStillKeepsJSONContract(t *testing.T) {
	app, env := bundleTestApplication(t)
	all := &allQueuesFakeEnvironment{bundleFakeEnvironment: env, queues: sampleQueues()}
	app.environment = all
	blocked := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocked, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"copy", "--all", blocked}, strings.NewReader(""), &stdout, &stderr, app)
	if code != int(install.ExitGeneralError) {
		t.Fatalf("copy --all code = %d; stderr = %s", code, stderr.String())
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(stdout.Bytes(), &fields); err != nil {
		t.Fatalf("stdout was not the documented AllResult schema: %v; stdout = %s", err, stdout.String())
	}
	if len(fields) != 6 {
		t.Fatalf("JSON schema changed: %s", stdout.String())
	}
	var result copyAllResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Requested != len(sampleQueues()) || result.Failed != len(sampleQueues()) || result.Written != 0 || len(result.Queues) != len(sampleQueues()) {
		t.Fatalf("result = %+v", result)
	}
	for _, outcome := range result.Queues {
		if outcome.Status != "error" || !strings.Contains(outcome.Reason, "not copied:") {
			t.Fatalf("outcome = %+v", outcome)
		}
	}
}

func TestCopyAllCancellationReturnsPartialJSONAndStopsFurtherCopies(t *testing.T) {
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
	dir := t.TempDir()
	var stdout, stderr bytes.Buffer
	code := run(ctx, []string{"copy", "--all", dir}, strings.NewReader(""), &stdout, &stderr, app)
	if code != int(install.ExitGeneralError) {
		t.Fatalf("canceled copy --all code = %d; stderr = %s", code, stderr.String())
	}
	var result copyAllResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Requested != 3 || result.Written != 1 || result.Failed != 2 || len(result.Queues) != 3 || result.Queues[0].Status != "written" {
		t.Fatalf("cancellation lost partial results: %+v", result)
	}
	if !slices.Equal(all.requestedPrinters, []string{"Office", "Warehouse"}) || probes != 2 || len(env.ran) != 0 {
		t.Fatalf("lookups = %v; probes = %d; Windows mutations = %v", all.requestedPrinters, probes, env.ran)
	}
	for _, outcome := range result.Queues[1:] {
		if outcome.Status != "error" || !strings.Contains(outcome.Reason, "context canceled") {
			t.Fatalf("canceled outcome = %+v", outcome)
		}
	}
	files, err := os.ReadDir(dir)
	if err != nil || len(files) != 1 || files[0].Name() != "Office.ssb" {
		t.Fatalf("files = %v, error = %v", files, err)
	}
}
