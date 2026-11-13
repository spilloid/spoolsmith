package main

import "strings"

// friendlyOperationError translates the handful of errors an operator hits
// often into plain guidance, with the original message kept underneath as a
// detail line -- translated, not replaced, so a case this doesn't recognize
// (or a report sent along with a ticket) still carries the real text.
func friendlyOperationError(message string) string {
	lead, ok := friendlyErrorLead(message)
	if !ok {
		return message
	}
	return lead + "\r\n\r\nDetails: " + message
}

func friendlyErrorLead(message string) (string, bool) {
	switch {
	// A failed step carries Windows' own reason, which can contain words the
	// cases below key on ("already exists", "administrator"). What matters
	// first is that the run stopped part-way, so this case is checked first.
	case strings.Contains(message, "install: step ") || strings.Contains(message, "repoint: step "):
		return "A step failed part-way. Some earlier steps may already have been applied, such as a port or driver. Open This PC to see what is there before trying again; adding the same printer again reuses what already exists.", true
	case strings.Contains(message, "remove: step "):
		return "A step failed part-way. Some earlier steps may already have been applied, such as removing the printer, which can leave its port or driver behind. Open This PC to see what is left before trying again.", true
	case strings.Contains(message, "administrator") || strings.Contains(message, "Administrator"):
		return "Administrator access is needed. Use the button with the shield to continue as administrator; Windows will ask first.", true
	// The check itself failed (usually the Print Spooler). Its message ends
	// with the "driver not found" guidance, so it must be matched first or it
	// reads as a missing driver.
	case strings.Contains(message, "driver presence check failed"):
		return "Windows could not verify the printer driver, so nothing was changed. Check that the Print Spooler service is running (services.msc) and that its driver files can be read, then try again.", true
	case strings.Contains(message, "driver not found"):
		return "The required driver is missing or incomplete for this computer. Install or repair the exact compatible Windows driver, then check again.", true
	case strings.Contains(message, "already exists"):
		return "A file with that name already exists. Choose a different name or location -- nothing here is ever overwritten automatically.", true
	case strings.Contains(message, "changed: saved"):
		return "This printer answered, but its identity does not match what was saved. Confirm it's still the same device -- a different printer may now be at this address -- before recapturing.", true
	case strings.Contains(message, "collect evidence") || strings.Contains(message, "did not identify itself") || strings.Contains(message, "could not be contacted"):
		return "The printer could not be reached. Check that it's powered on and connected to the network, then try again.", true
	case strings.Contains(message, "native driver change left the queue"):
		return "The printer was added, but Windows changed its driver and the previous one could not be put back, so the queue needs a look. Open this PC, check the printer prints, and if it doesn't, run Add printer again to review it.", true
	case strings.Contains(message, "bundle:") || strings.Contains(message, "zip:"):
		return "This file could not be read as a printer file. It may be damaged, or not a SpoolSmith printer file at all.", true
	default:
		return "", false
	}
}
