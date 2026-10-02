package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/spilloid/spoolsmith/internal/install"
)

func TestWindowsDriverDirectIPPreviewCLI(t *testing.T) {
	app := testApplication()
	app.workflow.DiscoverIPP = func(context.Context, string) (string, string, error) {
		return "ipp://192.0.2.10/ipp/print", "Example IPP printer", nil
	}
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"add", "192.0.2.10", "--windows-driver", "--name", "Office", "--dry-run", "--json"}, strings.NewReader(""), &stdout, &stderr, app)
	if code != int(install.ExitSuccess) || strings.Contains(stderr.String(), "pending deprecation") {
		t.Fatalf("code=%d out=%s err=%s", code, stdout.String(), stderr.String())
	}
	assertValidJSON(t, stdout.Bytes())
	var result struct {
		Plan struct {
			IPP bool `json:"ipp"`
		} `json:"plan"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil || !result.Plan.IPP {
		t.Fatalf("expected IPP plan: %v", err)
	}
}

func TestWindowsDriverArgumentsRejectAmbiguousModes(t *testing.T) {
	for _, args := range [][]string{
		{"192.0.2.10", "--windows-driver"},
		{"192.0.2.10", "--name", "Office"},
		{"192.0.2.10", "--windows-driver", "--name", "Office", "--offline"},
		{"192.0.2.10", "--windows-driver", "--name", "Office", "--force-family", "brother-hl-l2xxx"},
	} {
		if _, err := parseInstallArgs(args); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}
