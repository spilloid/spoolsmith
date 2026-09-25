package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/spilloid/spoolsmith/internal/bundle"
	"github.com/spilloid/spoolsmith/internal/install"
)

// The apply sheet shows a plan as a short checklist of sentences instead of a
// transcript. Everything here is presentation only: each line is derived from
// one command of the plan the shared workflow produced, in the same order,
// and the command itself -- the thing that is confirmed and run -- is never
// rewritten. Details on the sheet still shows the full plan.

// stepState is how one checklist line is drawn.
type stepState string

const (
	stepPending   stepState = "pending"   // in the plan, not run yet
	stepDone      stepState = "done"      // ran and changed something
	stepUnchanged stepState = "unchanged" // ran, or will run, and needs no change
	stepFailed    stepState = "failed"    // ran and failed
	stepNotRun    stepState = "not run"   // an earlier step failed first
)

// checklistLine is one plan step in plain words.
type checklistLine struct {
	Text string
	// Detail is extra lines shown under the step: a trusted publisher, or
	// Windows' own reason for a failure.
	Detail []string
	State  stepState
}

// Mark is the glyph drawn before a line. Plain Unicode, so the checklist reads
// the same in a label, a screen reader and pasted ticket notes.
func (l checklistLine) Mark() string {
	switch l.State {
	case stepDone:
		return "✓"
	case stepUnchanged:
		return "–"
	case stepFailed:
		return "✕"
	case stepNotRun:
		return "·"
	default:
		return "•"
	}
}

type stepKind int

const (
	stepUnknown stepKind = iota
	stepDriver
	stepPort
	stepQueue
	stepRemoveQueue
	stepRemovePort
	stepRemoveDriver
)

// classifyCommand recognizes the commands the shared workflow emits
// (internal/install: reconcile.go, bundledriver.go, package.go) by markers
// that appear in exactly one of them. An unrecognized command is still shown,
// as a numbered step whose full text is in Details.
func classifyCommand(command string) stepKind {
	switch {
	case strings.Contains(command, "Remove-Printer -InputObject"):
		return stepRemoveQueue
	case strings.Contains(command, "Retained shared port"):
		return stepRemovePort
	case strings.Contains(command, "Retained shared driver"):
		return stepRemoveDriver
	case strings.Contains(command, "pnputil.exe /add-driver"), strings.Contains(command, "Add-PrinterDriver -Name"):
		return stepDriver
	case strings.Contains(command, "Add-PrinterPort -Name"):
		return stepPort
	case strings.Contains(command, "Add-Printer -Name"):
		return stepQueue
	}
	return stepUnknown
}

func stepSentence(plan install.Plan, kind stepKind, index int) string {
	switch kind {
	case stepDriver:
		if plan.BundleDriver != nil {
			return "Install driver " + plan.DriverName + " from the printer file"
		}
		return "Install driver " + plan.DriverName
	case stepPort:
		return "Create port " + plan.IPAddress
	case stepQueue:
		switch {
		case plan.PreviousPortName != "":
			return "Point " + quoted(plan.PrinterName) + " at " + plan.IPAddress + " (the old port is kept)"
		case plan.UpdateExisting:
			return "Create or update printer " + quoted(plan.PrinterName)
		}
		return "Create printer " + quoted(plan.PrinterName)
	case stepRemoveQueue:
		return "Remove printer " + quoted(plan.PrinterName)
	case stepRemovePort:
		return "Remove port " + plan.PortName + " if nothing else uses it"
	case stepRemoveDriver:
		return "Remove driver " + plan.DriverName + " if nothing else uses it"
	}
	return fmt.Sprintf("Run plan step %d (see Details)", index+1)
}

// unchangedOutput reports a step that ran and found nothing to do. These are
// the exact status words the plan's commands print.
func unchangedOutput(output string) bool {
	for _, marker := range []string{"Unchanged ", "already absent", "Retained ", "Driver publisher already trusted"} {
		if strings.Contains(output, marker) {
			// A driver step that found its publisher already trusted can still
			// have staged the driver; only "Unchanged driver" is a no-op.
			if marker == "Driver publisher already trusted" && strings.Contains(output, "staged") {
				continue
			}
			return true
		}
	}
	return false
}

