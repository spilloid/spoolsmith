package main

import (
	"fmt"
	"strings"

	"github.com/spilloid/spoolsmith/internal/bundle"
	"github.com/spilloid/spoolsmith/internal/install"
)

func bulkCopyInventoryText(queues []install.InstalledQueue) string {
	var text strings.Builder
	copyable := 0
	for _, q := range queues {
		if q.Copyable() {
			copyable++
		}
	}
	fmt.Fprintf(&text, "%d printers can be copied; %d will be skipped.\r\nThe current Windows inventory is read again when copying starts.\r\n\r\n", copyable, len(queues)-copyable)
	for _, q := range queues {
		if reason := q.CopyBlockedReason(); reason != "" {
			fmt.Fprintf(&text, "Skip: %s\r\n  %s\r\n\r\n", q.PrinterName, reason)
		} else {
			fmt.Fprintf(&text, "%s -> %s\r\n  %s | %s\r\n\r\n", q.PrinterName, bundle.FileName(q.PrinterName), q.HostAddress, q.DriverName)
		}
	}
	return text.String()
}

func bulkCopyResultText(result bundle.AllResult, includeDriver bool) string {
	var text strings.Builder
	fmt.Fprintf(&text, "Folder: %s\r\n\r\n", result.OutputDir)
	for _, q := range result.Queues {
		switch q.Status {
		case "written":
			fmt.Fprintf(&text, "Copied: %s\r\n  %s\r\n", q.Name, q.Bundle)
			if q.Reason != "" {
				fmt.Fprintf(&text, "  %s\r\n", q.Reason)
			}
			text.WriteString("\r\n")
		case "skipped":
			fmt.Fprintf(&text, "Skipped: %s\r\n  %s\r\n\r\n", q.Name, q.Reason)
		default:
			fmt.Fprintf(&text, "Not copied: %s\r\n  %s\r\n\r\n", q.Name, q.Reason)
		}
	}
	if result.Written > 0 {
		if !includeDriver {
			text.WriteString("Drivers were not included. The other PC must already have the exact drivers installed.\r\n\r\n")
		}
		text.WriteString("Take the copied .ssb files to the other PC. Use Add a printer > Open a copied printer (.ssb) to review and apply each printer.\r\n")
	} else {
		text.WriteString("No printer files were copied. Resolve the reasons above and try again.\r\n")
	}
	if result.Failed > 0 {
		text.WriteString("\r\nFor a failed printer, use Copy to a file to retry it with a different filename or options. Existing files are never replaced.")
	}
	return text.String()
}
