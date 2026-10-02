//go:build windows

package bundle

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spilloid/spoolsmith/internal/install"
	"github.com/spilloid/spoolsmith/internal/probe"
)

// The import check is a dry run. Treat privilege preflight as satisfied so a
// normal test process can exercise the complete preview without Windows writes.
type dryRunElevatedEnvironment struct{ install.Environment }

func (dryRunElevatedEnvironment) IsElevated(context.Context) (bool, error) { return true, nil }

func (dryRunElevatedEnvironment) Run(context.Context, string) (string, error) {
	panic("dry-run preview must not execute a printer command")
}

// Explicitly gated because it reads an installed WSD queue and probes a real
// printer. No printer or port is changed by this test.
func TestHardwareWSDCopyAndImportPreview(t *testing.T) {
	queue := os.Getenv("SPOOLSMITH_TEST_WSD_QUEUE")
	if queue == "" {
		t.Skip("set SPOOLSMITH_TEST_WSD_QUEUE to a real WSD queue")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	env := install.NewEnvironment()
	path := filepath.Join(t.TempDir(), "wsd-converted.ssb")
	created, err := Create(ctx, env, probe.Collect, CreateOptions{QueueName: queue, Path: path, SettingsOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if created.Manifest.Profile.Target == "" || !strings.Contains(created.Warning, "Windows WSD port") {
		t.Fatalf("WSD bundle missing IP or warning: %+v", created)
	}
	opened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	profile := opened.Manifest.Profile
	if err := profile.Validate(); err != nil {
		t.Fatal(err)
	}
	var transcript bytes.Buffer
	outcome, code := install.NewWorkflow().RunInstall(ctx, dryRunElevatedEnvironment{env}, strings.NewReader(""), &transcript, false, install.InstallOptions{Profile: &profile, DryRun: true, UpdateExisting: true})
	if code != install.ExitSuccess || outcome.Plan == nil || outcome.Plan.IPAddress != profile.Target {
		t.Fatalf("WSD import preview: code %d, outcome %+v, transcript %s", code, outcome, transcript.String())
	}
	if profile.PortType == "ipp" {
		if !outcome.Plan.IPP || outcome.Plan.IPPURL != profile.IPPURL || outcome.Plan.PortName != "" || len(outcome.Plan.Commands) != 1 || !strings.Contains(outcome.Plan.Commands[0], "-IppURL") {
			t.Fatalf("WSD IPP import preview did not preserve the verified endpoint: %+v", outcome.Plan)
		}
	} else if outcome.Plan.IPP || !strings.HasPrefix(outcome.Plan.PortName, "RAW9100-") {
		t.Fatalf("WSD RAW import preview did not create an IP port: %+v", outcome.Plan)
	}
	t.Logf("Copied %s from WSD to IP %s; import would use %s with driver %s", queue, profile.Target, map[bool]string{true: profile.IPPURL, false: outcome.Plan.PortName}[profile.PortType == "ipp"], profile.DriverName)
}
