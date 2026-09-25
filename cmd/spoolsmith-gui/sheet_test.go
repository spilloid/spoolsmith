package main

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/spilloid/spoolsmith/internal/bundle"
	"github.com/spilloid/spoolsmith/internal/install"
)

const (
	cmdDriver = `$x; pnputil.exe /add-driver $inf; 'Driver package staged'`
	cmdPort   = `$x; Add-PrinterPort -Name "RAW9100-10.0.0.5" -PrinterHostAddress "10.0.0.5"; 'Created port'`
	cmdQueue  = `$x; Add-Printer -Name "Front" -DriverName "D" -PortName "RAW9100-10.0.0.5"; 'Created printer'`
	cmdRmQ    = `$x; Remove-Printer -InputObject $printer[0]; 'Removed printer'`
	cmdRmPort = `$x; if ($users.Count -gt 0) { 'Retained shared port' }`
	cmdRmDrv  = `$x; if ($users.Count -gt 0) { 'Retained shared driver' }`
)

func samplePlan() *install.Plan {
	return &install.Plan{
		PrinterName: "Front", IPAddress: "10.0.0.5", PortName: "RAW9100-10.0.0.5", DriverName: "D",
		Commands:       []string{cmdDriver, cmdPort, cmdQueue},
		BundleDriver:   &install.BundleDriver{WindowsDriverName: "D"},
		PublisherTrust: &install.PublisherTrust{Store: `LocalMachine\TrustedPublisher`},
	}
}

func TestClassifyCommandKeepsPortAndQueueApart(t *testing.T) {
	cases := map[string]stepKind{
		cmdDriver: stepDriver, cmdPort: stepPort, cmdQueue: stepQueue,
		cmdRmQ: stepRemoveQueue, cmdRmPort: stepRemovePort, cmdRmDrv: stepRemoveDriver,
		`Add-PrinterDriver -Name "D"`: stepDriver,
		`Get-Something`:               stepUnknown,
	}
	for command, want := range cases {
		if got := classifyCommand(command); got != want {
			t.Errorf("classifyCommand(%q) = %v, want %v", command, got, want)
		}
	}
}

func TestPreviewChecklistListsEveryStepAndPublisherTrust(t *testing.T) {
	lines := planChecklist(install.Outcome{Plan: samplePlan()})
	want := []string{
		"Install driver D from the printer file",
		"Trust the driver's publisher, only if Windows validates its signature",
		"Create port 10.0.0.5",
		`Create printer "Front"`,
	}
	if len(lines) != len(want) {
		t.Fatalf("got %d lines, want %d: %+v", len(lines), len(want), lines)
	}
	for i, line := range lines {
		if line.Text != want[i] || line.State != stepPending {
			t.Errorf("line %d = %q (%s), want %q pending", i, line.Text, line.State, want[i])
		}
	}
}

func TestPreviewChecklistMarksAnInstalledDriverUnchanged(t *testing.T) {
	out := install.Outcome{Plan: samplePlan(), Preflight: &install.PreflightResult{DriverChecked: true, DriverPresent: true}}
	lines := planChecklist(out)
	if lines[0].State != stepUnchanged || !strings.Contains(lines[0].Text, "already installed") {
		t.Fatalf("driver line = %+v", lines[0])
	}
	for _, line := range lines {
		if strings.Contains(line.Text, "publisher") {
			t.Fatalf("publisher trust shown for a driver that will not be staged: %+v", lines)
		}
	}
}

func TestResultChecklistBindsToTheCommandThatRan(t *testing.T) {
	plan := samplePlan()
	out := install.Outcome{Plan: plan, Status: "error", Result: &install.Result{Ran: []install.CommandResult{
		{Command: cmdDriver, Output: "Trusted driver publisher: Example Corp (ABC123)\nDriver package staged"},
		{Command: cmdPort, Output: "Unchanged port"},
		{Command: cmdQueue, Err: errors.New("Access is denied")},
	}}}
	lines := planChecklist(out)
	if len(lines) != 3 {
		t.Fatalf("result shows %d lines, want 3 (no pending trust line after execution)", len(lines))
	}
	if lines[0].State != stepDone || len(lines[0].Detail) != 1 || !strings.Contains(lines[0].Detail[0], "Example Corp") {
		t.Errorf("driver line = %+v", lines[0])
	}
	if lines[1].State != stepUnchanged {
		t.Errorf("port line = %+v", lines[1])
	}
	if lines[2].State != stepFailed || lines[2].Detail[0] != "Access is denied" {
		t.Errorf("queue line = %+v", lines[2])
	}
}

func TestResultChecklistNeverInfersSuccessForStepsThatDidNotRun(t *testing.T) {
	out := install.Outcome{Plan: samplePlan(), Status: "success", Result: &install.Result{Ran: []install.CommandResult{
		{Command: cmdDriver, Err: errors.New("Windows rejected the driver package")},
	}}}
	lines := planChecklist(out)
	if lines[1].State != stepNotRun || lines[2].State != stepNotRun {
		t.Fatalf("steps after a failure must be not run: %+v", lines)
	}
	// A result whose recorded command differs from the plan's is not trusted.
	out.Result.Ran = []install.CommandResult{{Command: "something else"}}
	if planChecklist(out)[0].State != stepNotRun {
		t.Fatal("a mismatched command was reported as run")
	}
}

