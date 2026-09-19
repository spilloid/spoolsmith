//go:build windows

package main

import (
	"context"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spilloid/spoolsmith/internal/bundle"
	"github.com/spilloid/spoolsmith/internal/install"
	"github.com/tailscale/walk"
	. "github.com/tailscale/walk/declarative"
)

type thisPCUI struct {
	queueList    *walk.ListBox
	queueDetail  *walk.TextEdit
	queueStatus  *walk.Label
	queueRefresh *walk.PushButton
	copyBtn      *walk.PushButton
	repointBtn   *walk.PushButton
	removeBtn    *walk.PushButton
	queues       []install.InstalledQueue
	queuesBusy   bool
}

// thisPCPage answers "what does this computer actually have?" -- the question
// an operator standing at someone else's desk asks first, and the one the app
// previously could not answer at all. It reads Windows' own inventory rather
// than SpoolSmith's saved files, so what it shows is the truth even for
// printers SpoolSmith never set up.
func thisPCPage(a *app) TabPage {
	return TabPage{Title: "This PC", Background: SolidColorBrush{Color: walk.RGB(250, 251, 253)}, Layout: pagePadding(), Children: []Widget{
		heading("Printers on this PC"),
		Label{Text: "What Windows has set up right now. Choose one to copy it to another PC, move it to a new address, or remove it."},
		Composite{Layout: row(), Children: []Widget{
			PushButton{AssignTo: &a.queueRefresh, Text: "Refresh", OnClicked: a.onRefreshQueues},
			Label{AssignTo: &a.queueStatus, Text: "Reading this PC's printers..."},
			HSpacer{},
		}},
		HSplitter{Children: []Widget{
			ListBox{AssignTo: &a.queueList, MinSize: Size{Width: 330, Height: 150}, Accessibility: name("thispc-list")},
			TextEdit{AssignTo: &a.queueDetail, ReadOnly: true, VScroll: true, MinSize: Size{Width: 260, Height: 150}, Accessibility: name("thispc-detail")},
		}},
		Composite{Layout: row(), Children: []Widget{
			PushButton{AssignTo: &a.copyBtn, Text: "Copy to a file...", Enabled: false, OnClicked: a.onCopyQueue},
			PushButton{AssignTo: &a.repointBtn, Text: "Change address...", Enabled: false, OnClicked: a.onRepointQueue},
			HSpacer{},
			PushButton{AssignTo: &a.removeBtn, Text: "Remove printer...", Enabled: false, OnClicked: a.onRemoveQueue},
		}},
	}}
}

func (a *app) initializeThisPC() {
	a.queueList.CurrentIndexChanged().Attach(a.updateQueueActions)
	a.onRefreshQueues()
}

func (a *app) onRefreshQueues() {
	if a.queuesBusy {
		return
	}
	a.queuesBusy = true
	a.queueRefresh.SetEnabled(false)
	a.queueStatus.SetText("Reading this PC's printers...")
	start := time.Now()
	go func() {
		queues, err := install.ListPrinters(context.Background(), a.env)
		a.log("gui", "printers", nil, statusOf(err), err, start)
		a.mw.Synchronize(func() {
			a.queuesBusy = false
			a.queueRefresh.SetEnabled(true)
			if err != nil {
				a.queues = nil
				a.queueList.SetModel([]string{})
				a.queueStatus.SetText("Could not read this PC's printers.")
				a.queueDetail.SetText(friendlyOperationError(err.Error()))
				a.updateQueueActions()
				return
			}
			a.queues = queues
			labels := make([]string, 0, len(queues))
			for _, queue := range queues {
				labels = append(labels, queueRow(queue))
			}
			a.queueList.SetModel(labels)
			switch len(queues) {
			case 0:
				a.queueStatus.SetText("This PC has no printers set up yet.")
				a.queueDetail.SetText("Use Add a printer to set one up.")
			case 1:
				a.queueStatus.SetText("1 printer on this PC.")
			default:
				a.queueStatus.SetText(fmt.Sprintf("%d printers on this PC.", len(queues)))
			}
			if len(queues) > 0 {
				a.queueList.SetCurrentIndex(0)
			}
			a.updateQueueActions()
		})
	}()
}

func (a *app) selectedQueue() (install.InstalledQueue, bool) {
	index := a.queueList.CurrentIndex()
	if index < 0 || index >= len(a.queues) {
		return install.InstalledQueue{}, false
	}
	return a.queues[index], true
}

