package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/spilloid/spoolsmith/internal/install"
)

// A printer set (.zip) is reviewed as a whole: every member is previewed
// against this PC first, the operator sees every plan in one list, and one
// confirmation of that list sets up the ticked printers. Each printer still
// runs its own reviewed plan, bound to the preview the operator saw
// (ExpectedPlan), exactly as `spoolsmith apply set.zip --yes` does after a dry
// run. Operator decision, 2026-11-10.

// setRowState is where one member of a set is in review and setup.
type setRowState int

const (
	rowChecking setRowState = iota
	// rowReady has a complete plan that this process can run.
	rowReady
	// rowNeedsAdmin has a complete plan that only an elevated copy can run.
	rowNeedsAdmin
	// rowNothing previewed with nothing to do.
	rowNothing
	// rowBlocked cannot be set up; Detail says why.
	rowBlocked
	rowRunning
	rowDone
	rowFailed
	// rowSkipped was not ticked when the set was confirmed.
	rowSkipped
)

// setRow is one printer file inside the set.
type setRow struct {
	Member  string // file name inside the zip
	Path    string // extracted, verified copy
	Name    string // printer name saved in the file
	Target  string // address, or "USB printer"
	Driver  string // "driver included" or "settings only"
	State   setRowState
	Checked bool
	Detail  string
	Preview *install.Outcome
	Result  *install.Outcome
	// Transcript is the preview's (then the run's) plan text.
	Transcript string
}

// Runnable reports a row that has a reviewed plan to run.
func (r setRow) Runnable() bool { return r.State == rowReady || r.State == rowNeedsAdmin }

// classifyPreview reads one member's dry run.
func classifyPreview(outcome install.Outcome, err error) (setRowState, string) {
	switch {
	case err != nil:
		return rowBlocked, err.Error()
	case needsAdministrator(outcome):
		return rowNeedsAdmin, ""
	case outcome.Error != "":
		return rowBlocked, outcome.Error
	case outcome.Status == "dry-run" && outcome.Plan != nil:
		return rowReady, ""
	default:
		return rowNothing, ""
	}
}

// classifyResult reads one member's real run.
func classifyResult(outcome install.Outcome) (setRowState, string) {
	if outcome.Error != "" || (outcome.Status != "success" && outcome.Status != "already-absent") {
		return rowFailed, shownOr(outcome.Error, outcome.Status)
	}
	return rowDone, ""
}

// stepCount is the number of plan steps, for the list and the confirmation.
func stepCount(out *install.Outcome) int {
	if out == nil || out.Plan == nil {
		return 0
	}
	return len(out.Plan.Commands)
}

func countSteps(n int) string {
	if n == 1 {
		return "1 step"
	}
	return fmt.Sprintf("%d steps", n)
}

// firstLine keeps the list cell to one line; the full text is under Details.
func firstLine(text string) string {
	text = strings.TrimSpace(strings.ReplaceAll(text, "\r\n", "\n"))
	if i := strings.IndexByte(text, '\n'); i >= 0 {
		text = text[:i]
	}
	return text
}

// StatusText is the row's "Plan" column.
func (r setRow) StatusText() string {
	switch r.State {
	case rowChecking:
		return "Checking..."
	case rowReady, rowNeedsAdmin:
		return "Ready: " + countSteps(stepCount(r.Preview))
	case rowNothing:
		return "Nothing to change"
	case rowBlocked:
		lead, ok := friendlyErrorLead(r.Detail)
		if !ok {
			lead = r.Detail
		}
		return "Can't set up: " + firstLine(lead)
	case rowRunning:
		return "Setting up..."
	case rowDone:
		return "Set up"
	case rowFailed:
		lead, ok := friendlyErrorLead(r.Detail)
		if !ok {
			lead = r.Detail
		}
		return "Failed: " + firstLine(lead)
	case rowSkipped:
		return "Not ticked, left alone"
	}
	return ""
}

// tickedRows are the rows the confirmation covers.
func tickedRows(rows []setRow) []setRow {
	var out []setRow
	for _, r := range rows {
		if r.Checked && r.Runnable() {
			out = append(out, r)
		}
	}
	return out
}

// tickedMembers names the ticked rows, so an elevated relaunch ticks the same
// printers (it still previews each of them again).
func tickedMembers(rows []setRow) []string {
	var names []string
	for _, r := range tickedRows(rows) {
		names = append(names, r.Member)
	}
	return names
}

// setPrimaryCaption is the set dialog's main button.
func setPrimaryCaption(ticked int, elevated, done bool) string {
	if done {
		return "Done"
	}
	text := "Set up " + countPrinters(ticked)
	if ticked == 0 {
		text = "Set up printers"
	}
	if !elevated {
		text += " as administrator..."
	}
	return text
}