func TestRemovalAndRepointSentences(t *testing.T) {
	plan := &install.Plan{PrinterName: "Front", PortName: "P", DriverName: "D", Commands: []string{cmdRmQ, cmdRmPort, cmdRmDrv}}
	got := checklistText(planChecklist(install.Outcome{Plan: plan}), "\n")
	for _, want := range []string{`Remove printer "Front"`, "Remove port P if nothing else uses it", "Remove driver D if nothing else uses it"} {
		if !strings.Contains(got, want) {
			t.Errorf("removal checklist missing %q:\n%s", want, got)
		}
	}
	repoint := &install.Plan{PrinterName: "Front", IPAddress: "10.0.0.9", PreviousPortName: "old", Commands: []string{cmdPort, cmdQueue}}
	if text := planChecklist(install.Outcome{Plan: repoint})[1].Text; !strings.Contains(text, "old port is kept") {
		t.Errorf("repoint line = %q", text)
	}
}

func TestHeaderPrefersThePlanAndShowsWhereTheFileCameFrom(t *testing.T) {
	manifest := &bundle.Manifest{Created: "2026-09-20T15:04:05Z", SourceHost: "FRONTDESK-01",
		Profile: install.Profile{PrinterName: "From file", Target: "10.0.0.5", DriverName: "D"},
		Driver:  &bundle.DriverPayload{}}
	h := headerFor(operation{Kind: opApply}, nil, manifest)
	if h.Title != "From file" || h.Subtitle != "10.0.0.5 · D · driver included" || !strings.HasPrefix(h.Source, "from FRONTDESK-01 · copied ") {
		t.Fatalf("header = %+v", h)
	}
	h = headerFor(operation{Kind: opRepoint, PrinterName: "Front", NewAddress: "10.0.0.9"}, nil, nil)
	if h.Title != "Front" || h.Subtitle != "10.0.0.9" {
		t.Fatalf("repoint header = %+v", h)
	}
}

func TestPrimaryCaptionCarriesTheElevationPrompt(t *testing.T) {
	if got := primaryCaption(operation{Kind: opApply}, false); got != "Install as administrator..." {
		t.Errorf("unelevated apply = %q", got)
	}
	if got := primaryCaption(operation{Kind: opRemove}, true); got != "Remove" {
		t.Errorf("elevated remove = %q", got)
	}
	if got := primaryCaption(operation{Kind: opApply, UpdateExisting: true}, true); got != "Update" {
		t.Errorf("update = %q", got)
	}
}

func TestTicketNotesReportWhatRanAndTheResult(t *testing.T) {
	out := install.Outcome{Plan: samplePlan(), Status: "success", PlanHash: "abc", Result: &install.Result{Ran: []install.CommandResult{
		{Command: cmdDriver, Output: "Trusted driver publisher: Example Corp (ABC123)\nDriver package staged"},
		{Command: cmdPort, Output: "Created port"},
		{Command: cmdQueue, Output: "Created printer"},
	}}}
	at := time.Date(2026, 9, 24, 14, 2, 0, 0, time.UTC)
	notes := ticketNotes("v1.3.0", "PC-1", at, operation{Kind: opApply}, out)
	for _, want := range []string{
		"SpoolSmith v1.3.0 — add printer from file on PC-1, 2026-09-24 14:02",
		"Printer: Front", "Address: 10.0.0.5", "Driver: D (included in printer file)",
		"✓ Install driver D from the printer file", "    Trusted driver publisher: Example Corp (ABC123)",
		"✓ Create port 10.0.0.5", `✓ Create printer "Front"`, "Plan: abc", "Result: Successful",
	} {
		if !strings.Contains(notes, want) {
			t.Errorf("notes missing %q:\n%s", want, notes)
		}
	}
	out.Error = "install: Access is denied"
	out.Status = "error"
	failed := ticketNotes("v1.3.0", "PC-1", at, operation{Kind: opApply}, out)
	if !strings.Contains(failed, "Error: install: Access is denied") || !strings.Contains(failed, "Result: Failed") {
		t.Errorf("failure notes:\n%s", failed)
	}
}

func TestNeedsAdministratorOnlyForACompletePlan(t *testing.T) {
	if !needsAdministrator(install.Outcome{Plan: samplePlan(), Error: "install: administrator privileges are required"}) {
		t.Error("an unelevated preview with a plan should offer elevation")
	}
	if needsAdministrator(install.Outcome{Error: "install: administrator privileges are required"}) {
		t.Error("no plan, nothing to elevate for")
	}
}

func TestWrapTextKeepsBreaksAndIndentation(t *testing.T) {
	got := wrapText("short\n     a detail line that is long enough to wrap", 20)
	want := "short\r\n     a detail line\r\n     that is long\r\n     enough to wrap"
	if got != want {
		t.Fatalf("wrapText = %q, want %q", got, want)
	}
	if wrapText("unbreakable-long-word", 5) != "unbreakable-long-word" {
		t.Fatal("a word longer than the width was split")
	}
}
