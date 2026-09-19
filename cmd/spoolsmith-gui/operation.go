package main

import (
	"fmt"
	"strings"

	"github.com/spilloid/spoolsmith/internal/bundle"
	"github.com/spilloid/spoolsmith/internal/install"
)

// operationKind is what the review screen is about to do.
type operationKind string

const (
	opInstall   operationKind = "install"
	opConfigure operationKind = "configure"
	opRemove    operationKind = "remove"
	opApply     operationKind = "apply"
	opRepoint   operationKind = "repoint"
)

// operation is one reviewable intention, assembled by whichever screen the
// operator started from.
//
// It replaces the review screen's radio buttons. Those asked the operator to
// restate a decision they had already made by choosing a button elsewhere, and
// they made one text field mean three different things depending on which
// radio was selected -- a profile path, a queue name, or an IP address. Here
// the originating screen states the whole intention once, and the review
// screen only has to show it and run it.
type operation struct {
	Kind operationKind
	// PrinterName is the queue this is about, when one is already known.
	PrinterName string
	// ProfilePath, BundlePath and Target are the inputs for the kinds that
	// need them; exactly which is required is asserted by Validate.
	ProfilePath string
	BundlePath  string
	Target      string
	// NewAddress is the destination for a repoint.
	NewAddress string
	// Offline skips the live identity check, and must be visible wherever the
	// operation is described.
	Offline        bool
	UpdateExisting bool
	// PurgeDriver applies to a removal only.
	PurgeDriver bool
	// ForceFamily applies to a catalog-driven install only, and is cleared for
	// every other kind so a stale advanced choice cannot leak into an
	// operation it does not apply to.
	ForceFamily string
	// IncludeDriver applies when copying a queue out to a bundle.
	IncludeDriver bool
}

// Title is the operation's name, used for buttons and dialog captions.
func (o operation) Title() string {
	switch o.Kind {
	case opInstall:
		return "Add printer"
	case opConfigure:
		return "Update printer"
	case opRemove:
		return "Remove printer"
	case opApply:
		return "Add printer from file"
	case opRepoint:
		return "Change address"
	}
	return "Review"
}

// Summary is one sentence naming exactly what will happen, shown on the review
// screen in place of the old mode radios.
func (o operation) Summary() string {
	name := quoted(o.PrinterName)
	var text string
	switch o.Kind {
	case opInstall:
		if name != "" {
			text = fmt.Sprintf("Add printer %s at %s to this PC.", name, shownOr(o.Target, "the saved address"))
		} else {
			text = fmt.Sprintf("Add the printer at %s to this PC.", shownOr(o.Target, "the chosen address"))
		}
	case opConfigure:
		text = fmt.Sprintf("Update printer %s on this PC to match its saved settings.", shownOr(name, "the saved printer"))
	case opRemove:
		text = fmt.Sprintf("Remove printer %s from this PC.", shownOr(name, "the selected printer"))
		if o.PurgeDriver {
			text += " Its driver is also removed if nothing else uses it."
		}
	case opApply:
		text = fmt.Sprintf("Set up printer %s on this PC from %s.", shownOr(name, "the printer in this file"), shownOr(o.BundlePath, "the chosen file"))
	case opRepoint:
		text = fmt.Sprintf("Point printer %s at %s instead. Its name and driver stay the same.", shownOr(name, "the selected printer"), shownOr(o.NewAddress, "a new address"))
	default:
		return ""
	}
	if o.Kind == opApply && o.UpdateExisting {
		text += " An existing queue will be updated to match the file."
	}
	if o.Offline {
		text += " The printer will not be contacted, so its identity cannot be checked."
	}
	return text
}

// Validate reports why this operation cannot be reviewed yet, in the words the
// operator needs to fix it.
func (o operation) Validate() error {
	switch o.Kind {
	case opInstall:
		if strings.TrimSpace(o.ProfilePath) == "" && strings.TrimSpace(o.Target) == "" {
			return fmt.Errorf("choose a saved printer or enter the printer's IP address first")
		}
	case opConfigure:
		if strings.TrimSpace(o.ProfilePath) == "" {
			return fmt.Errorf("updating a printer needs its saved settings; choose a saved printer first")
		}
	case opRemove:
		if strings.TrimSpace(o.PrinterName) == "" && strings.TrimSpace(o.ProfilePath) == "" {
			return fmt.Errorf("choose the printer to remove first")
		}
	case opApply:
		if strings.TrimSpace(o.BundlePath) == "" {
			return fmt.Errorf("choose the printer file to set up from first")
		}
	case opRepoint:
		if strings.TrimSpace(o.PrinterName) == "" {
			return fmt.Errorf("choose the printer to move first")
		}
		if strings.TrimSpace(o.NewAddress) == "" {
			return fmt.Errorf("enter the printer's new IP address")
		}
	default:
		return fmt.Errorf("choose what you want to do first")
	}
	return nil
}

// Mutates reports whether this operation changes Windows, which is what
// decides if administrator rights are needed.
func (o operation) Mutates() bool { return o.Kind != "" }

// normalized clears inputs that do not apply to this kind, so an advanced
// option set for one operation can never reach another.
func (o operation) normalized() operation {
	if o.Kind == opRemove || o.Kind == opRepoint {
		o.Offline = false
	}
	if o.Kind != opInstall {
		o.ForceFamily = ""
	}
	if o.Kind != opRemove {
		o.PurgeDriver = false
	}
	if o.Kind != opRepoint {
		o.NewAddress = ""
	}
	if o.Kind != opApply {
		o.UpdateExisting = false
		o.BundlePath = ""
	}
	if o.ProfilePath != "" {
		// A profile supplies the family, so a catalog override is meaningless.
		o.ForceFamily = ""
	}
	return o
}

// queueRow renders one installed queue for the This PC list.
//
// A queue that cannot be copied is marked rather than hidden: the operator
// needs to know it exists, and the reason belongs beside it.
func queueRow(q install.InstalledQueue) string {
	marker := "   "
	if !q.Copyable() {
		marker = " ! "
	}
	where := strings.TrimSpace(q.HostAddress)
	if where == "" {
		where = q.PortName
	} else if q.ProtocolName != "" {
		where = fmt.Sprintf("%s  %s/%d", where, q.ProtocolName, q.PortNumber)
	}
	return fmt.Sprintf("%s%s  —  %s  [%s]", marker, q.PrinterName, where, q.DriverName)
}

// queueDetail is the description shown beside the list when one queue is
// selected.
func queueDetail(q install.InstalledQueue) string {
	lines := []string{
		q.PrinterName,
		"",
		"Address: " + shownOr(q.HostAddress, "(not a network port)"),
		"Port: " + q.PortName,
		"Driver: " + q.DriverName,
	}
	if q.ProtocolName != "" {
		lines = append(lines, fmt.Sprintf("Connection: %s on TCP %d", q.ProtocolName, q.PortNumber))
	}
	if q.Shared {
		lines = append(lines, "Shared with other computers: yes")
	}
	lines = append(lines, "")
	if reason := q.CopyBlockedReason(); reason != "" {
		lines = append(lines, "This printer cannot be copied to another PC:", reason)
	} else {
		lines = append(lines, "This printer can be copied to another PC.")
	}
	return strings.Join(lines, "\r\n")
}

// bundleFileName derives a suggested file name from a queue name, matching the
// CLI's own default so the two produce the same name for the same printer.
func bundleFileName(queueName string) string {
	return bundle.FileName(queueName)
}

func quoted(value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	return "\"" + value + "\""
}

func shownOr(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
