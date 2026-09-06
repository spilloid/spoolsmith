// Package actionlog records a best-effort, local-only, JSON-lines trail of the
// commands and clicks SpoolSmith's CLI and GUI performed, for after-the-fact
// observability. It is never load-bearing: a logging failure never blocks or
// fails the operation it describes.
package actionlog

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// maxSizeBytes triggers a single rotation (actions.log -> actions.log.1) so the
// file never grows unbounded across a long-lived machine.
const maxSizeBytes = 5 * 1024 * 1024

// Entry is one recorded action. Fields are deliberately limited to
// operation/target metadata and outcome/timing: SpoolSmith collects no
// credentials anywhere, so there is nothing sensitive to redact, but the log
// still carries no driver payloads or file contents.
type Entry struct {
	Time     time.Time `json:"time"`
	Source   string    `json:"source"` // "cli" or "gui"
	Op       string    `json:"op"`
	Args     []string  `json:"args,omitempty"`
	Status   string    `json:"status"`
	ExitCode *int      `json:"exit_code,omitempty"`
	Error    string    `json:"error,omitempty"`
	Duration string    `json:"duration,omitempty"`
}

// Logger appends Entry values to a JSON-lines file. The zero value is not
// usable; construct with Open or use Default.
type Logger struct {
	mu   sync.Mutex
	path string
	file *os.File
}

// Path returns the standard action-log location: a "spoolsmith" directory
// under the OS temp directory. Exported so the GUI's log viewer and any
// support tooling agree with the CLI on where to look.
func Path() string {
	return filepath.Join(os.TempDir(), "spoolsmith", "actions.log")
}

// Open opens (creating as needed) the action log at path for appending.
func Open(path string) (*Logger, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("actionlog: create directory: %w", err)
	}
	rotateIfLarge(path)
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("actionlog: open: %w", err)
	}
	return &Logger{path: path, file: file}, nil
}

// noopLogger is returned by Default when the real log file can't be opened,
// so callers never have to nil-check: logging degrades silently rather than
// ever blocking or failing the command it's describing.
var noopLogger = &Logger{}

var (
	defaultOnce   sync.Once
	defaultLogger *Logger
)

// Default returns a process-wide Logger writing to Path(). If the file can't
// be opened (e.g. an unwritable temp directory), it prints one warning to
// stderr and returns a no-op logger; it never returns an error, so callers in
// both the CLI and GUI can use it unconditionally.
func Default() *Logger {
	defaultOnce.Do(func() {
		logger, err := Open(Path())
		if err != nil {
			fmt.Fprintf(os.Stderr, "spoolsmith: action logging disabled: %v\n", err)
			defaultLogger = noopLogger
			return
		}
		defaultLogger = logger
	})
	return defaultLogger
}

// Record appends one entry. A write failure is swallowed (best-effort,
// non-load-bearing observability) except when returned directly by tests
// exercising Open'd loggers other than Default.
func (l *Logger) Record(entry Entry) error {
	if l == nil || l.file == nil {
		return nil
	}
	if entry.Time.IsZero() {
		entry.Time = time.Now().UTC()
	} else {
		entry.Time = entry.Time.UTC()
	}
	line, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("actionlog: encode entry: %w", err)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	_, err = l.file.Write(append(line, '\n'))
	return err
}

// Close closes the underlying file. Safe to call on a no-op Logger.
func (l *Logger) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	return l.file.Close()
}

func rotateIfLarge(path string) {
	info, err := os.Stat(path)
	if err != nil || info.Size() < maxSizeBytes {
		return
	}
	_ = os.Remove(path + ".1")
	_ = os.Rename(path, path+".1")
}
