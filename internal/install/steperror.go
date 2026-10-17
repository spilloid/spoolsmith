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

// stepDiagnostic extracts the reason from a failed generated command's output.
//
// Every generated command ends `catch { Write-Error $_.Exception.Message; exit
// 1 }`. Windows PowerShell 5.1 prints such an error as the echoed script line
// (wrapped at the console width), " : ", the message (also wrapped), then an
// "At line:" position block. The message is recovered by unwrapping the text
// before "At line:" and taking what follows the echoed catch block. Anything
// else falls back to the last non-empty output line.
func stepDiagnostic(output string) string {
	text := strings.ReplaceAll(output, "\r\n", "\n")
	if index := strings.Index(text, "\nAt line:"); index >= 0 {
		head := strings.ReplaceAll(text[:index], "\n", "")
		const echoEnd = "exit 1 } : "
		if at := strings.LastIndex(head, echoEnd); at >= 0 {
			return clampDiagnostic(head[at+len(echoEnd):])
		}
		if at := strings.LastIndex(head, " : "); at >= 0 {
			return clampDiagnostic(head[at+len(" : "):])
		}
		text = text[:index]
	}
	lines := strings.Split(text, "\n")
	for index := len(lines) - 1; index >= 0; index-- {
		if line := strings.TrimSpace(lines[index]); line != "" {
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