func (a *app) updateQueueActions() {
	queue, ok := a.selectedQueue()
	a.copyBtn.SetEnabled(ok && queue.Copyable() && !a.queuesBusy)
	a.repointBtn.SetEnabled(ok && !a.queuesBusy)
	a.removeBtn.SetEnabled(ok && !a.queuesBusy)
	if !ok {
		if len(a.queues) > 0 {
			a.queueDetail.SetText("Choose a printer to see its details.")
		}
		return
	}
	a.queueDetail.SetText(queueDetail(queue))
}

// onCopyQueue writes the selected queue to a file another PC can open.
//
// This does not go through Review, because it changes nothing on this PC: it
// reads the queue and writes a file. Review exists to gate changes to Windows,
// and routing a read-only export through it would teach operators that the
// confirmation step is a formality.
func (a *app) onCopyQueue() {
	queue, ok := a.selectedQueue()
	if !ok || !queue.Copyable() {
		return
	}
	var dialog *walk.Dialog
	var pathEdit, noteEdit *walk.LineEdit
	var includeDriver *walk.CheckBox
	var statusLabel *walk.Label
	var copyButton, cancelButton *walk.PushButton
	var running bool

	suggested := filepath.Join(defaultCopyDirectory(), bundleFileName(queue.PrinterName))

	err := (Dialog{
		AssignTo: &dialog, Title: "Copy " + queue.PrinterName,
		MinSize: Size{Width: 560, Height: 300}, Layout: pagePadding(),
		CancelButton: &cancelButton,
		Children: []Widget{
			Label{Text: "This saves the printer's settings to one file. Copy that file to the other PC and open it there."},
			Composite{Layout: formGrid(3), Children: []Widget{
				Label{Text: "Save to:"},
				LineEdit{AssignTo: &pathEdit, Text: suggested, Accessibility: name("copy-path")},
				PushButton{Text: "Browse...", OnClicked: func() {
					picker := walk.FileDialog{Title: "Save printer file", Filter: "Printer files (*.ssb)|*.ssb|All files (*.*)|*.*", FilePath: pathEdit.Text()}
					if ok, err := picker.ShowSave(dialog); err != nil {
						showErr(dialog, "Save printer file", err)
					} else if ok {
						pathEdit.SetText(picker.FilePath)
					}
				}},
				Label{Text: "Note (optional):"},
				LineEdit{AssignTo: &noteEdit, CueBanner: "For example: front desk, replaced 2026", Accessibility: name("copy-note"), ColumnSpan: 2},
			}},
			CheckBox{AssignTo: &includeDriver, Text: "Include the driver, so the other PC does not need it already (needs administrator)", Checked: true},
			Label{AssignTo: &statusLabel, Text: "The printer is checked while copying, so the other PC can confirm it is the same one."},
			VSpacer{},
			Composite{Layout: row(), Children: []Widget{
				HSpacer{},
				PushButton{AssignTo: &cancelButton, Text: "Cancel", OnClicked: func() { dialog.Cancel() }},
				PushButton{AssignTo: &copyButton, Text: "Copy", OnClicked: func() {
					if running {
						return
					}
					path := strings.TrimSpace(pathEdit.Text())
					if path == "" {
						statusLabel.SetText("Choose where to save the file first.")
						return
					}
					running = true
					copyButton.SetEnabled(false)
					cancelButton.SetEnabled(false)
					statusLabel.SetText("Starting...")
					opts := bundle.CreateOptions{
						QueueName:     queue.PrinterName,
						Path:          path,
						Note:          strings.TrimSpace(noteEdit.Text()),
						IncludeDriver: includeDriver.Checked(),
						CreatedBy:     "SpoolSmith desktop",
						SourceHost:    hostName(),
						Progress: func(step string) {
							a.mw.Synchronize(func() { statusLabel.SetText(step) })
						},
					}
					start := time.Now()
					go func() {
						result, err := bundle.Create(context.Background(), a.env, a.collector(), opts)
						a.log("gui", "copy", []string{queue.PrinterName, path}, statusOf(err), err, start)
						a.mw.Synchronize(func() {
							running = false
							copyButton.SetEnabled(true)
							cancelButton.SetEnabled(true)
							if err != nil {
								statusLabel.SetText("Could not copy this printer.")
								showErr(dialog, "Copy printer", fmt.Errorf("%s", friendlyOperationError(err.Error())))
								return
							}
							walk.MsgBox(dialog, "Printer copied", copySuccessMessage(path, result.Manifest), walk.MsgBoxIconInformation)
							dialog.Accept()
						})
					}()
				}},
			}},
		},
	}).Create(a.mw)
	if err != nil {
		showErr(a.mw, "Copy printer", err)
		return
	}
	dialog.Closing().Attach(func(canceled *bool, _ walk.CloseReason) {
		if running {
			*canceled = true
		}
	})
	a.runDialog(dialog)
}

