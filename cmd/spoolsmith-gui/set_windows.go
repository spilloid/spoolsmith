//go:build windows

package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spilloid/spoolsmith/internal/bundle"
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

// setEntry is one printer inside an opened set, already extracted and
// verified as a standalone printer file.
type setEntry struct {
	path  string
	label string
}

// showPrinterSet lists the printers inside a set so the operator picks one
// to review. Applying never happens in bulk: each printer still goes through
// its own plan and its own confirmation, exactly as if its .ssb had been
// opened directly. Extraction runs off the UI thread, because members can
// carry driver payloads of hundreds of megabytes.
func (a *app) showPrinterSet(path string) {
	var dialog *walk.Dialog
	var list *walk.ListBox
	var status *walk.Label
	var setupBtn, closeBtn *walk.PushButton
	var entries []setEntry
	closed := false

	selected := func() (setEntry, bool) {
		i := list.CurrentIndex()
		if i < 0 || i >= len(entries) {
			return setEntry{}, false
		}
		return entries[i], true
	}
	setUp := func() {
		entry, ok := selected()
		if !ok {
			return
		}
		dialog.Accept()
		a.reviewBundle(entry.path)
	}

	err := (Dialog{
		AssignTo: &dialog, Title: "Printer set",
		MinSize: Size{Width: 640, Height: 380}, Size: Size{Width: 760, Height: 460},
		Background: SolidColorBrush{Color: colorPage}, Layout: dialogLayout(),
		DefaultButton: &setupBtn, CancelButton: &closeBtn,
		Children: dialogFrame("Printer set",
			Label{Text: filepath.Base(path)},
			hint("Choose a printer to review. Each printer is reviewed and confirmed on its own."),
			ListBox{AssignTo: &list, MinSize: Size{Height: 180}, Accessibility: name("set-members"), OnItemActivated: setUp},
			Label{AssignTo: &status, Text: "Opening the set..."},
			Composite{Layout: row(), Children: []Widget{
				HSpacer{},
				PushButton{AssignTo: &closeBtn, Text: "Close", OnClicked: func() { dialog.Cancel() }},
				PushButton{AssignTo: &setupBtn, Text: "Review this printer", Enabled: false, OnClicked: setUp},
			}},
		),
	}).Create(a.mw)
	if err != nil {
		showErr(a.mw, "Printer set", err)
		return
	}
	dialog.Closing().Attach(func(*bool, walk.CloseReason) { closed = true })
	list.CurrentIndexChanged().Attach(func() {
		_, ok := selected()
		setupBtn.SetEnabled(ok)
	})

	go func() {
		loaded, note, err := extractSet(path)
		a.mw.Synchronize(func() {
			if closed {
				return
			}
			if err != nil {
				status.SetText("This set cannot be used: " + friendlyOperationError(err.Error()))
				return
			}
			entries = loaded
			labels := make([]string, len(entries))
			for i, entry := range entries {
				labels[i] = entry.label
			}
			list.SetModel(labels)
			if len(entries) > 0 {
				list.SetCurrentIndex(0)
			}
			text := countPrinters(len(entries)) + " in this set."
			if note != "" {
				text += " Note: " + note
			}
			status.SetText(text)
		})
	}()
	a.runDialog(dialog)
}

// extractSet unpacks every member into a private temporary folder and
// verifies each one, so what the list shows is exactly what review will open.
func extractSet(path string) ([]setEntry, string, error) {
	set, err := bundle.OpenSet(path)
	if err != nil {
		return nil, "", err
	}
	defer set.Close()
	dir, err := os.MkdirTemp("", "spoolsmith-set-")
	if err != nil {
		return nil, "", err
	}
	entries := make([]setEntry, 0, len(set.Members))
	for _, name := range set.Members {
		extracted, err := set.Extract(name, dir)
		if err != nil {
			return nil, "", fmt.Errorf("%s: %w", name, err)
		}
		opened, err := bundle.Open(extracted)
		if err != nil {
			return nil, "", fmt.Errorf("%s: %w", name, err)
		}
		profile := opened.Manifest.Profile
		driver := "settings only"
		if opened.Manifest.Driver != nil {
			driver = "driver included"
		}
		opened.Close()
		entries = append(entries, setEntry{
			path:  extracted,
			label: fmt.Sprintf("%s  ·  %s  ·  %s", profile.PrinterName, profile.Target, driver),
		})
	}
	return entries, set.Note, nil
}
