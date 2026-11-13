package main

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/spilloid/spoolsmith/internal/install"
)

func planned(steps int) *install.Outcome {
	return &install.Outcome{Status: "dry-run", Plan: &install.Plan{Commands: make([]string, steps)}}
}

func TestClassifyPreviewSeparatesRunnableFromBlocked(t *testing.T) {
	cases := []struct {
		name    string
		outcome install.Outcome
		err     error
		want    setRowState
	}{
		{"ready", *planned(3), nil, rowReady},
		{"needs admin", install.Outcome{Status: "error", Error: "administrator privileges are required", Plan: &install.Plan{}}, nil, rowNeedsAdmin},
		{"admin without plan is blocked", install.Outcome{Status: "error", Error: "administrator privileges are required"}, nil, rowBlocked},
		{"unreadable file", install.Outcome{}, errors.New("bundle: bad manifest"), rowBlocked},
		{"workflow error", install.Outcome{Status: "error", Error: "driver not found"}, nil, rowBlocked},
		{"no plan", install.Outcome{Status: "dry-run"}, nil, rowNothing},
	}
	for _, c := range cases {
		got, _ := classifyPreview(c.outcome, c.err)
		if got != c.want {
			t.Errorf("%s: state = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestClassifyResultFailsAnythingButSuccess(t *testing.T) {
	if got, _ := classifyResult(install.Outcome{Status: "success"}); got != rowDone {
		t.Fatalf("success = %v", got)
	}
	for _, out := range []install.Outcome{{Status: "error", Error: "plan changed"}, {Status: "not-confirmed"}, {Status: "success", Error: "step 2 failed"}} {
		if got, detail := classifyResult(out); got != rowFailed || detail == "" {
			t.Fatalf("%+v = %v %q", out, got, detail)
		}
	}
}

func TestOnlyTickedRunnableRowsAreConfirmedAndHandedOver(t *testing.T) {
	rows := []setRow{
		{Member: "a.ssb", Name: "A", Target: "10.0.0.1", Driver: "settings only", State: rowReady, Checked: true, Preview: planned(2)},
		{Member: "b.ssb", Name: "B", State: rowReady, Checked: false, Preview: planned(2)},
		{Member: "c.ssb", Name: "C", State: rowBlocked, Checked: true, Detail: "driver not found"},
		{Member: "d.ssb", Name: "D", Target: "USB printer", Driver: "driver included", State: rowNeedsAdmin, Checked: true, Preview: planned(1)},
	}
	if got := tickedMembers(rows); !slices.Equal(got, []string{"a.ssb", "d.ssb"}) {
		t.Fatalf("ticked = %v", got)
	}
	text := setConfirmText("office.zip", rows, setOptions{Offline: true})
	for _, want := range []string{"Set up 2 printers", "office.zip", "A  —  10.0.0.1, settings only (2 steps)", "D  —  USB printer, driver included (1 step)", "2 other printer(s)", "Offline", "Proceed"} {
		if !strings.Contains(text, want) {
			t.Errorf("confirmation lacks %q:\n%s", want, text)
		}
	}
	for _, unwanted := range []string{"B  —", "C  —"} {
		if strings.Contains(text, unwanted) {
			t.Errorf("confirmation names an unticked or blocked printer %q:\n%s", unwanted, text)
		}
	}
}

func TestSetPrimaryCaptionNamesCountAndElevation(t *testing.T) {
	for _, c := range []struct {
		n              int
		elevated, done bool
		want           string
	}{
		{3, true, false, "Set up 3 printers"},
		{1, false, false, "Set up 1 printer as administrator..."},
		{0, true, false, "Set up printers"},
		{3, false, true, "Done"},
	} {
		if got := setPrimaryCaption(c.n, c.elevated, c.done); got != c.want {
			t.Errorf("caption(%d,%v,%v) = %q, want %q", c.n, c.elevated, c.done, got, c.want)
		}
	}
}

func TestSetSummaryIsOneLinePerPrinter(t *testing.T) {
	rows := []setRow{
		{Member: "a.ssb", Name: "Lobby", Target: "10.0.0.1", State: rowDone},
		{Member: "b.ssb", Name: "Lab", Target: "10.0.0.2", State: rowFailed, Detail: "install: step 2 failed\nmore"},
		{Member: "c.ssb", Name: "Desk", State: rowSkipped},
		{Member: "d.ssb", Name: "Old", State: rowBlocked, Detail: "driver not found"},
	}
	if got := setSummaryLine(rows); got != "1 set up, 1 failed, 2 left alone." {
		t.Fatalf("summary line = %q", got)
	}
	text := setSummaryText("v1.6.0", "PC1", time.Date(2026, 11, 10, 9, 30, 0, 0, time.UTC), "office.zip", rows)
	for _, want := range []string{"SpoolSmith v1.6.0", "office.zip on PC1, 2026-11-10 09:30", "ok  Lobby  (10.0.0.1, a.ssb): Set up", "x   Lab", "Failed: A step failed part-way", "install: step 2 failed", "Desk  (no address, c.ssb): Not ticked", "Can't set up: The required driver is missing or incomplete"} {
		if !strings.Contains(text, want) {
			t.Errorf("summary lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "more") {
		t.Errorf("summary carries more than the first line of a failure:\n%s", text)
	}
}

func TestSetDetailsCarryEveryTranscript(t *testing.T) {
	rows := []setRow{
		{Member: "a.ssb", Name: "A", State: rowReady, Preview: planned(1), Transcript: "Queue: A"},
		{Member: "b.ssb", Name: "B", State: rowBlocked, Detail: "bundle: bad"},
	}
	text := setDetailsText(rows)
	for _, want := range []string{"1 of 2  A  (no address, a.ssb)", "Ready: 1 step", "Full plan:", "Queue: A", "2 of 2  B", "could not be read as a printer file", "Details: bundle: bad"} {
		if !strings.Contains(text, want) {
			t.Errorf("details lack %q:\n%s", want, text)
		}
	}
}

func TestSetHandOverRoundTripsTickedMembers(t *testing.T) {
	op := operation{Kind: opApplySet, BundlePath: `C:\sets\office.zip`, UpdateExisting: true, Offline: true, Members: []string{"a.ssb", "d.ssb"}}
	arg, err := encodeReview(op)
	if err != nil {
		t.Fatal(err)
	}
	request, err := parseLaunchArgs([]string{arg})
	if err != nil {
		t.Fatal(err)
	}
	got := request.Review
	if got == nil || got.Kind != opApplySet || got.BundlePath != op.BundlePath || !got.UpdateExisting || !got.Offline || !slices.Equal(got.Members, op.Members) {
		t.Fatalf("review = %+v", got)
	}
	if _, err := decodeReview(strings.TrimPrefix(mustEncode(t, operation{Kind: opApplySet}), resumeFlag)); err == nil {
		t.Fatal("a set hand-over without a set was accepted")
	}
	// Members never leak into another kind of operation.
	if n := (operation{Kind: opApply, BundlePath: "a.ssb", Members: []string{"x"}}).normalized(); n.Members != nil {
		t.Fatalf("members kept on %s", n.Kind)
	}
}

func TestRowStepsExplainEachState(t *testing.T) {
	const nl = "\n"
	if got := rowSteps(setRow{State: rowChecking}, nl); !strings.Contains(got, "Checking") {
		t.Fatalf("checking = %q", got)
	}
	if got := rowSteps(setRow{State: rowNothing}, nl); !strings.Contains(got, "Nothing needs to change") {
		t.Fatalf("nothing = %q", got)
	}
	blocked := rowSteps(setRow{State: rowBlocked, Detail: "driver not found"}, nl)
	if !strings.HasPrefix(blocked, "The required driver is missing or incomplete") {
		t.Fatalf("blocked = %q", blocked)
	}
	ready := rowSteps(setRow{State: rowReady, Preview: planned(2)}, nl)
	if strings.Count(ready, nl) != 1 || strings.HasSuffix(ready, nl) {
		t.Fatalf("two steps should be two lines: %q", ready)
	}
}

func mustEncode(t *testing.T, op operation) string {
	t.Helper()
	arg, err := encodeReview(op)
	if err != nil {
		t.Fatal(err)
	}
	return arg
}
