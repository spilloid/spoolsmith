package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/spilloid/spoolsmith/internal/install"
)

// runPrinters reports the queues this machine already has.
//
// This is deliberately a read-only wrapper over Windows' own Get-Printer and
// Get-PrinterPort rather than a SpoolSmith inventory: the question it answers
// is "what does this PC actually have right now", and the only trustworthy
// answer is Windows'. It is the command an operator runs first on someone
// else's machine, before knowing any queue's exact name.
func runPrinters(ctx context.Context, args []string, stdout, stderr io.Writer, app application) int {
	jsonOnly := false
	copyableOnly := false
	for _, arg := range args {
		switch arg {
		case "--json":
			jsonOnly = true
		case "--copyable":
			copyableOnly = true
		default:
			return usageError(stdout, stderr, "printers", fmt.Errorf("unknown option %q", arg))
		}
	}

	queues, err := install.ListPrinters(ctx, app.environment)
	if err != nil {
		return commandError(stdout, stderr, "printers", err, int(install.ExitGeneralError))
	}
	if copyableOnly {
		kept := make([]install.InstalledQueue, 0, len(queues))
		for _, queue := range queues {
			if queue.Copyable() {
				kept = append(kept, queue)
			}
		}
		queues = kept
	}
	if !jsonOnly {
		writeQueueTable(stderr, queues)
	}
	return encodeSuccess(stdout, stderr, "printers", queues)
}

// writeQueueTable renders the listing for a human, numbered so the numbers
// match what an interactive selection prompt offers.
func writeQueueTable(writer io.Writer, queues []install.InstalledQueue) {
	if len(queues) == 0 {
		fmt.Fprintln(writer, "This PC has no printer queues installed.")
		return
	}
	width := 0
	for _, queue := range queues {
		if len(queue.PrinterName) > width {
			width = len(queue.PrinterName)
		}
	}
	fmt.Fprintf(writer, "Printers installed on this PC (%d)\n", len(queues))
	for index, queue := range queues {
		target := queue.HostAddress
		if strings.TrimSpace(target) == "" {
			target = queue.PortName
		} else if queue.ProtocolName != "" {
			target = fmt.Sprintf("%s %s/%d", target, queue.ProtocolName, queue.PortNumber)
		}
		marker := "  "
		if !queue.Copyable() {
			marker = "! "
		}
		fmt.Fprintf(writer, "%s%2d. %-*s  %s  [%s]\n", marker, index+1, width, queue.PrinterName, target, queue.DriverName)
	}
	blocked := 0
	for _, queue := range queues {
		if !queue.Copyable() {
			blocked++
		}
	}
	if blocked > 0 {
		fmt.Fprintf(writer, "\n%d queue(s) marked ! cannot be copied to another PC:\n", blocked)
		for _, queue := range queues {
			if reason := queue.CopyBlockedReason(); reason != "" {
				fmt.Fprintf(writer, "  %s: %s\n", queue.PrinterName, reason)
			}
		}
	}
	fmt.Fprintln(writer, "\nCopy one to another PC with: spoolsmith copy <name> <bundle-file> --include-driver")
}

// selectInstalledQueue resolves the queue to copy when the operator did not
// name one.
//
// Nothing here guesses. With a terminal it offers a numbered list and takes
// one answer; without one — a scheduled task, an RMM, an Intune package — it
// fails with the command that would have listed the choices, because a
// non-interactive caller that did not name a queue has not decided which
// machine's printer it meant, and picking one for it would be a mutation
// nobody reviewed.
func selectInstalledQueue(ctx context.Context, input io.Reader, stderr io.Writer, app application) (string, error) {
	if !app.inputTerminal {
		return "", errors.New("no queue named, and stdin is not a terminal; name the queue to copy, or run `spoolsmith printers` to list them")
	}
	queues, err := install.ListPrinters(ctx, app.environment)
	if err != nil {
		return "", err
	}
	copyable := make([]install.InstalledQueue, 0, len(queues))
	for _, queue := range queues {
		if queue.Copyable() {
			copyable = append(copyable, queue)
		}
	}
	if len(copyable) == 0 {
		if len(queues) == 0 {
			return "", errors.New("this PC has no printer queues installed")
		}
		writeQueueTable(stderr, queues)
		return "", errors.New("none of this PC's queues can be copied; see the reasons above")
	}
	writeQueueTable(stderr, copyable)
	fmt.Fprintf(stderr, "\nWhich printer do you want to copy? [1-%d, or q to cancel]: ", len(copyable))
	answer, err := readLine(input)
	if err != nil {
		return "", err
	}
	answer = strings.TrimSpace(answer)
	if answer == "" || strings.EqualFold(answer, "q") {
		return "", errors.New("cancelled")
	}
	choice, convErr := strconv.Atoi(answer)
	if convErr != nil || choice < 1 || choice > len(copyable) {
		return "", fmt.Errorf("%q is not one of the offered numbers (1-%d)", answer, len(copyable))
	}
	selected := copyable[choice-1]
	fmt.Fprintf(stderr, "Copying %q.\n", selected.PrinterName)
	return selected.PrinterName, nil
}

// readLine reads one answer from the operator.
func readLine(input io.Reader) (string, error) {
	reader := bufio.NewReader(input)
	answer, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	return strings.TrimSpace(answer), nil
}
