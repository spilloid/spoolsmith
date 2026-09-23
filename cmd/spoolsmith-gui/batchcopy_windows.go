//go:build windows

package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/spilloid/spoolsmith/internal/bundle"
	"github.com/tailscale/walk"
	. "github.com/tailscale/walk/declarative"
)

// Bulk export is a file operation over the same CreateAll used by the CLI.
// Applying any of the resulting files still uses the existing reviewed plan.
func (a *app) onCopyAllQueues() {
	if a.queuesBusy || a.mutationBusy || len(a.queues) == 0 {
		return
	}
	var dialog *walk.Dialog
	var setEdit, noteEdit *walk.LineEdit
	var driverCheck *walk.CheckBox
	var detail *walk.TextEdit
	var status *walk.Label
	var browseBtn, copyBtn, closeBtn, reportBtn *walk.PushButton
	var cancel context.CancelFunc
	var running bool
	var resultText string

	stop := func() {
		if running {
			cancel()
			closeBtn.SetEnabled(false)
			status.SetText("Stopping the copy. No set is saved when you stop; waiting for the current step to finish...")
			return
		}
		dialog.Cancel()
	}
	start := func() {
		if running {
			return
		}
		setPath := strings.TrimSpace(setEdit.Text())
		if setPath == "" {
			status.SetText("Choose where to save the printer set.")
			setEdit.SetFocus()
			return
		}
		if !strings.EqualFold(filepath.Ext(setPath), bundle.SetExt) {
			setPath += bundle.SetExt
			setEdit.SetText(setPath)
		}
		ctx, stopCopy := context.WithCancel(context.Background())
		cancel = stopCopy
		running = true
		resultText = ""
		reportBtn.SetEnabled(false)
		for _, control := range []walk.Widget{setEdit, noteEdit, driverCheck, browseBtn, copyBtn} {
			control.SetEnabled(false)
		}
		closeBtn.SetText("Stop copying")
		status.SetText("Reading this PC's current printers...")
		detail.SetText("Each printer becomes one .ssb inside the set. A printer that fails is reported and the others are still saved.\r\n\r\nAn existing file will not be replaced.")
		opts := bundle.AllOptions{
			SetPath: setPath, SettingsOnly: !driverCheck.Checked(), Note: strings.TrimSpace(noteEdit.Text()),
			CreatedBy: "SpoolSmith desktop", SourceHost: hostName(),
			Progress: func(queue, step string) {
				a.mw.Synchronize(func() {
					if ctx.Err() == nil {
						status.SetText(queue + ": " + step)
					}
				})
			},
		}
		started := time.Now()
		go func() {
			defer stopCopy()
			result, err := bundle.CreateAll(ctx, a.env, a.collector(), opts)
			logStatus := statusOf(err)
			if err == nil && result.Failed > 0 {
				logStatus = "partial"
				if result.Written == 0 {
					logStatus = "error"
				}
			}
			a.log("gui", "copy --all", []string{setPath}, logStatus, err, started)
			a.mw.Synchronize(func() {
				running = false
				for _, control := range []walk.Widget{setEdit, noteEdit, driverCheck, browseBtn, copyBtn, closeBtn} {
					control.SetEnabled(true)
				}
				closeBtn.SetText("Close")
				reportBtn.SetEnabled(result.Requested > 0)
				status.SetText(fmt.Sprintf("%d copied, %d skipped, %d failed or not copied.", result.Written, result.Skipped, result.Failed))
				text := bulkCopyResultText(result)
				if err != nil {
					lead := friendlyOperationError(err.Error())
					if errors.Is(err, context.Canceled) {
						lead = "Copying stopped. No set was saved."
					}
					text = lead + "\r\n\r\n" + text
				}
				detail.SetText(text)
				resultText = text
			})
		}()
	}

	driverLabel := "Include each printer's driver where possible"
	if !isElevated() {
		driverLabel = "Include drivers where possible (needs administrator; otherwise settings only)"
	}

	err := (Dialog{
		AssignTo: &dialog, Title: "Copy all printers", MinSize: Size{Width: 720, Height: 480},
		Size: Size{Width: 820, Height: 560}, Background: SolidColorBrush{Color: colorPage}, Layout: dialogLayout(), CancelButton: &closeBtn,
		Children: dialogFrame("Copy all printers",
			Label{Text: "Copy this PC's printers into one printer set (.zip), one .ssb per printer inside."},
			hint("Open the set on the other PC with Add a printer, or unzip it to get each printer's .ssb file."),
			Composite{Layout: formGrid(3), Children: []Widget{
				Label{Text: "Save set as:"},
				LineEdit{AssignTo: &setEdit, Text: filepath.Join(defaultCopyDirectory(), "SpoolSmith-printers-"+time.Now().Format("20060102-150405")+bundle.SetExt), Accessibility: name("copy-all-file")},
				PushButton{AssignTo: &browseBtn, Text: "Browse...", OnClicked: func() {
					picker := walk.FileDialog{Title: "Save printer set", Filter: setFilter, FilePath: setEdit.Text()}
					if ok, err := picker.ShowSave(dialog); err != nil {
						showErr(dialog, "Save printer set", err)
					} else if ok {
						setEdit.SetText(picker.FilePath)
					}
				}},
				Label{Text: "Note (optional):"},
				LineEdit{AssignTo: &noteEdit, ColumnSpan: 2, CueBanner: "For example: front office PC replacement", Accessibility: name("copy-all-note")},
			}},
			CheckBox{AssignTo: &driverCheck, Text: driverLabel, Checked: true, Accessibility: name("copy-all-drivers")},
			TextEdit{AssignTo: &detail, ReadOnly: true, VScroll: true, Text: bulkCopyInventoryText(a.queues), Accessibility: name("copy-all-details")},
			Label{AssignTo: &status, Text: "An existing file will not be replaced. Windows printer settings will not change."},
			Composite{Layout: row(), Children: []Widget{
				PushButton{AssignTo: &reportBtn, Text: "Copy results", Enabled: false, OnClicked: func() {
					if err := walk.Clipboard().SetText(resultText); err != nil {
						showErr(dialog, "Copy results", err)
					} else {
						status.SetText("Results copied to the clipboard for your ticket or handoff.")
					}
				}},
				HSpacer{},
				PushButton{AssignTo: &closeBtn, Text: "Cancel", OnClicked: stop},
				PushButton{AssignTo: &copyBtn, Text: "Copy printers", OnClicked: start},
			}},
		),
	}).Create(a.mw)
	if err != nil {
		showErr(a.mw, "Copy all printers", err)
		return
	}
	dialog.Closing().Attach(func(canceled *bool, _ walk.CloseReason) {
		if running {
			*canceled = true
			stop()
		}
	})
	a.runDialog(dialog)
}
