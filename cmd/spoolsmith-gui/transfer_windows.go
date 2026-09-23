//go:build windows

package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/spilloid/spoolsmith/internal/bundle"
	"github.com/spilloid/spoolsmith/internal/install"
	"github.com/spilloid/spoolsmith/internal/profileset"
	"github.com/tailscale/walk"
	. "github.com/tailscale/walk/declarative"
)

func (a *app) exportSetups(owner walk.Form) {
	picker := walk.FileDialog{Title: "Export saved setups", Filter: "Saved-setup collections (*.ssb)|*.ssb", FilePath: "printer-setups.ssb", InitialDirPath: filepath.Dir(a.profilesDirectory())}
	ok, err := picker.ShowSave(owner)
	if err != nil {
		showErr(owner, "Export setups", err)
		return
	}
	if !ok {
		return
	}
	path := picker.FilePath
	if filepath.Ext(path) == "" {
		path += ".ssb"
	}
	a.reviewSetupTransfer(owner, false, a.profilesDirectory(), path)
}

func (a *app) importSetups(owner walk.Form) bool {
	picker := walk.FileDialog{Title: "Import saved setups", Filter: "Saved-setup collections (*.ssb)|*.ssb"}
	ok, err := picker.ShowOpen(owner)
	if err != nil {
		showErr(owner, "Import setups", err)
		return false
	}
	if !ok {
		return false
	}
	return a.reviewSetupTransfer(owner, true, picker.FilePath, a.profilesDirectory())
}

// Review and execution share the same validated snapshot as CLI --dry-run.
// Destination changes only recheck conflicts; they never reload reviewed input.
func (a *app) reviewSetupTransfer(owner walk.Form, importing bool, source, destination string) bool {
	var dialog *walk.Dialog
	var destinationEdit *walk.LineEdit
	var details *walk.TextEdit
	var status *walk.Label
	var browseButton, previewButton, saveButton, cancelButton *walk.PushButton
	var transfer *profileset.Transfer
	running, saved, closed := false, false, false
	verb, command, destinationLabel := "Export", "export-all", "Collection file:"
	if importing {
		verb, command, destinationLabel = "Import", "import-all", "Save setups in:"
	}
	setBusy := func(busy bool) {
		running = busy
		destinationEdit.SetEnabled(!busy)
		browseButton.SetEnabled(!busy)
		previewButton.SetEnabled(!busy)
		saveButton.SetEnabled(false)
	}
	var prepare func()
	prepare = func() {
		if running {
			return
		}
		path := strings.TrimSpace(destinationEdit.Text())
		if !importing && path != "" && filepath.Ext(path) == "" {
			path += ".ssb"
			destinationEdit.SetText(path)
		}
		setBusy(true)
		status.SetText("Checking saved setups and destination...")
		reviewed := transfer
		go func() {
			var next *profileset.Transfer
			var err error
			switch {
			case reviewed != nil:
				next, err = reviewed.WithDestination(path)
			case importing:
				next, err = profileset.PrepareImport(source, path)
			default:
				next, err = profileset.PrepareExport(source, path)
			}
			a.mw.Synchronize(func() {
				if closed {
					return
				}
				setBusy(false)
				if err != nil {
					status.SetText("Cannot prepare transfer: " + err.Error())
					return
				}
				transfer = next
				preview := next.Preview()
				details.SetText(setupTransferSummary(preview))
				if len(preview.Conflicts) > 0 {
					status.SetText(fmt.Sprintf("%d filename conflicts. Choose another destination, then review again.", len(preview.Conflicts)))
					return
				}
				status.SetText(fmt.Sprintf("Ready to %s %d saved setups. Review the filenames, printers and destination above.", strings.ToLower(verb), preview.Count))
				saveButton.SetEnabled(true)
			})
		}()
	}
	err := (Dialog{
		AssignTo: &dialog, Title: "Review setup " + strings.ToLower(verb),
		MinSize: Size{Width: 760, Height: 480}, Size: Size{Width: 850, Height: 600}, Layout: pagePadding(),
		CancelButton: &cancelButton,
		Children: []Widget{
			Label{Text: "Source: " + source},
			Composite{Layout: formGrid(3), Children: []Widget{
				Label{Text: destinationLabel},
				LineEdit{AssignTo: &destinationEdit, Text: destination, Accessibility: name("transfer-destination"), OnTextChanged: func() {
					if saveButton != nil && !running {
						saveButton.SetEnabled(false)
						status.SetText("Destination changed. Review again before saving.")
					}
				}},
				PushButton{AssignTo: &browseButton, Text: "Browse...", OnClicked: func() {
					picker := walk.FileDialog{Title: "Choose destination", FilePath: destinationEdit.Text(), InitialDirPath: destinationEdit.Text()}
					var ok bool
					var err error
					if importing {
						ok, err = picker.ShowBrowseFolder(dialog)
					} else {
						picker.Filter = "Saved-setup collections (*.ssb)|*.ssb"
						picker.InitialDirPath = filepath.Dir(destinationEdit.Text())
						ok, err = picker.ShowSave(dialog)
					}
					if err != nil {
						showErr(dialog, "Choose destination", err)
					} else if ok {
						destinationEdit.SetText(picker.FilePath)
						prepare()
					}
				}},
			}},
			TextEdit{AssignTo: &details, ReadOnly: true, VScroll: true, MinSize: Size{Width: 680, Height: 260}, Accessibility: name("transfer-review")},
			Label{AssignTo: &status, Text: "Checking saved setups..."},
			Composite{Layout: row(), Children: []Widget{
				PushButton{AssignTo: &previewButton, Text: "Review destination", OnClicked: prepare},
				HSpacer{},
				PushButton{AssignTo: &cancelButton, Text: "Cancel", OnClicked: func() { dialog.Cancel() }},
				PushButton{AssignTo: &saveButton, Text: verb + " setups", Enabled: false, OnClicked: func() {
					if running || transfer == nil || strings.TrimSpace(destinationEdit.Text()) != transfer.Preview().Destination {
						return
					}
					reviewed := transfer
					preview := reviewed.Preview()
					setBusy(true)
					status.SetText("Saving setups...")
					start := time.Now()
					go func() {
						count, err := reviewed.Execute()
						a.log("gui", "profile "+command, []string{source, preview.Destination}, statusOf(err), err, start)
						a.mw.Synchronize(func() {
							if closed {
								return
							}
							setBusy(false)
							if err != nil {
								status.SetText("Could not save: " + err.Error() + " Review the destination before retrying.")
								return
							}
							saved = true
							if importing {
								a.profileDirPath = preview.Destination
							}
							message := fmt.Sprintf("Saved %d setups to %s.\r\n\r\n%s", count, preview.Destination, profileset.TransferScope)
							if importing {
								message += "\r\n\r\nChoose a saved setup to review and set up its printer."
							}
							walk.MsgBox(dialog, "Setups saved", message, walk.MsgBoxIconInformation)
							dialog.Accept()
						})
					}()
				}},
			}},
		},
	}).Create(owner)
	if err != nil {
		showErr(owner, verb+" setups", err)
		return false
	}
	// Neither the destination check nor the save is cancellable mid-flight
	// (plain filesystem calls, no context to interrupt), so closing no
	// longer blocks on them. `closed` tells the background goroutine's
	// completion callback to discard its result instead of touching widgets
	// that may already be gone -- a stalled network destination no longer
	// traps the operator in this dialog.
	dialog.Closing().Attach(func(canceled *bool, _ walk.CloseReason) {
		closed = true
	})
	prepare()
	a.runDialog(dialog)
	return saved
}

