package main

import (
	"bytes"
	"context"
	"github.com/spilloid/spoolsmith/internal/evidence"
	"github.com/spilloid/spoolsmith/internal/install"
	"github.com/spilloid/spoolsmith/internal/probe"
	"io"
	"path/filepath"
	"strings"
	"testing"
)

func TestOfflineCLIRequiresProfileAndRejectsMixedInputs(t *testing.T) {
	p := install.Profile{Version: 1, Target: "192.0.2.10", PrinterName: "Accounting", DriverName: "OEM Driver", Evidence: evidence.Evidence{Provenance: "captured", HTTPTitle: "Example"}}
	path := filepath.Join(t.TempDir(), "profile.json")
	if err := install.SaveProfile(path, p); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"--offline", "192.0.2.10"}, {"--offline"}, {"--offline", "--profile", path, "192.0.2.10"}, {"--offline", "--profile", path, "--force-family", "brother-hl-l2xxx"}, {"--profile", path, "--offline", "--offline"}} {
		if _, err := parseInstallArgs(args); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
	opts, err := parseInstallArgs([]string{"--profile", path, "--offline", "--yes", "--dry-run", "--json"})
	if err != nil || !opts.Offline || !opts.DryRun || !opts.NonInteractive || opts.Profile == nil {
		t.Fatalf("%+v %v", opts, err)
	}
	app := testApplication()
	app.workflow.Collect = func(context.Context, string) (probe.Result, error) {
		t.Fatal("offline CLI probed")
		return probe.Result{}, nil
	}
	// Missing local-inventory support fails before any mutation, even when
	// an adapter forgets to implement the post-install verification seam.
	var out bytes.Buffer
	code := run(context.Background(), []string{"add", "--profile", path, "--offline", "--yes"}, strings.NewReader(""), &out, io.Discard, app)
	if code != int(install.ExitPreflight) || len(app.environment.(*cliFakeEnvironment).ran) != 0 {
		t.Fatalf("code=%d output=%s", code, out.String())
	}
}
