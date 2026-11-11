//go:build windows

package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/spilloid/spoolsmith/internal/bundle"
	"github.com/spilloid/spoolsmith/internal/install"
	"github.com/tailscale/walk"
	. "github.com/tailscale/walk/declarative"
)

// One printer is a .ssb file; several printers are a .zip of .ssb files.
// Every file picker in the app offers exactly these two shapes.
const (
	printerFilter     = "Printer file (*.ssb)|*.ssb"
	setFilter         = "Printer set (*.zip)|*.zip"
	openPrinterFilter = "Printer file or set (*.ssb;*.zip)|*.ssb;*.zip|Printer file (*.ssb)|*.ssb|Printer set (*.zip)|*.zip"
)

// setTableModel lists a set's printers with a tick box each. Only a printer
// with a reviewed plan can be ticked, and nothing can be ticked or unticked
// while the set is being set up.
type setTableModel struct {
	walk.TableModelBase
	rows     []setRow
	locked   func() bool
	onTicked func()
}

func (m *setTableModel) RowCount() int { return len(m.rows) }

func (m *setTableModel) Value(row, col int) interface{} {
	r := m.rows[row]
	switch col {
	case 0:
		return r.Name
	case 1:
		return r.Target
	case 2:
		return r.Driver
	case 3:
		return r.StatusText()
	}
	return ""
}

func (m *setTableModel) Checked(row int) bool { return m.rows[row].Checked }

func (m *setTableModel) SetChecked(row int, checked bool) error {
	if (m.locked != nil && m.locked()) || !m.rows[row].Runnable() {
		// Redraw the old state rather than accept a tick that means nothing.
		m.PublishRowChanged(row)
		return nil
	}
	m.rows[row].Checked = checked
	if m.onTicked != nil {
		m.onTicked()
	}
	return nil
}

// showPrinterSet opens the set review for a set the operator just opened.
func (a *app) showPrinterSet(path string) {
	a.reviewPrinterSet(operation{Kind: opApplySet, BundlePath: path})
}

type setPhase int

const (
	setLoading setPhase = iota
	setPreviewing
	setReviewed
	setRunning
	setFinished
)

