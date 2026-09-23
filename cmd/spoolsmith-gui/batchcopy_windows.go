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
	var folderEdit, noteEdit *walk.LineEdit
	var driverCheck *walk.CheckBox
	var detail *walk.TextEdit
	var status *walk.Label
	var browseBtn, copyBtn, closeBtn, reportBtn *walk.PushButton
	var cancel context.CancelFunc
	var running bool
	var resultJSON string

	stop := func() {
		if running {
			cancel()
			closeBtn.SetEnabled(false)
			status.SetText("Stopping the copy. Completed files will be kept; waiting for the current step to finish...")
			return
		}
		dialog.Cancel()
	}
	start := func() {
		if running {
			return
		}
		folder := strings.TrimSpace(folderEdit.Text())
		if folder == "" {
			status.SetText("Choose a folder for the copied printer files.")
			folderEdit.SetFocus()
			return
		}
		ctx, stopCopy := context.WithCancel(context.Background())
		cancel = stopCopy
		running = true
		resultJSON = ""
		reportBtn.SetEnabled(false)
		for _, control := range []walk.Widget{folderEdit, noteEdit, driverCheck, browseBtn, copyBtn} {
			control.SetEnabled(false)
		}
		closeBtn.SetText("Stop copying")
		status.SetText("Reading this PC's current printers...")
		detail.SetText("Each printer gets its own .ssb file. Completed files are kept if another printer fails or you stop copying.\r\n\r\nExisting files will not be replaced.")
		opts := bundle.AllOptions{
			OutputDir: folder, IncludeDriver: driverCheck.Checked(), Note: strings.TrimSpace(noteEdit.Text()),
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
			a.log("gui", "copy --all", []string{folder}, logStatus, err, started)
			a.mw.Synchronize(func() {
				running = false
				for _, control := range []walk.Widget{folderEdit, noteEdit, driverCheck, browseBtn, copyBtn, closeBtn} {
					control.SetEnabled(true)
				}
				closeBtn.SetText("Close")
				resultJSON = prettyJSON(result)
				reportBtn.SetEnabled(result.Requested > 0)
				status.SetText(fmt.Sprintf("%d copied, %d skipped, %d failed or not copied.", result.Written, result.Skipped, result.Failed))
				text := bulkCopyResultText(result, opts.IncludeDriver)
				if err != nil {
					lead := friendlyOperationError(err.Error())
					if errors.Is(err, context.Canceled) {
						lead = "Copying stopped. Completed files have been kept."
					}
					text = lead + "\r\n\r\n" + text
				}
				detail.SetText(text)
			})
		}()
	}

	elevated := isElevated()
	driverLabel := "Include drivers (otherwise the other PC needs them installed)"
	if !elevated {
		driverLabel = "Include drivers (needs administrator -- restart the app as administrator to use this)"
	}

	err := (Dialog{
		AssignTo: &dialog, Title: "Copy all printers", MinSize: Size{Width: 720, Height: 480},
		Size: Size{Width: 820, Height: 560}, Layout: pagePadding(), CancelButton: &closeBtn,
		Children: []Widget{
			Label{Text: "Copy this PC's supported printers to a folder, one .ssb file per printer. Take the folder to the other PC to set them up."},
			Composite{Layout: formGrid(3), Children: []Widget{
				Label{Text: "Save in folder:"},
				LineEdit{AssignTo: &folderEdit, Text: filepath.Join(defaultCopyDirectory(), "SpoolSmith-printers-"+time.Now().Format("20060102-150405")), Accessibility: name("copy-all-folder")},
				PushButton{AssignTo: &browseBtn, Text: "Browse...", OnClicked: func() {
					picker := walk.FileDialog{Title: "Choose a folder for copied printers", FilePath: folderEdit.Text()}
					if ok, err := picker.ShowBrowseFolder(dialog); err != nil {
						showErr(dialog, "Choose folder", err)
					} else if ok {
						folderEdit.SetText(picker.FilePath)
					}
				}},
				Label{Text: "Note (optional):"},
				LineEdit{AssignTo: &noteEdit, ColumnSpan: 2, CueBanner: "For example: front office PC replacement", Accessibility: name("copy-all-note")},
			}},
			CheckBox{AssignTo: &driverCheck, Text: driverLabel, Checked: elevated, Enabled: elevated, Accessibility: name("copy-all-drivers")},
			TextEdit{AssignTo: &detail, ReadOnly: true, VScroll: true, Text: bulkCopyInventoryText(a.queues), Accessibility: name("copy-all-details")},
			Label{AssignTo: &status, Text: "Existing files will not be replaced. Windows printer settings will not change."},
			Composite{Layout: row(), Children: []Widget{
				PushButton{AssignTo: &reportBtn, Text: "Copy results / JSON", Enabled: false, OnClicked: func() {
					if err := walk.Clipboard().SetText(resultJSON); err != nil {
						showErr(dialog, "Copy results", err)
					} else {
						status.SetText("Results copied to the clipboard for your ticket or handoff.")
					}
				}},
				HSpacer{},
				PushButton{AssignTo: &closeBtn, Text: "Cancel", OnClicked: stop},
				PushButton{AssignTo: &copyBtn, Text: "Copy printers", OnClicked: start},
			}},
		},
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
