package bundle

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spilloid/spoolsmith/internal/install"
)

// AllOptions describes a copy of every installed, reproducible printer queue.
// Driver files remain an explicit opt-in, just as they are for Create.
type AllOptions struct {
	OutputDir     string
	IncludeDriver bool
	Note          string
	CreatedBy     string
	SourceHost    string
	Progress      func(queueName, step string)
}

// AllResult reports every queue considered by CreateAll, including queues that
// could not be copied and those left unstarted when the operation was canceled.
type AllResult struct {
	OutputDir string        `json:"output_dir"`
	Requested int           `json:"requested"`
	Written   int           `json:"written"`
	Skipped   int           `json:"skipped"`
	Failed    int           `json:"failed"`
	Queues    []QueueResult `json:"queues"`
}

// QueueResult is one queue's outcome. Status is written, skipped, or error.
type QueueResult struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Bundle string `json:"bundle,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// CreateAll writes one bundle per copyable queue using the same operation as a
// single Create. Each queue is independent: an unsupported queue is skipped,
// and a failed copy does not stop subsequent copies. Ordinary per-queue errors
// are reported in AllResult, while setup errors and cancellation are returned.
// Cancellation preserves completed outcomes and reports unstarted queues as
// errors, so callers can always account for the entire known inventory.
func CreateAll(ctx context.Context, env install.Environment, collect Collector, opts AllOptions) (AllResult, error) {
	result := AllResult{OutputDir: opts.OutputDir, Queues: []QueueResult{}}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if strings.TrimSpace(opts.OutputDir) == "" {
		return result, errors.New("copy: choose where to save the files")
	}
	queues, err := install.ListPrinters(ctx, env)
	if err != nil {
		return result, err
	}
	if len(queues) == 0 {
		return result, errors.New("this PC has no printer queues installed")
	}
	result.Requested = len(queues)
	result.Queues = make([]QueueResult, 0, len(queues))
	markRemaining := func(start int, cause error) {
		for _, queue := range queues[start:] {
			result.Failed++
			result.Queues = append(result.Queues, QueueResult{
				Name: queue.PrinterName, Status: "error", Reason: "not copied: " + cause.Error(),
			})
		}
	}
	if err := ctx.Err(); err != nil {
		markRemaining(0, err)
		return result, err
	}
	if err := os.MkdirAll(opts.OutputDir, 0o700); err != nil {
		markRemaining(0, err)
		return result, fmt.Errorf("copy --all: %w", err)
	}
	entries, err := os.ReadDir(opts.OutputDir)
	if err != nil {
		markRemaining(0, err)
		return result, fmt.Errorf("copy --all: %w", err)
	}
	// Windows file names are case-insensitive. Check existing entries and
	// reserve each chosen name before probing or exporting driver files, even
	// when this operation is tested on a case-sensitive filesystem. Write
	// still uses exclusive creation to protect against races at write time.
	occupied := make(map[string]string, len(entries)+len(queues))
	for _, entry := range entries {
		occupied[strings.ToLower(entry.Name())] = "an existing file or folder"
	}
	for index, queue := range queues {
		if err := ctx.Err(); err != nil {
			markRemaining(index, err)
			return result, err
		}
		outcome := QueueResult{Name: queue.PrinterName}
		if reason := queue.CopyBlockedReason(); reason != "" {
			outcome.Status, outcome.Reason = "skipped", reason
			result.Skipped++
			result.Queues = append(result.Queues, outcome)
			continue
		}
		name := FileName(queue.PrinterName)
		path := filepath.Join(opts.OutputDir, name)
		key := strings.ToLower(name)
		if owner, exists := occupied[key]; exists {
			outcome.Status = "error"
			outcome.Reason = fmt.Sprintf("copy: %s is already used by %s; retry this queue with an explicit, unused bundle filename", path, owner)
			result.Failed++
			result.Queues = append(result.Queues, outcome)
			continue
		}
		occupied[key] = fmt.Sprintf("queue %q", queue.PrinterName)
		created, err := Create(ctx, env, collect, CreateOptions{
			QueueName: queue.PrinterName, Path: path, IncludeDriver: opts.IncludeDriver,
			Note: opts.Note, CreatedBy: opts.CreatedBy, SourceHost: opts.SourceHost,
			Progress: func(step string) {
				if opts.Progress != nil {
					opts.Progress(queue.PrinterName, step)
				}
			},
		})
		if err != nil {
			outcome.Status, outcome.Reason = "error", err.Error()
			result.Failed++
		} else {
			outcome.Status, outcome.Bundle = "written", path
			if created.Manifest.Profile.Evidence.Provenance != "captured" {
				outcome.Reason = "written offline: the printer did not answer, so its identity was not confirmed"
			}
			result.Written++
		}
		result.Queues = append(result.Queues, outcome)
	}
	return result, ctx.Err()
}

// FileName derives the same portable bundle name for both front ends. Distinct
// queue names can produce the same name; CreateAll reports those collisions,
// and Write never replaces an existing file.
func FileName(queueName string) string {
	var builder strings.Builder
	for _, r := range queueName {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			builder.WriteRune(r)
		default:
			builder.WriteRune('-')
		}
	}
	name := strings.Trim(builder.String(), "-")
	if name == "" {
		name = "printer"
	}
	return name + ".ssb"
}
