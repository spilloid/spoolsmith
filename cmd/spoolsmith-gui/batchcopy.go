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
	fmt.Fprintf(&text, "%s can be copied into the set; %d will be skipped.\r\nThe current Windows inventory is read again when copying starts.\r\n\r\n", countPrinters(copyable), len(queues)-copyable)
	for _, q := range queues {
		if reason := q.CopyBlockedReason(); reason != "" {
			fmt.Fprintf(&text, "Skip: %s\r\n  %s\r\n\r\n", q.PrinterName, reason)
		} else {
			fmt.Fprintf(&text, "%s -> %s\r\n  %s | %s\r\n\r\n", q.PrinterName, bundle.FileName(q.PrinterName), q.HostAddress, q.DriverName)
		}
	}
	return text.String()
}

func bulkCopyResultText(result bundle.AllResult) string {
	var text strings.Builder
	if result.Written > 0 {
		fmt.Fprintf(&text, "Printer set: %s\r\n\r\n", result.SetPath)
	}
	withoutDriver := 0
	for _, q := range result.Queues {
		switch q.Status {
		case "written":
			driver := "driver included"
			if !q.DriverIncluded {
				driver = "settings only"
				withoutDriver++
			}
			fmt.Fprintf(&text, "Copied: %s\r\n  %s, %s\r\n", q.Name, q.Member, driver)
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
		if withoutDriver > 0 {
			fmt.Fprintf(&text, "%s copied without a driver. The other PC must already have those drivers installed.\r\n\r\n", countPrinters(withoutDriver))
		}
		text.WriteString("Take the set to the other PC and choose Add a printer > Open a printer file to review and apply each printer.\r\n")
	} else {
		text.WriteString("No printers were copied, so no set was saved. Resolve the reasons above and try again.\r\n")
	}
	if result.Failed > 0 {
		text.WriteString("\r\nFor a failed printer, use Copy to a file to retry it with a different filename or options. Existing files are never replaced.")
	}
	return text.String()
}

// countPrinters reads "1 printer" or "3 printers".
func countPrinters(n int) string {
	if n == 1 {
		return "1 printer"
	}
	return fmt.Sprintf("%d printers", n)
}