// trustLines picks the publisher-trust outcome out of a driver step's output.
func trustLines(output string) []string {
	var found []string
	for _, line := range strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Trusted driver publisher:") || strings.HasPrefix(line, "Driver publisher already trusted:") {
			found = append(found, line)
		}
	}
	return found
}

// planChecklist turns an outcome into checklist lines. Before execution every
// line is pending (or unchanged when preflight already knows); after, each
// line reflects the command result recorded for that exact command.
func planChecklist(out install.Outcome) []checklistLine {
	if out.Plan == nil {
		return nil
	}
	plan := *out.Plan
	lines := make([]checklistLine, 0, len(plan.Commands)+1)
	for i, command := range plan.Commands {
		kind := classifyCommand(command)
		line := checklistLine{Text: stepSentence(plan, kind, i), State: stepPending}
		if kind == stepDriver && out.Result == nil && out.Preflight != nil && out.Preflight.DriverChecked && out.Preflight.DriverPresent {
			line.Text = "Driver " + plan.DriverName + " is already installed"
			line.State = stepUnchanged
		}
		if out.Result != nil {
			line.State = stepNotRun
			// Bind to the command actually attempted at this position, never
			// to the overall status.
			if i < len(out.Result.Ran) && out.Result.Ran[i].Command == command {
				ran := out.Result.Ran[i]
				output := strings.TrimSpace(ran.Output)
				switch {
				case ran.Err != nil:
					line.State = stepFailed
					if reason := strings.TrimSpace(ran.Err.Error()); reason != "" {
						line.Detail = append(line.Detail, reason)
					}
				case unchangedOutput(output):
					line.State = stepUnchanged
				default:
					line.State = stepDone
				}
				if kind == stepDriver {
					line.Detail = append(trustLines(output), line.Detail...)
				}
			}
		}
		lines = append(lines, line)
		if kind == stepDriver && plan.PublisherTrust != nil && out.Result == nil && line.State == stepPending {
			lines = append(lines, checklistLine{
				Text:  "Trust the driver's publisher, only if Windows validates its signature",
				State: stepPending,
			})
		}
	}
	return lines
}

// checklistText renders lines for a label or the clipboard.
func checklistText(lines []checklistLine, newline string) string {
	var b strings.Builder
	for i, line := range lines {
		if i > 0 {
			b.WriteString(newline)
		}
		b.WriteString(line.Mark() + "  " + line.Text)
		for _, detail := range line.Detail {
			b.WriteString(newline + "     " + detail)
		}
	}
	return b.String()
}

// sheetHeader is the printer the sheet is about, as a title and one line.
type sheetHeader struct {
	Title    string
	Subtitle string
	Source   string
}

// headerFor describes the operation from the plan once there is one, and from
// the printer file or operation before that.
func headerFor(op operation, plan *install.Plan, manifest *bundle.Manifest) sheetHeader {
	h := sheetHeader{Title: shownOr(op.PrinterName, "Printer")}
	var address, driver, carried string
	if manifest != nil {
		h.Title = shownOr(manifest.Profile.PrinterName, h.Title)
		address, driver = manifest.Profile.Target, manifest.Profile.DriverName
		carried = "settings only"
		if manifest.Driver != nil {
			carried = "driver included"
		}
		var parts []string
		if manifest.SourceHost != "" {
			parts = append(parts, "from "+manifest.SourceHost)
		}
		if created, err := time.Parse(time.RFC3339, manifest.Created); err == nil {
			parts = append(parts, "copied "+created.Local().Format("Jan 2, 2006"))
		}
		h.Source = strings.Join(parts, " · ")
	}
	if plan != nil {
		h.Title = shownOr(plan.PrinterName, h.Title)
		address = shownOr(plan.IPAddress, address)
		driver = shownOr(plan.DriverName, driver)
	}
	if address == "" {
		address = shownOr(op.NewAddress, op.Target)
	}
	var parts []string
	for _, part := range []string{address, driver, carried} {
		if strings.TrimSpace(part) != "" {
			parts = append(parts, part)
		}
	}
	h.Subtitle = strings.Join(parts, " · ")
	return h
}