// reviewPrinterSet is the set's one review: every printer is previewed against
// this PC, listed with its plan, and the ticked ones are set up after one
// confirmation of that list. Each printer runs its own previewed plan, bound
// to it with ExpectedPlan, so a printer whose plan changed is not set up.
//
// op.Members, from an elevated relaunch, says which printers were ticked; the
// list here is still this instance's own preview.
func (a *app) reviewPrinterSet(op operation) {
	path := op.BundlePath
	file := filepath.Base(path)
	var (
		dialog                        *walk.Dialog
		table                         *walk.TableView
		status                        *walk.Label
		stepsTitle, stepsLabel        *walk.Label
		updateCheck, offlineCheck     *walk.CheckBox
		detailsBtn, reviewOneBtn      *walk.PushButton
		copyBtn, closeBtn, primaryBtn *walk.PushButton
		phase                         = setLoading
		closed                        bool
		workDir                       string
		keepWorkDir                   bool
		reviewAfter                   string
		closeAppAfter                 bool
		generation                    int
		cancelPreview                 context.CancelFunc = func() {}
		finishedAt                    time.Time
		note                          string
	)
	// One preview or run at a time against Windows: a preview superseded by
	// an option change finishes its current printer before the next begins.
	var workMu sync.Mutex
	model := &setTableModel{}
	model.locked = func() bool { return phase == setRunning || phase == setFinished }

	options := func() setOptions {
		return setOptions{UpdateExisting: updateCheck.Checked(), Offline: offlineCheck.Checked()}
	}
	refresh := func() {
		if closed {
			return
		}
		ticked := len(tickedRows(model.rows))
		primaryBtn.SetText(setPrimaryCaption(ticked, isElevated(), phase == setFinished))
		setShield(primaryBtn, !isElevated() && phase != setFinished)
		primaryBtn.SetEnabled(phase == setFinished || (phase == setReviewed && ticked > 0))
		updateCheck.SetEnabled(phase == setReviewed || phase == setPreviewing)
		offlineCheck.SetEnabled(phase == setReviewed || phase == setPreviewing)
		detailsBtn.SetEnabled(phase >= setReviewed)
		i := table.CurrentIndex()
		reviewOneBtn.SetEnabled(phase == setReviewed && i >= 0 && i < len(model.rows))
		closeBtn.SetEnabled(phase != setRunning)
		setShown(copyBtn, phase == setFinished)
		// The selected printer's plan, in the same words as the sheet.
		if i >= 0 && i < len(model.rows) {
			r := model.rows[i]
			stepsTitle.SetText(r.Name + "  —  " + r.StatusText())
			stepsLabel.SetText(shownOr(wrapText(rowSteps(r, "\r\n"), sheetWrap), " "))
		} else {
			stepsTitle.SetText("Select a printer to see its steps.")
			stepsLabel.SetText(" ")
		}
	}
	model.onTicked = refresh

	previewStatus := func() string {
		var ready, admin, nothing, blocked int
		for _, r := range model.rows {
			switch r.State {
			case rowReady:
				ready++
			case rowNeedsAdmin:
				admin++
			case rowNothing:
				nothing++
			case rowBlocked:
				blocked++
			}
		}
		parts := []string{fmt.Sprintf("%s ready to set up", countPrinters(ready+admin))}
		if nothing > 0 {
			parts = append(parts, fmt.Sprintf("%d with nothing to change", nothing))
		}
		if blocked > 0 {
			parts = append(parts, fmt.Sprintf("%d can't be set up", blocked))
		}
		text := strings.Join(parts, ", ") + ". Nothing has changed yet."
		if admin > 0 && !isElevated() {
			text += " Windows asks for administrator permission next; SpoolSmith then checks every printer again and asks you to confirm the list once."
		}
		if note != "" {
			text = "Note: " + note + "\r\n" + text
		}
		return text
	}

	// preview dry-runs every printer in order, filling the list as it goes.
	preview := func() {
		cancelPreview()
		ctx, cancel := context.WithCancel(context.Background())
		cancelPreview = cancel
		generation++
		gen := generation
		phase = setPreviewing
		opts := options()
		for i := range model.rows {
			if model.rows[i].Path == "" {
				// Unreadable in the set itself; nothing to preview.
				continue
			}
			model.rows[i].State, model.rows[i].Detail = rowChecking, ""
			model.rows[i].Preview, model.rows[i].Result, model.rows[i].Transcript = nil, nil, ""
		}
		// A reset drops the selection; keep the operator's place in the list.
		selectedRow := table.CurrentIndex()
		model.PublishRowsReset()
		if len(model.rows) > 0 {
			table.SetCurrentIndex(max(selectedRow, 0))
		}
		status.SetText(fmt.Sprintf("Checking %s against this PC. Nothing changes yet...", countPrinters(len(model.rows))))
		refresh()
		rows := slices.Clone(model.rows)
		go func() {
			workMu.Lock()
			defer workMu.Unlock()
			for i, r := range rows {
				if ctx.Err() != nil {
					return
				}
				if r.Path == "" {
					continue
				}
				start := time.Now()
				member := operation{Kind: opApply, BundlePath: r.Path, UpdateExisting: opts.UpdateExisting, Offline: opts.Offline}.normalized()
				var buf bytes.Buffer
				outcome, _, err := a.previewOperation(member, &buf)
				outcome.Operation = "apply"
				state, detail := classifyPreview(outcome, err)
				a.log("gui", "apply-set preview", []string{path, r.Member}, statusOf(err), err, start)
				a.mw.Synchronize(func() {
					if closed || gen != generation {
						return
					}
					row := &model.rows[i]
					row.State, row.Detail, row.Transcript = state, detail, buf.String()
					if err == nil {
						row.Preview = &outcome
					}
					row.Checked = row.Runnable() && (len(op.Members) == 0 || slices.Contains(op.Members, row.Member))
					model.PublishRowChanged(i)
					refresh()
				})
			}
			a.mw.Synchronize(func() {
				if closed || gen != generation {
					return
				}
				phase = setReviewed
				// The hand-over's ticks apply to the first preview only.
				op.Members = nil
				status.SetText(previewStatus())
				refresh()
				if primaryBtn.Enabled() {
					primaryBtn.SetFocus()
				}
			})
		}()
	}

	// run sets up the ticked printers, each with its own reviewed plan.
	run := func() {
		opts := options()
		if walk.MsgBox(dialog, "Confirm: set up printers from "+file, setConfirmText(file, model.rows, opts), walk.MsgBoxYesNo|walk.MsgBoxDefButton2|walk.MsgBoxIconWarning) != walk.DlgCmdYes {
			return
		}
		phase = setRunning
		a.setMutationBusy(true)
		a.mutationExecuting = true
		status.SetText("Setting up the confirmed printers. Please keep SpoolSmith open.")
		refresh()
		rows := slices.Clone(model.rows)
		go func() {
			workMu.Lock()
			defer workMu.Unlock()
			for i, r := range rows {
				if !(r.Checked && r.Runnable()) {
					if r.Runnable() {
						a.mw.Synchronize(func() {
							model.rows[i].State = rowSkipped
							model.PublishRowChanged(i)
							refresh()
						})
					}
					continue
				}
				a.mw.Synchronize(func() {
					model.rows[i].State = rowRunning
					model.PublishRowChanged(i)
					refresh()
				})
				start := time.Now()
				var buf bytes.Buffer
				var outcome install.Outcome
				profile, driver, err := loadPrinterFile(r.Path)
				if err != nil {
					outcome = install.Outcome{Status: "error", Error: err.Error()}
				} else {
					outcome, _ = a.workflow.RunInstall(context.Background(), a.env, strings.NewReader(""), &buf, false, install.InstallOptions{
						Profile:        &profile,
						BundleDriver:   driver,
						UpdateExisting: opts.UpdateExisting,
						Offline:        opts.Offline,
						Compact:        true,
						ExpectedPlan:   r.Preview.Plan,
						Yes:            true,
						NonInteractive: true,
					})
				}
				outcome.Operation = "apply"
				state, detail := classifyResult(outcome)
				var logErr error
				if detail != "" {
					logErr = fmt.Errorf("%s", detail)
				}
				a.log("gui", "apply-set execute", []string{path, r.Member, profile.PrinterName}, statusOf(logErr), logErr, start)
				a.mw.Synchronize(func() {
					row := &model.rows[i]
					row.State, row.Detail, row.Result = state, detail, &outcome
					row.Transcript = buf.String()
					model.PublishRowChanged(i)
					refresh()
				})
			}
			a.mw.Synchronize(func() {
				phase = setFinished
				finishedAt = time.Now()
				a.mutationExecuting = false
				a.setMutationBusy(false)
				a.onRefreshQueues()
				if closed {
					return
				}
				status.SetText(setSummaryLine(model.rows) + " The list shows each printer's result; Details has every step.")
				refresh()
				primaryBtn.SetFocus()
			})
		}()
	}

	onPrimary := func() {
		switch phase {
		case setFinished:
			dialog.Accept()
		case setReviewed:
			if len(tickedRows(model.rows)) == 0 {
				return
			}
			if isElevated() {
				run()
				return
			}
			handOver := operation{Kind: opApplySet, BundlePath: path, UpdateExisting: updateCheck.Checked(), Offline: offlineCheck.Checked(), Members: tickedMembers(model.rows)}
			started, cancelled := a.startElevated(dialog, handOver)
			switch {
			case cancelled:
				status.SetText("Administrator permission wasn't given, so nothing changed. Use the shield button to try again.")
			case started:
				closeAppAfter = true
				dialog.Accept()
			}
		}
	}

	err := (Dialog{
		AssignTo: &dialog, Title: "Printer set",
		MinSize: Size{Width: 760, Height: 560}, Size: Size{Width: 940, Height: 700},
		Background: SolidColorBrush{Color: colorPage}, Layout: dialogLayout(),
		DefaultButton: &primaryBtn, CancelButton: &closeBtn,
		Children: dialogFrame("Set up printers from a set",
			Label{Text: file, Font: Font{Family: "Segoe UI", PointSize: 11, Bold: true}},
			hint("Every printer below is checked against this PC first, and you confirm the ticked ones once. Each printer runs exactly the steps shown for it."),
			TableView{
				AssignTo: &table, Model: model, CheckBoxes: true,
				AlternatingRowBG: false, LastColumnStretched: true, NotSortableByHeaderClick: true,
				MinSize: Size{Height: 200}, Accessibility: name("set-members"),
				Columns: []TableViewColumn{
					{Title: "Printer", Width: 220},
					{Title: "Address", Width: 130},
					{Title: "Driver", Width: 120},
					{Title: "Plan", Width: 300},
				},
				StyleCell: func(style *walk.CellStyle) {
					if row := style.Row(); row >= 0 && row < len(model.rows) {
						switch model.rows[row].State {
						case rowBlocked, rowFailed:
							style.TextColor = colorWarning
						case rowNothing, rowSkipped:
							style.TextColor = colorHint
						}
					}
				},
				OnCurrentIndexChanged: func() { refresh() },
			},
			Composite{Background: SolidColorBrush{Color: colorSurface}, Layout: VBox{Margins: Margins{Left: 14, Top: 10, Right: 14, Bottom: 10}, Spacing: 6}, Children: []Widget{
				Label{AssignTo: &stepsTitle, Text: "Select a printer to see its steps.", Font: Font{Family: "Segoe UI", PointSize: 10, Bold: true}, Background: SolidColorBrush{Color: colorSurface}, Accessibility: name("set-steps-title")},
				Label{AssignTo: &stepsLabel, Text: " ", Font: Font{Family: "Segoe UI", PointSize: 10}, Background: SolidColorBrush{Color: colorSurface}, Accessibility: name("set-steps")},
				HSpacer{},
			}},
			Label{AssignTo: &status, Text: "Opening the set..."},
			Composite{Layout: row(), Children: []Widget{
				CheckBox{AssignTo: &updateCheck, Text: "Update printers already on this PC to match their files", Enabled: false},
				HSpacer{},
			}},
			Composite{Layout: row(), Children: []Widget{
				CheckBox{AssignTo: &offlineCheck, Text: "Offline setup — the printers are not contacted or checked", Enabled: false, Checked: op.Offline},
				HSpacer{},
			}},
			Composite{Layout: row(), Children: []Widget{
				PushButton{AssignTo: &detailsBtn, Text: "Details", Enabled: false, OnClicked: func() {
					a.showDetailsOn(dialog, "Printer set details", lines(setDetailsText(model.rows)))
				}},
				PushButton{AssignTo: &reviewOneBtn, Text: "Review only this printer...", Enabled: false, OnClicked: func() {
					i := table.CurrentIndex()
					if i < 0 || i >= len(model.rows) {
						return
					}
					// The single-printer sheet reads the extracted file after
					// this dialog closes.
					keepWorkDir = true
					reviewAfter = model.rows[i].Path
					dialog.Accept()
				}},
				PushButton{AssignTo: &copyBtn, Text: "Copy summary for the ticket", Visible: false, OnClicked: func() {
					text := setSummaryText(versionString(), hostName(), finishedAt, file, model.rows)
					if err := walk.Clipboard().SetText(lines(text)); err != nil {
						showErr(dialog, "Copy summary", err)
						return
					}
					copyBtn.SetText("Copied")
				}},
				HSpacer{},
				PushButton{AssignTo: &closeBtn, Text: "Close", OnClicked: func() { dialog.Cancel() }},
				PushButton{AssignTo: &primaryBtn, Text: "Set up printers", Enabled: false, MinSize: Size{Width: 200}, OnClicked: onPrimary},
			}},
		),
	}).Create(a.mw)
	if err != nil {
		showErr(a.mw, "Printer set", err)
		return
	}
	updateCheck.SetChecked(op.UpdateExisting)
	for _, check := range []*walk.CheckBox{updateCheck, offlineCheck} {
		check.CheckedChanged().Attach(func() {
			if phase == setPreviewing || phase == setReviewed {
				preview()
			}
		})
	}
	dialog.Closing().Attach(func(canceled *bool, _ walk.CloseReason) {
		if phase == setRunning {
			*canceled = true
			walk.MsgBox(dialog, "Setting up printers", "Wait for the printers being set up to finish before closing.", walk.MsgBoxIconInformation)
			return
		}
		closed = true
		cancelPreview()
		if workDir != "" && !keepWorkDir {
			dir := workDir
			go func() {
				// After any preview still reading from it has stopped.
				workMu.Lock()
				defer workMu.Unlock()
				os.RemoveAll(dir)
			}()
		}
	})

	go func() {
		loaded, dir, setNote, err := extractSet(path)
		a.mw.Synchronize(func() {
			if closed {
				if dir != "" {
					os.RemoveAll(dir)
				}
				return
			}
			workDir = dir
			if err != nil {
				status.SetText("This set cannot be used: " + friendlyOperationError(err.Error()))
				return
			}
			note = setNote
			model.rows = loaded
			model.PublishRowsReset()
			preview()
		})
	}()
	a.runDialog(dialog)
	switch {
	case closeAppAfter:
		// The elevated copy has started and reviews the set again.
		a.mw.Close()
	case reviewAfter != "":
		a.reviewBundle(reviewAfter)
	}
}

