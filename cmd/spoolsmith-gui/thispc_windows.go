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
	queueTable   *walk.TableView
	queueModel   *queueTableModel
	queueDetail  *walk.Label
	queueStatus  *walk.Label
	queueRefresh *walk.PushButton
	copyBtn      *walk.PushButton
	repointBtn   *walk.PushButton
	removeBtn    *walk.PushButton
	queues       []install.InstalledQueue
	queuesBusy   bool
	clipBusy     bool
}

// queueTableModel shows copyable printers first, then the ones SpoolSmith
// can't reproduce, greyed with their reason. It keeps Windows' own order
// within each group.
type queueTableModel struct {
	walk.TableModelBase
	rows []install.InstalledQueue
}

func (m *queueTableModel) RowCount() int { return len(m.rows) }

func (m *queueTableModel) Value(row, col int) interface{} {
	q := m.rows[row]
	switch col {
	case 0:
		return q.PrinterName
	case 1:
		if host := strings.TrimSpace(q.HostAddress); host != "" {
			return host
		}
		return q.PortName
	case 2:
		return q.DriverName
	}
	if q.Copyable() {
		return "Can copy"
	}
	return "Can't copy"
}

func (m *queueTableModel) set(queues []install.InstalledQueue) {
	m.rows = m.rows[:0]
	for _, pass := range []bool{true, false} {
		for _, q := range queues {
			if q.Copyable() == pass {
				m.rows = append(m.rows, q)
			}
		}
	}
	m.PublishRowsReset()
}

// thisPCPage answers "what does this computer actually have?" -- the question
// an operator standing at someone else's desk asks first. It reads Windows'
// own inventory rather than SpoolSmith's saved files, so what it shows is the
// truth even for printers SpoolSmith never set up. It is the home screen: the
// first half of the app's one job, taking printers off this PC.
func thisPCPage(a *app) Composite {
	a.queueModel = &queueTableModel{}
	return contentPage(a, pageThisPC,
		heading("Printers on this PC"),
		hint("Select printers to copy them to another PC. To install one here, drop or paste its printer file anywhere in this window."),
		Composite{Layout: row(), Children: []Widget{
			Label{AssignTo: &a.queueStatus, Text: "Reading this PC's printers..."},
			HSpacer{},
			PushButton{AssignTo: &a.queueRefresh, Text: "Refresh", OnClicked: a.onRefreshQueues},
		}},
		TableView{
			AssignTo: &a.queueTable, Model: a.queueModel, MultiSelection: true,
			AlternatingRowBG: false, LastColumnStretched: true, NotSortableByHeaderClick: true,
			MinSize: Size{Height: 150}, Accessibility: name("thispc-list"),
			Columns: []TableViewColumn{
				{Title: "Printer", Width: 230},
				{Title: "Address", Width: 130},
				{Title: "Driver", Width: 250},
				{Title: "Copy", Width: 90},
			},
			StyleCell: func(style *walk.CellStyle) {
				if row := style.Row(); row >= 0 && row < len(a.queueModel.rows) && !a.queueModel.rows[row].Copyable() {
					style.TextColor = colorHint
				}
			},
			OnSelectedIndexesChanged: func() { a.updateQueueActions() },
			OnItemActivated:          func() { a.onCopySelected() },
		},
		Label{AssignTo: &a.queueDetail, Text: " ", TextColor: colorHint, EllipsisMode: EllipsisEnd},
		Composite{Layout: row(), Children: []Widget{
			PushButton{AssignTo: &a.copyBtn, Text: "Select printers to copy", Enabled: false, OnClicked: a.onCopySelected},
			HSpacer{},
			PushButton{AssignTo: &a.repointBtn, Text: "Change address...", Visible: false, OnClicked: a.onRepointQueue},
			PushButton{AssignTo: &a.removeBtn, Text: "Remove printer...", Visible: false, OnClicked: a.onRemoveQueue},
		}},
	)
}

func (a *app) initializeThisPC() {
	selectAll := walk.NewAction()
	selectAll.SetShortcut(walk.Shortcut{Modifiers: walk.ModControl, Key: walk.KeyA})
	selectAll.Triggered().Attach(a.selectAllCopyable)
	a.queueTable.ShortcutActions().Add(selectAll)
	a.buildQueueContextMenu()
	a.onRefreshQueues()
}

func (a *app) buildQueueContextMenu() {
	menu, err := walk.NewMenu()
	if err != nil {
		return
	}
	add := func(text string, handler walk.EventHandler) {
		action := walk.NewAction()
		action.SetText(text)
		action.Triggered().Attach(handler)
		menu.Actions().Add(action)
	}
	add("Copy to the clipboard\tCtrl+C", a.onCopyToClipboard)
	add("Save to a file...", a.onCopySelected)
	menu.Actions().Add(walk.NewSeparatorAction())
	add("Change address...", a.onRepointQueue)
	add("Remove printer...", a.onRemoveQueue)
	a.queueTable.SetContextMenu(menu)
}