// copySuccessMessage tells the operator what they now have and what to do with
// it, including the consequence of having left the driver out.
func copySuccessMessage(path string, manifest bundle.Manifest) string {
	text := fmt.Sprintf("Saved %s\r\n\r\nPrinter: %s\r\nAddress: %s\r\nDriver: %s\r\n\r\n",
		path, manifest.Profile.PrinterName, manifest.Profile.Target, manifest.Profile.DriverName)
	if manifest.Driver == nil {
		text += "The driver was not included, so the other PC must already have this driver installed.\r\n\r\n"
	} else {
		text += fmt.Sprintf("The driver is included (%d files), so the other PC does not need it beforehand.\r\n\r\n", len(manifest.Driver.Files))
	}
	return text + "Copy this file to the other PC, open SpoolSmith there, and choose Add a printer > Open a copied printer (.ssb)."
}

func (a *app) onRepointQueue() {
	queue, ok := a.selectedQueue()
	if !ok {
		return
	}
	var dialog *walk.Dialog
	var addressEdit *walk.LineEdit
	var statusLabel *walk.Label
	var okButton, cancelButton *walk.PushButton

	err := (Dialog{
		AssignTo: &dialog, Title: "Change address for " + queue.PrinterName,
		MinSize: Size{Width: 520, Height: 220}, Layout: pagePadding(),
		DefaultButton: &okButton, CancelButton: &cancelButton,
		Children: []Widget{
			Label{Text: "Use this when the printer itself moved to a different address. The printer keeps its name and driver, so anyone who already prints to it keeps working."},
			Composite{Layout: row(), Children: []Widget{
				Label{Text: "Currently:"},
				Label{Text: shownOr(queue.HostAddress, queue.PortName)},
			}},
			Composite{Layout: row(), Children: []Widget{
				Label{Text: "New address:"},
				LineEdit{AssignTo: &addressEdit, CueBanner: "For example 192.168.1.75", Accessibility: name("repoint-address")},
			}},
			Label{AssignTo: &statusLabel, Text: "You will see exactly what changes before anything happens."},
			VSpacer{},
			Composite{Layout: row(), Children: []Widget{
				HSpacer{},
				PushButton{AssignTo: &cancelButton, Text: "Cancel", OnClicked: func() { dialog.Cancel() }},
				PushButton{AssignTo: &okButton, Text: "Review change", OnClicked: func() {
					address := strings.TrimSpace(addressEdit.Text())
					if _, err := netip.ParseAddr(address); err != nil {
						statusLabel.SetText("Enter the printer's new IP address, for example 192.168.1.75.")
						addressEdit.SetFocus()
						return
					}
					if strings.EqualFold(address, strings.TrimSpace(queue.HostAddress)) {
						statusLabel.SetText("That is the address it already uses.")
						return
					}
					dialog.Accept()
					a.startOperation(operation{
						Kind:        opRepoint,
						PrinterName: queue.PrinterName,
						NewAddress:  address,
					})
				}},
			}},
		},
	}).Create(a.mw)
	if err != nil {
		showErr(a.mw, "Change address", err)
		return
	}
	a.runDialog(dialog)
}

func (a *app) onRemoveQueue() {
	queue, ok := a.selectedQueue()
	if !ok {
		return
	}
	a.startOperation(operation{Kind: opRemove, PrinterName: queue.PrinterName})
}

// defaultCopyDirectory prefers the operator's Desktop, because a file meant to
// be carried to another PC is easier to find there than beside the executable.
func defaultCopyDirectory() string {
	if home, err := os.UserHomeDir(); err == nil {
		desktop := filepath.Join(home, "Desktop")
		if info, err := os.Stat(desktop); err == nil && info.IsDir() {
			return desktop
		}
		return home
	}
	return "."
}

func statusOf(err error) string {
	if err != nil {
		return "error"
	}
	return "success"
}