// extractSet unpacks every member into a private temporary folder and
// verifies each one, so what the list shows is exactly what review will open.
// The folder is returned even on error, for the caller to remove.
func extractSet(path string) ([]setRow, string, string, error) {
	set, err := bundle.OpenSet(path)
	if err != nil {
		return nil, "", "", err
	}
	defer set.Close()
	dir, err := os.MkdirTemp("", "spoolsmith-set-")
	if err != nil {
		return nil, "", "", err
	}
	rows := make([]setRow, 0, len(set.Members))
	for _, name := range set.Members {
		extracted, err := set.Extract(name, dir)
		if err != nil {
			// One damaged printer file doesn't sink the set, as on the CLI: it
			// is listed as one that can't be set up, and the rest go ahead.
			rows = append(rows, setRow{Member: name, Name: name, State: rowBlocked, Detail: err.Error()})
			continue
		}
		opened, err := bundle.Open(extracted)
		if err != nil {
			rows = append(rows, setRow{Member: name, Name: name, State: rowBlocked, Detail: fmt.Sprintf("%s: %v", name, err)})
			continue
		}
		profile := opened.Manifest.Profile
		driver := "settings only"
		if opened.Manifest.Driver != nil {
			driver = "driver included"
		}
		opened.Close()
		target := profile.Target
		if profile.PortType == "usb" {
			target = "USB printer"
		}
		rows = append(rows, setRow{
			Member: name,
			Path:   extracted,
			Name:   shownOr(profile.PrinterName, name),
			Target: target,
			Driver: driver,
		})
	}
	return rows, dir, set.Note, nil
}