func (a *app) onRefreshQueues() {
	if a.queuesBusy {
		return
	}
	a.queuesBusy = true
	a.queueRefresh.SetEnabled(false)
	a.updateQueueActions()
	setLabel(a.queueStatus, "Reading this PC's printers...")
	start := time.Now()
	go func() {
		queues, err := install.ListPrinters(context.Background(), a.env)
		a.log("gui", "printers", nil, statusOf(err), err, start)
		a.mw.Synchronize(func() {
			a.queuesBusy = false
			a.queueRefresh.SetEnabled(true)
			if err != nil {
				a.queues = nil
				a.queueModel.set(nil)
				setLabel(a.queueStatus, "Could not read this PC's printers.")
				a.queueDetail.SetText(friendlyOperationError(err.Error()))
				a.updateQueueActions()
				return
			}
			a.queues = queues
			a.queueModel.set(queues)
			copyable := 0
			for _, q := range queues {
				if q.Copyable() {
					copyable++
				}
			}
			switch {
			case len(queues) == 0:
				setLabel(a.queueStatus, "This PC has no printers set up yet.")
			case copyable == len(queues):
				setLabel(a.queueStatus, countPrinters(len(queues))+" on this PC.")
			default:
				setLabel(a.queueStatus, fmt.Sprintf("%s on this PC; %d can't be copied.", countPrinters(len(queues)), len(queues)-copyable))
			}
			if copyable > 0 {
				a.queueTable.SetSelectedIndexes([]int{0})
				a.queueTable.SetCurrentIndex(0)
			}
			a.updateQueueActions()
		})
	}()
}

// selectedQueues returns the selected rows, in table order.
func (a *app) selectedQueues() []install.InstalledQueue {
	if a.queueTable == nil {
		return nil
	}
	var selected []install.InstalledQueue
	for _, index := range a.queueTable.SelectedIndexes() {
		if index >= 0 && index < len(a.queueModel.rows) {
			selected = append(selected, a.queueModel.rows[index])
		}
	}
	return selected
}

func copyableOnly(queues []install.InstalledQueue) []install.InstalledQueue {
	var copyable []install.InstalledQueue
	for _, q := range queues {
		if q.Copyable() {
			copyable = append(copyable, q)
		}
	}
	return copyable
}

// selectedQueue is the one selected printer, for the single-printer actions.
func (a *app) selectedQueue() (install.InstalledQueue, bool) {
	selected := a.selectedQueues()
	if len(selected) != 1 {
		return install.InstalledQueue{}, false
	}
	return selected[0], true
}

func (a *app) selectAllCopyable() {
	var indexes []int
	for i, q := range a.queueModel.rows {
		if q.Copyable() {
			indexes = append(indexes, i)
		}
	}
	a.queueTable.SetSelectedIndexes(indexes)
}

// updateQueueActions keeps the buttons honest about the selection: the copy
// button counts what will be copied, and the single-printer actions only
// appear when exactly one printer is selected.
func (a *app) updateQueueActions() {
	if a.copyBtn == nil || a.queueModel == nil {
		return
	}
	ready := !a.queuesBusy && !a.mutationBusy && !a.clipBusy
	selected := a.selectedQueues()
	copyable := copyableOnly(selected)
	switch {
	case len(copyable) == 0:
		a.copyBtn.SetText("Select printers to copy")
	case len(copyable) == 1:
		a.copyBtn.SetText("Copy 1 printer...")
	default:
		a.copyBtn.SetText(fmt.Sprintf("Copy %d printers...", len(copyable)))
	}
	a.copyBtn.SetEnabled(ready && len(copyable) > 0)
	queue, single := a.selectedQueue()
	setShown(a.repointBtn, single && queue.Copyable())
	setShown(a.removeBtn, single)
	a.repointBtn.SetEnabled(ready)
	a.removeBtn.SetEnabled(ready)
	a.queueDetail.SetText(selectionDetail(selected, isElevated()))
}

// selectionDetail is one or two quiet lines about the selection.
func selectionDetail(selected []install.InstalledQueue, elevated bool) string {
	if len(selected) == 0 {
		return " "
	}
	if len(selected) == 1 {
		q := selected[0]
		if reason := q.CopyBlockedReason(); reason != "" {
			return "Can't copy " + q.PrinterName + ": " + reason
		}
		var facts []string
		if q.ProtocolName != "" {
			facts = append(facts, fmt.Sprintf("%s on TCP %d, port %s", q.ProtocolName, q.PortNumber, q.PortName))
		}
		if q.Shared {
			facts = append(facts, "shared with other computers")
		}
		line := strings.Join(facts, " · ")
		if !elevated {
			line = strings.TrimPrefix(line+" · Copies include settings only; drivers need administrator.", " · ")
		}
		return shownOr(line, " ")
	}
	copyable := len(copyableOnly(selected))
	text := fmt.Sprintf("%d selected; %s will go into one printer set (.zip).", len(selected), countPrinters(copyable))
	if copyable < len(selected) {
		text = fmt.Sprintf("%d selected; %s can be copied into one printer set (.zip).", len(selected), countPrinters(copyable))
	}
	return text
}