// setConfirmText is the one confirmation of the whole reviewed set. It names
// every printer and its plan, so the operator confirms what is listed.
func setConfirmText(file string, rows []setRow, options setOptions) string {
	ticked := tickedRows(rows)
	var b strings.Builder
	fmt.Fprintf(&b, "Set up %s on this PC from %s:\n\n", countPrinters(len(ticked)), file)
	for _, r := range ticked {
		fmt.Fprintf(&b, "  •  %s  —  %s, %s (%s)\n", r.Name, shownOr(r.Target, "no address"), r.Driver, countSteps(stepCount(r.Preview)))
	}
	if left := len(rows) - len(ticked); left > 0 {
		fmt.Fprintf(&b, "\n%d other printer(s) in the set are left alone.\n", left)
	}
	if options.UpdateExisting {
		b.WriteString("\nPrinters already on this PC are updated to match their files.\n")
	}
	if options.Offline {
		b.WriteString("\nOffline: the printers are not contacted, so their identity is not checked.\n")
	}
	b.WriteString("\nEach printer runs exactly the steps shown for it under Details. If a printer's steps have changed since this review, it is not set up.\n\nProceed with these changes?")
	return b.String()
}

// setOptions are the set-wide choices that change every member's plan.
type setOptions struct {
	UpdateExisting bool
	Offline        bool
}

// setSummaryLine is the tidy one-line outcome under the list.
func setSummaryLine(rows []setRow) string {
	var done, failed, skipped int
	for _, r := range rows {
		switch r.State {
		case rowDone:
			done++
		case rowFailed:
			failed++
		default:
			skipped++
		}
	}
	return fmt.Sprintf("%d set up, %d failed, %d left alone.", done, failed, skipped)
}

// setSummaryText is the plain-text report for a ticket: one line per printer.
func setSummaryText(version, host string, at time.Time, file string, rows []setRow) string {
	var b strings.Builder
	fmt.Fprintf(&b, "SpoolSmith %s — printer set %s on %s, %s\n", version, file, shownOr(host, "this PC"), at.Format("2006-01-02 15:04"))
	fmt.Fprintln(&b, setSummaryLine(rows))
	b.WriteString("\n")
	for _, r := range rows {
		mark := "-"
		switch r.State {
		case rowDone:
			mark = "ok"
		case rowFailed:
			mark = "x"
		}
		fmt.Fprintf(&b, "  %-2s  %s  (%s, %s): %s\n", mark, r.Name, shownOr(r.Target, "no address"), r.Member, r.StatusText())
		if r.State == rowFailed && r.Detail != "" {
			fmt.Fprintf(&b, "        %s\n", firstLine(r.Detail))
		}
	}
	return b.String()
}

// rowSteps is one printer's plan in the sheet's own words: the steps it will
// take before the run, what each step did after it, or why it can't run.
func rowSteps(r setRow, newline string) string {
	out := r.Result
	if out == nil {
		out = r.Preview
	}
	var parts []string
	if r.Detail != "" && (r.State == rowBlocked || r.State == rowFailed) {
		lead, ok := friendlyErrorLead(r.Detail)
		if !ok {
			lead = r.Detail
		}
		parts = append(parts, lead)
	}
	if out != nil {
		if steps := strings.TrimSpace(checklistText(planChecklist(*out), newline)); steps != "" {
			parts = append(parts, steps)
		}
	}
	if len(parts) == 0 {
		switch r.State {
		case rowChecking:
			return "Checking this printer. Nothing changes yet..."
		case rowNothing:
			return "Nothing needs to change for this printer."
		}
	}
	return strings.Join(parts, newline)
}

// setDetailsText is every printer's steps, then its full plan text, in order.
func setDetailsText(rows []setRow) string {
	const nl = "\n"
	var b strings.Builder
	for i, r := range rows {
		if i > 0 {
			b.WriteString(nl + nl)
		}
		fmt.Fprintf(&b, "%d of %d  %s  (%s, %s)"+nl+"%s"+nl, i+1, len(rows), r.Name, shownOr(r.Target, "no address"), r.Member, r.StatusText())
		if steps := rowSteps(r, nl); steps != "" {
			for _, line := range strings.Split(steps, nl) {
				b.WriteString("    " + line + nl)
			}
		}
		if r.State == rowBlocked || r.State == rowFailed {
			b.WriteString("    Details: " + firstLine(r.Detail) + nl)
		}
		if t := strings.TrimSpace(r.Transcript); t != "" {
			b.WriteString(nl + "    Full plan:" + nl)
			for _, line := range strings.Split(strings.ReplaceAll(t, "\r"+nl, nl), nl) {
				b.WriteString("      " + line + nl)
			}
		}
	}
	return b.String()
}