// sheetWarnings are the facts that deserve their own line above the steps.
func sheetWarnings(out install.Outcome, manifest *bundle.Manifest) []string {
	var warnings []string
	if manifest != nil {
		if manifest.Profile.Evidence.Provenance != "" && manifest.Profile.Evidence.Provenance != "captured" {
			warnings = append(warnings, bundle.UnconfirmedIdentityNotice)
		}
		if manifest.Driver == nil && manifest.Profile.DriverPackage == nil {
			warnings = append(warnings, "Settings only: this file has no driver, so this PC must already have "+shownOr(manifest.Profile.DriverName, "the driver")+".")
		}
	}
	switch out.Resolution {
	case "offline-fallback-operator-profile":
		warnings = append(warnings, "The printer didn't answer, so its identity isn't checked. Only this PC's setup is prepared.")
	case "offline-operator-profile":
		warnings = append(warnings, "Offline setup: the printer isn't contacted, so its identity isn't checked.")
	}
	return warnings
}

// primaryCaption is the sheet's main button for this operation.
func primaryCaption(op operation, elevated bool) string {
	var verb string
	switch op.Kind {
	case opRemove:
		verb = "Remove"
	case opRepoint:
		verb = "Change address"
	case opConfigure:
		verb = "Update"
	default:
		verb = "Install"
		if op.UpdateExisting {
			verb = "Update"
		}
	}
	if !elevated {
		return verb + " as administrator..."
	}
	return verb
}

// needsAdministrator reports a preview that stopped only because this process
// is not elevated. The plan is complete, so the sheet can show it and offer
// the shield button instead of an error.
func needsAdministrator(out install.Outcome) bool {
	return out.Plan != nil && strings.Contains(out.Error, "administrator privileges are required")
}

// ticketNotes is plain text for a ticket, built only from the execution
// record. A step that ran is never reported as a printed page.
func ticketNotes(version, host string, at time.Time, op operation, out install.Outcome) string {
	var b strings.Builder
	fmt.Fprintf(&b, "SpoolSmith %s — %s on %s, %s\n", version, strings.ToLower(op.Title()), shownOr(host, "this PC"), at.Format("2006-01-02 15:04"))
	if out.Plan != nil {
		p := out.Plan
		fmt.Fprintf(&b, "Printer: %s\n", p.PrinterName)
		if p.PreviousPortName != "" {
			fmt.Fprintf(&b, "Address: %s (was port %s)\n", p.IPAddress, p.PreviousPortName)
		} else if p.IPAddress != "" {
			fmt.Fprintf(&b, "Address: %s\n", p.IPAddress)
		}
		if p.DriverName != "" {
			driver := p.DriverName
			if p.BundleDriver != nil {
				driver += " (included in printer file)"
			}
			fmt.Fprintf(&b, "Driver: %s\n", driver)
		}
	}
	for _, warning := range sheetWarnings(out, nil) {
		fmt.Fprintf(&b, "Note: %s\n", warning)
	}
	for _, line := range planChecklist(out) {
		fmt.Fprintf(&b, "%s %s\n", line.Mark(), line.Text)
		for _, detail := range line.Detail {
			fmt.Fprintf(&b, "    %s\n", detail)
		}
	}
	result := "Failed"
	switch {
	case out.Error != "":
		fmt.Fprintf(&b, "Error: %s\n", out.Error)
	case out.Status == "success", out.Status == "already-absent":
		result = "Successful"
	default:
		result = shownOr(out.Status, result)
	}
	if out.PlanHash != "" {
		fmt.Fprintf(&b, "Plan: %s\n", out.PlanHash)
	}
	fmt.Fprintf(&b, "Result: %s\n", result)
	return b.String()
}

// wrapText breaks lines longer than width at spaces, keeping existing line
// breaks and each line's indentation. The sheet's labels don't wrap on their
// own (walk's wrapping label distorts the page layout), so long sentences are
// wrapped here instead.
func wrapText(text string, width int) string {
	var out []string
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		indent := line[:len(line)-len(strings.TrimLeft(line, " "))]
		for len([]rune(line)) > width {
			runes := []rune(line)
			cut := strings.LastIndex(string(runes[:width]), " ")
			if cut <= len(indent) {
				break
			}
			out = append(out, strings.TrimRight(line[:cut], " "))
			line = indent + strings.TrimLeft(line[cut:], " ")
		}
		out = append(out, line)
	}
	return strings.Join(out, "\r\n")
}