// onCopySelected saves the selection: one printer as a .ssb, several as one
// set. Nothing on this PC changes, so this never goes through review.
func (a *app) onCopySelected() {
	copyable := copyableOnly(a.selectedQueues())
	switch {
	case len(copyable) == 1:
		a.onCopyQueue(copyable[0])
	case len(copyable) > 1:
		a.onCopyQueues(copyable)
	}
}

// onCopyQueue writes one queue to a file another PC can open.
//
// This does not go through Review, because it changes nothing on this PC: it
// reads the queue and writes a file. Review exists to gate changes to Windows,
// and routing a read-only export through it would teach operators that the
// confirmation step is a formality.
func (a *app) onCopyQueue(queue install.InstalledQueue) {
	if !queue.Copyable() || a.queuesBusy || a.mutationBusy {
		return
	}
	var dialog *walk.Dialog
	var pathEdit, noteEdit *walk.LineEdit
	var includeDriver *walk.CheckBox
	var statusLabel *walk.Label
	var copyButton, cancelButton *walk.PushButton
	var running bool

	suggested := filepath.Join(defaultCopyDirectory(), bundleFileName(queue.PrinterName))
	driverLabel := "Include the driver where possible, so the other PC does not need it already"
	if !isElevated() {
		driverLabel = "Include the driver where possible (needs administrator; otherwise settings only)"
	}

	err := (Dialog{
		AssignTo: &dialog, Title: "Copy " + queue.PrinterName,
		MinSize: Size{Width: 560, Height: 300}, Background: SolidColorBrush{Color: colorPage}, Layout: dialogLayout(),
		DefaultButton: &copyButton, CancelButton: &cancelButton,
		Children: dialogFrame("Copy "+queue.PrinterName,
			Label{Text: "This saves the printer to one file. Open that file on the other PC, or drop it onto SpoolSmith there."},
			Composite{Layout: formGrid(3), Children: []Widget{
				Label{Text: "Save to:"},
				LineEdit{AssignTo: &pathEdit, Text: suggested, Accessibility: name("copy-path")},
				PushButton{Text: "Browse...", OnClicked: func() {
					picker := walk.FileDialog{Title: "Save printer file", Filter: printerFilter, FilePath: pathEdit.Text()}
					if ok, err := picker.ShowSave(dialog); err != nil {
						showErr(dialog, "Save printer file", err)
					} else if ok {
						pathEdit.SetText(picker.FilePath)
					}
				}},
				Label{Text: "Note (optional):"},
				LineEdit{AssignTo: &noteEdit, CueBanner: "For example: front desk, replaced 2026", Accessibility: name("copy-note"), ColumnSpan: 2},
			}},
			CheckBox{AssignTo: &includeDriver, Text: driverLabel, Checked: true},
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
						QueueName:    queue.PrinterName,
						Path:         path,
						Note:         strings.TrimSpace(noteEdit.Text()),
						SettingsOnly: !includeDriver.Checked(),
						CreatedBy:    "SpoolSmith desktop " + versionString(),
						SourceHost:   hostName(),
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
							walk.MsgBox(dialog, "Printer copied", copySuccessMessage(path, result.Manifest, result.DriverNotIncluded), walk.MsgBoxIconInformation)
							dialog.Accept()
						})
					}()
				}},
			}},
		),
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
func copySuccessMessage(path string, manifest bundle.Manifest, driverNotIncluded string) string {
	text := fmt.Sprintf("Saved %s\r\n\r\nPrinter: %s\r\nAddress: %s\r\nDriver: %s\r\n\r\n",
		path, manifest.Profile.PrinterName, manifest.Profile.Target, manifest.Profile.DriverName)
	if manifest.Profile.Evidence.Provenance != "captured" {
		text += bundle.UnconfirmedIdentityNotice + "\r\n\r\n"
	}
	if manifest.Driver == nil {
		text += "The driver was not included, so the other PC must already have this driver installed.\r\n"
		if driverNotIncluded != "" {
			text += "Why: " + driverNotIncluded + "\r\n"
		}
		text += "\r\n"
	} else {
		text += fmt.Sprintf("The driver is included (%d files), so the other PC does not need it beforehand.\r\n\r\n", len(manifest.Driver.Files))
	}
	return text + "On the other PC, double-click this file, or drop it onto SpoolSmith."
}

func (a *app) onRepointQueue() {
	queue, ok := a.selectedQueue()
	if !ok || !queue.Copyable() {
		return
	}
	var dialog *walk.Dialog
	var addressEdit *walk.LineEdit
	var statusLabel *walk.Label
	var okButton, cancelButton *walk.PushButton

	err := (Dialog{
		AssignTo: &dialog, Title: "Change address for " + queue.PrinterName,
		MinSize: Size{Width: 520, Height: 220}, Background: SolidColorBrush{Color: colorPage}, Layout: dialogLayout(),
		DefaultButton: &okButton, CancelButton: &cancelButton,
		Children: dialogFrame("Change address for "+queue.PrinterName,
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
		),
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
	a.startOperation(operation{Kind: opRemove, PrinterName: queue.PrinterName, Target: queue.HostAddress})
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
