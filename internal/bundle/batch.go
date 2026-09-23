package bundle

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spilloid/spoolsmith/internal/install"
)

// AllOptions describes a copy of every installed, reproducible printer queue
// into one printer set (.zip). Drivers are preferred, exactly as for Create;
// SettingsOnly is the explicit opt-out.
type AllOptions struct {
	SetPath      string
	SettingsOnly bool
	Note         string
	CreatedBy    string
	SourceHost   string
	Progress     func(queueName, step string)
}

// AllResult reports every queue considered by CreateAll, including queues that
// could not be copied and those left unstarted when the operation was canceled.
// SetPath is the set file actually written, empty when none was.
type AllResult struct {
	SetPath   string        `json:"set_path"`
	Requested int           `json:"requested"`
	Written   int           `json:"written"`
	Skipped   int           `json:"skipped"`
	Failed    int           `json:"failed"`
	Queues    []QueueResult `json:"queues"`
}

// QueueResult is one queue's outcome. Status is written, skipped, or error.
// Member is the queue's file name inside the set. Reason carries why the
// driver was not included and/or the unconfirmed-identity notice for a
// written queue, or why the queue was skipped or failed.
type QueueResult struct {
	Name           string `json:"name"`
	Status         string `json:"status"`
	Member         string `json:"member,omitempty"`
	Reason         string `json:"reason,omitempty"`
	DriverIncluded bool   `json:"driver_included"`
}

// CreateAll copies every copyable queue with the same operation as a single
// Create, then writes them all as ONE printer set at SetPath. Each queue is
// independent: an unsupported queue is skipped, and a failed copy does not
// stop subsequent copies. Ordinary per-queue errors are reported in AllResult,
// while setup errors and cancellation are returned. When nothing was copied,
// or the operation was canceled, no set file is written and queues that had
// been copied are reported as not saved -- Written always counts members of
// the set file on disk.
func CreateAll(ctx context.Context, env install.Environment, collect Collector, opts AllOptions) (AllResult, error) {
	result := AllResult{Queues: []QueueResult{}}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	setPath := strings.TrimSpace(opts.SetPath)
	if setPath == "" {
		return result, errors.New("copy: choose where to save the printer set (.zip)")
	}
	if !strings.EqualFold(filepath.Ext(setPath), SetExt) {
		return result, fmt.Errorf("copy: %s must be a .zip file; all printers are saved together as one printer set", setPath)
	}
	if _, err := os.Stat(setPath); err == nil {
		return result, fmt.Errorf("copy: %s already exists; choose a different, unused file name", setPath)
	} else if !os.IsNotExist(err) {
		return result, fmt.Errorf("copy: check %s: %w", setPath, err)
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
	// unsave reports queues that were copied but never reached a set file.
	unsave := func(why string) {
		for i := range result.Queues {
			if result.Queues[i].Status == "written" {
				result.Queues[i].Status = "error"
				result.Queues[i].Reason = "not saved: " + why
				result.Queues[i].Member = ""
				result.Written--
				result.Failed++
			}
		}
	}
	if err := ctx.Err(); err != nil {
		markRemaining(0, err)
		return result, err
	}

	work, err := os.MkdirTemp("", "spoolsmith-copy-all-")
	if err != nil {
		markRemaining(0, err)
		return result, fmt.Errorf("copy --all: %w", err)
	}
	defer os.RemoveAll(work)

	// Windows file names are case-insensitive, so names are reserved
	// case-insensitively; a collision gets a deterministic suffix rather
	// than failing the queue.
	reserved := make(map[string]bool, len(queues))
	var members []SetMember
	for index, queue := range queues {
		if err := ctx.Err(); err != nil {
			markRemaining(index, err)
			unsave("the copy was canceled before the printer set was written")
			return result, err
		}
		outcome := QueueResult{Name: queue.PrinterName}
		if reason := queue.CopyBlockedReason(); reason != "" {
			outcome.Status, outcome.Reason = "skipped", reason
			result.Skipped++
			result.Queues = append(result.Queues, outcome)
			continue
		}
		name := uniqueMemberName(FileName(queue.PrinterName), reserved)
		path := filepath.Join(work, name)
		created, err := Create(ctx, env, collect, CreateOptions{
			QueueName: queue.PrinterName, Path: path, SettingsOnly: opts.SettingsOnly,
			Note: opts.Note, CreatedBy: opts.CreatedBy, SourceHost: opts.SourceHost,
			Progress: func(step string) {
				if opts.Progress != nil {
					opts.Progress(queue.PrinterName, step)
				}
			},
		})
		if created.ExportDir != "" {
			// The payload now lives inside the member; the working export
			// is not worth keeping per queue in a batch.
			os.RemoveAll(created.ExportDir)
		}
		if err != nil {
			outcome.Status, outcome.Reason = "error", err.Error()
			result.Failed++
			result.Queues = append(result.Queues, outcome)
			continue
		}
		outcome.Status, outcome.Member = "written", name
		outcome.DriverIncluded = created.Manifest.Driver != nil
		var reasons []string
		if created.DriverNotIncluded != "" {
			reasons = append(reasons, "Driver not included: "+created.DriverNotIncluded+".")
		}
		if created.Manifest.Profile.Evidence.Provenance != "captured" {
			reasons = append(reasons, UnconfirmedIdentityNotice)
		}
		outcome.Reason = strings.Join(reasons, " ")
		result.Written++
		members = append(members, SetMember{Name: name, Path: path})
		result.Queues = append(result.Queues, outcome)
	}
	if err := ctx.Err(); err != nil {
		unsave("the copy was canceled before the printer set was written")
		return result, err
	}
	if len(members) == 0 {
		return result, nil
	}
	if err := WriteSet(setPath, opts.Note, members); err != nil {
		unsave("the printer set could not be written")
		return result, fmt.Errorf("copy --all: write %s: %w", setPath, err)
	}
	result.SetPath = setPath
	return result, nil
}

// uniqueMemberName reserves name, or the first free "name-N.ssb" (N from 2)
// when it is taken, comparing case-insensitively.
func uniqueMemberName(name string, reserved map[string]bool) string {
	candidate := name
	stem := strings.TrimSuffix(name, filepath.Ext(name))
	for n := 2; reserved[strings.ToLower(candidate)]; n++ {
		candidate = stem + "-" + strconv.Itoa(n) + filepath.Ext(name)
	}
	reserved[strings.ToLower(candidate)] = true
	return candidate
}

// FileName derives the same portable printer-file name for both front ends.
// Distinct queue names can produce the same name; CreateAll gives the later
// ones a numbered suffix, and Write never replaces an existing file.
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
	if len(name) > 150 {
		name = strings.TrimRight(name[:150], "-")
	}
	// A queue named like a Windows device (CON, LPT1...) would otherwise
	// produce a file name a printer set refuses to carry.
	if validateMemberName(name+".ssb") != nil {
		name = "printer-" + name
	}
	return name + ".ssb"
}
