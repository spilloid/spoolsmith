package main

import (
	"bytes"
	"io"
	"strings"
	"sync"
)

// maxCapturedError bounds what one action-log entry carries.
const maxCapturedError = 2000

// errorCapture passes stderr through untouched and remembers the error lines
// the CLI writes ("spoolsmith <command>: <reason>"), so the action log records
// why a run failed and not only that it did. Without it a helpdesk tech
// reading actions.log saw `"status":"error","exit_code":1` and nothing else;
// the desktop already recorded its error text.
type errorCapture struct {
	mu      sync.Mutex
	out     io.Writer
	partial []byte
	errors  []string
}

func newErrorCapture(out io.Writer) *errorCapture { return &errorCapture{out: out} }

func (c *errorCapture) Write(p []byte) (int, error) {
	c.mu.Lock()
	c.partial = append(c.partial, p...)
	for {
		index := bytes.IndexByte(c.partial, '\n')
		if index < 0 {
			break
		}
		c.consider(string(c.partial[:index]))
		c.partial = c.partial[index+1:]
	}
	c.mu.Unlock()
	return c.out.Write(p)
}

func (c *errorCapture) consider(line string) {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "spoolsmith ") || !strings.Contains(line, ": ") {
		return
	}
	c.errors = append(c.errors, line)
	if len(c.errors) > 3 {
		c.errors = c.errors[len(c.errors)-3:]
	}
}

// Error returns the last few error lines, joined, or "" if none were written.
func (c *errorCapture) Error() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	lines := append([]string(nil), c.errors...)
	if tail := strings.TrimSpace(string(c.partial)); tail != "" && strings.HasPrefix(tail, "spoolsmith ") {
		lines = append(lines, tail)
	}
	joined := strings.Join(lines, " | ")
	if runes := []rune(joined); len(runes) > maxCapturedError {
		joined = string(runes[:maxCapturedError]) + "..."
	}
	return joined
}