func setupTransferSummary(preview profileset.Preview) string {
	var text strings.Builder
	fmt.Fprintf(&text, "%d saved setups\r\nDestination: %s\r\n\r\n%s\r\n\r\n", preview.Count, preview.Destination, preview.Scope)
	if len(preview.Conflicts) > 0 {
		text.WriteString("These filenames already exist and will not be replaced:\r\n" + strings.Join(preview.Conflicts, "\r\n") + "\r\n\r\n")
	}
	for _, profile := range preview.Profiles {
		fmt.Fprintf(&text, "%s\r\n  Printer: %s\r\n  Address: %s\r\n  Driver: %s\r\n", profile.File, profile.PrinterName, profile.Target, profile.DriverName)
		if profile.Archive != "" {
			fmt.Fprintf(&text, "  Separate archive: %s\r\n", profile.Archive)
		}
		text.WriteString("\r\n")
	}
	return text.String()
}

func (a *app) checkSavedStatus(owner walk.Form, path string) {
	p, err := bundle.LoadProfile(path)
	if err != nil {
		showErr(owner, "Check local status", err)
		return
	}
	// A status query does not probe the printer or mutate Windows.
	status, err := install.CheckStatus(context.Background(), a.env, p)
	if err != nil {
		showErr(owner, "Check local status", err)
		return
	}
	text := "This PC matches the saved queue, driver and RAW TCP 9100 settings."
	if !status.Compliant {
		text = "This PC differs from the saved setup:\r\n\r\n" + strings.Join(status.Mismatches, "\r\n")
	}
	walk.MsgBox(owner, "Local status: "+p.PrinterName, text+"\r\n\r\nThis checks local configuration only, not reachability or physical printing.", walk.MsgBoxIconInformation)
}
