package install

import (
	"fmt"
	"strings"
)

// StepError reports that one command of a confirmed plan failed. It names the
// reason Windows gave and what the earlier steps already did, so the one-line
// error that reaches a GUI dialog, the --json "error" field and the action log
// tells a helpdesk tech what happened without the per-command transcript.
// The full command and output stay in Result.Ran.
type StepError struct {
	Op         string // "install", "remove" or "repoint"
	Step       int    // 1-based
	Total      int
	Command    string
	Diagnostic string
	// Completed lists the outputs of the steps that finished before this one,
	// in order. A step that printed nothing is listed as "step N: done".
	Completed []string
	Err       error
}

func (e *StepError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s: step %d of %d failed", e.Op, e.Step, e.Total)
	if e.Diagnostic != "" {
		b.WriteString(": ")
		b.WriteString(e.Diagnostic)
	}
	if e.Err != nil {
		fmt.Fprintf(&b, " (%v)", e.Err)
	}
	if len(e.Completed) == 0 {
		b.WriteString(". No earlier step had run")
	} else {
		b.WriteString(". Already done before it: ")
		b.WriteString(strings.Join(e.Completed, "; "))
	}
	return b.String()
}

func (e *StepError) Unwrap() error { return e.Err }

// newStepError builds a StepError from the commands that ran, the last of
// which failed.
func newStepError(op string, total int, ran []CommandResult) *StepError {
	failed := ran[len(ran)-1]
	stepErr := &StepError{Op: op, Step: len(ran), Total: total, Command: failed.Command, Diagnostic: stepDiagnostic(failed.Output), Err: failed.Err}
	for index, earlier := range ran[:len(ran)-1] {
		summary := summarizeStepOutput(earlier.Output)
		if summary == "" {
			summary = "done"
		}
		stepErr.Completed = append(stepErr.Completed, fmt.Sprintf("step %d: %s", index+1, summary))
	}
	return stepErr
}

const maxDiagnosticRunes = 400

// reasonMarker starts the line every generated command's catch block prints
// with the exception message (see powerShellCommand). The reason is read from
// that line, not from PowerShell's error view, whose position header ("At
// line:", "In Zeile:", "Au caractère") is in the display language. The catch
// block builds the marker from two literals, so the script text PowerShell
// echoes with the error never contains it.
const reasonMarker = "SPOOLSMITH-REASON: "

// stepDiagnostic extracts the reason from a failed generated command's output.
//
// The marker line wins. Output without one (a command that died before its
// catch block, or text from an older build) falls back to the layout of
// Windows PowerShell 5.1's error view: the echoed script line (wrapped at the
// console width), " : ", the message (also wrapped), a position header line,
// then "+"-prefixed lines (the caret, CategoryInfo, FullyQualifiedErrorId).
// Those are found by their shape, so a localized header does not matter.
// Anything else gives the last non-empty line that is not error-record
// metadata.
func stepDiagnostic(output string) string {
	lines := strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n")
	reason := ""
	for _, line := range lines {
		if rest, ok := strings.CutPrefix(line, reasonMarker); ok {
			reason = rest
		}
	}
	if reason = strings.TrimSpace(reason); reason != "" {
		return clampDiagnostic(reason)
	}
	for index := 2; index < len(lines); index++ {
		if !strings.HasPrefix(strings.TrimSpace(lines[index]), "+") {
			continue
		}
		// lines[index-1] is the position header; the message ends just above it.
		head := strings.Join(lines[:index-1], "")
		const echoEnd = "exit 1 } : "
		if at := strings.LastIndex(head, echoEnd); at >= 0 {
			return clampDiagnostic(head[at+len(echoEnd):])
		}
		if at := strings.LastIndex(head, " : "); at >= 0 {
			return clampDiagnostic(head[at+len(" : "):])
		}
		lines = lines[:index-1]
		break
	}
	for index := len(lines) - 1; index >= 0; index-- {
		if line := strings.TrimSpace(lines[index]); line != "" && !strings.HasPrefix(line, "+") {
			return clampDiagnostic(line)
		}
	}
	return ""
}

// summarizeStepOutput keeps a successful step's short status lines ("Created
// port", "Unchanged driver") and drops long tool transcripts.
func summarizeStepOutput(output string) string {
	var kept []string
	for _, line := range strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || len([]rune(line)) > 120 || strings.HasPrefix(line, nativeDriverMarker) {
			continue
		}
		kept = append(kept, line)
	}
	if len(kept) > 3 {
		kept = kept[len(kept)-3:]
	}
	return clampDiagnostic(strings.Join(kept, ", "))
}

func clampDiagnostic(value string) string {
	value = strings.TrimSpace(value)
	if runes := []rune(value); len(runes) > maxDiagnosticRunes {
		return string(runes[:maxDiagnosticRunes]) + "..."
	}
	return value
}
