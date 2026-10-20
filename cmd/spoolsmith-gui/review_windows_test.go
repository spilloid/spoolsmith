//go:build windows

package main

import (
	"strings"
	"testing"
)

// TestFriendlyOperationErrorTranslatesCommonCasesAndKeepsTheDetail locks in
// the small set of errors an operator hits often, and that the original
// message always survives underneath -- a case this doesn't recognize is
// still shown verbatim, and a recognized one still carries its detail line
// for a screenshot or a ticket.
func TestFriendlyOperationErrorTranslatesCommonCasesAndKeepsTheDetail(t *testing.T) {
	for _, tc := range []struct {
		name    string
		raw     string
		wantLed string
	}{
		{"administrator", "install: administrator privileges are required", "Administrator access is needed"},
		{"driver not found", "install: driver not found: OEM Driver", "This driver is not installed"},
		{"file exists", "copy: office.ssb already exists; retry with a different, unused bundle filename", "already exists"},
		{"identity mismatch", `profile: HTTP title changed: saved "A", observed "B"; verify the device and recapture if appropriate`, "does not match what was saved"},
		{"unreachable", "install: collect evidence: dial tcp: connection refused", "could not be reached"},
		{"driver switch not restored", "install: native driver change left the queue in an unverified state: attention endpoint changed", "queue needs a look"},
		{"interrupted run", `install: step 2 of 2 failed: The spooler service is not reachable. (exit status 1). Already done before it: step 1: Created port`, "Some earlier steps may already have been applied"},
		{"step failure whose Windows reason says already exists", `install: step 1 of 2 failed: The specified port already exists. (exit status 1). No earlier step had run`, "A step failed part-way"},
		{"spooler down during the driver check", `install: driver presence check failed: install: check for driver "OEM": The spooler service is not reachable.  Ensure the spooler service is running. (exit status 1); guidance: install: driver not found — install it via Windows Update or run the vendor package manually first, then retry`, "Print Spooler"},
		{"removal stopped after the queue went", `remove: step 2 of 3 failed: The spooler service is not reachable. (exit status 1). Already done before it: step 1: Removed printer`, "Some earlier steps may already have been applied, such as removing the printer"},
		{"removal step whose Windows reason says administrator", `remove: step 2 of 3 failed: Access denied; run as administrator (exit status 1). Already done before it: step 1: Removed printer`, "A step failed part-way"},
		{"corrupt bundle", `bundle: decode manifest: invalid character 'P' looking for beginning of value`, "could not be read as a printer file"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := friendlyOperationError(tc.raw)
			if !strings.Contains(got, tc.wantLed) {
				t.Fatalf("missing lead %q in: %s", tc.wantLed, got)
			}
			if !strings.Contains(got, tc.raw) {
				t.Fatalf("lost the original message: %s", got)
			}
		})
	}
}

func TestFriendlyOperationErrorPassesThroughUnrecognizedMessages(t *testing.T) {
	raw := "install: something this function has never heard of"
	if got := friendlyOperationError(raw); got != raw {
		t.Fatalf("unrecognized message was altered: %s", got)
	}
}
