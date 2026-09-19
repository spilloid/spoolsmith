//go:build windows

package main

import (
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spilloid/spoolsmith/internal/install"
	"github.com/tailscale/walk"
	. "github.com/tailscale/walk/declarative"
)

// onOpenSavedSetup offers the setups already saved on this PC.
//
// These used to occupy a whole tab with six buttons and an editor that expanded
// inline underneath them. Reusing a saved setup is a step on the way to adding
// a printer, not a place to live, so it is a dialog opened from Add a printer.
func (a *app) onOpenSavedSetup() {
	if a.mutationBusy {
		return
	}
	paths, err := savedSetupPaths(a.profilesDirectory())
	a.showSavedSetups(paths, "", err)
}

// savedSetupPaths lists JSON candidates in a folder, in filename order. A
// folder that does not exist yet is the ordinary first-run case, not an
// error -- SpoolSmith has never had reason to create it. Anything else
// os.ReadDir reports (permission denied, the path is a file, a network
// share gone away) is a real problem and is returned rather than folded
// into the same "no saved setups" empty state a caller cannot tell apart
// from it.
func savedSetupPaths(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var paths []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".json") {
			continue
		}
		paths = append(paths, filepath.Join(dir, entry.Name()))
	}
	sort.Strings(paths)
	return paths, nil
}

// savedSetupLabel describes one setup in a single line.
func savedSetupLabel(path string) string {
	profile, err := install.LoadProfile(path)
	if err != nil {
		return filepath.Base(path) + "  ·  cannot be used"
	}
	return fmt.Sprintf("%s  ·  %s  ·  %s", profile.PrinterName, profile.Target, filepath.Base(path))
}

func (a *app) showSavedSetups(paths []string, message string, readErr error) {
	var dialog *walk.Dialog
	var list *walk.ListBox
	var detail *walk.TextEdit
	var status *walk.Label
	var setupBtn, updateBtn, editBtn, statusBtn, removeBtn, cancelBtn *walk.PushButton
	current := append([]string(nil), paths...)
	folderErr := readErr

	labels := func() []string {
		out := make([]string, 0, len(current))
		for _, path := range current {
			out = append(out, savedSetupLabel(path))
		}
		return out
	}
	selected := func() (string, bool) {
		index := list.CurrentIndex()
		if index < 0 || index >= len(current) {
			return "", false
		}
		return current[index], true
	}
	refreshDetail := func() {
		path, ok := selected()
		if !ok {
			switch {
			case folderErr != nil:
				detail.SetText("Couldn't read this folder: " + folderErr.Error() + "\r\n\r\nUse Open another folder to choose a different location.")
			case len(current) == 0:
				detail.SetText("No saved setups in this folder yet.\r\n\r\nImport a JSON collection, open another folder, or save a printer from Add a printer.")
			default:
				detail.SetText("Choose a saved setup.")
			}
			for _, button := range []*walk.PushButton{setupBtn, updateBtn, editBtn, statusBtn, removeBtn} {
				button.SetEnabled(false)
			}
			return
		}
		profile, err := install.LoadProfile(path)
		if err != nil {
			detail.SetText("This setup cannot be used:\r\n\r\n" + err.Error())
			setupBtn.SetEnabled(false)
			updateBtn.SetEnabled(false)
			editBtn.SetEnabled(false)
			statusBtn.SetEnabled(false)
			removeBtn.SetEnabled(false)
			return
		}
		detail.SetText(profileSummary(profile, path))
		for _, button := range []*walk.PushButton{setupBtn, updateBtn, editBtn, statusBtn, removeBtn} {
			button.SetEnabled(true)
		}
	}
	start := func(kind operationKind) {
		path, ok := selected()
		if !ok {
			return
		}
		dialog.Accept()
		a.startProfileOperation(path, kind)
	}

	err := (Dialog{
		AssignTo: &dialog, Title: "Saved printer setups",
		MinSize: Size{Width: 720, Height: 420}, Size: Size{Width: 820, Height: 480}, Layout: pagePadding(),
		CancelButton: &cancelBtn,
		Children: []Widget{
			Label{Text: "Setups saved on this PC. Set one up again, or update a printer to match its saved settings."},
			Label{AssignTo: &status, Text: shownOr(message, "Folder: "+a.profilesDirectory())},
			HSplitter{Children: []Widget{
				ListBox{AssignTo: &list, MinSize: Size{Width: 330, Height: 200}, Accessibility: name("saved-list")},
				TextEdit{AssignTo: &detail, ReadOnly: true, VScroll: true, MinSize: Size{Width: 280, Height: 200}, Accessibility: name("saved-detail")},
			}},
			Composite{Layout: row(), Children: []Widget{
				PushButton{AssignTo: &setupBtn, Text: "Set up this printer", Enabled: false, OnClicked: func() { start(opInstall) }},
				PushButton{AssignTo: &updateBtn, Text: "Update to match", Enabled: false, OnClicked: func() { start(opConfigure) }},
				PushButton{AssignTo: &statusBtn, Text: "Check status", Enabled: false, OnClicked: func() {
					if path, ok := selected(); ok {
						a.checkSavedStatus(dialog, path)
					}
				}},
				PushButton{AssignTo: &removeBtn, Text: "Remove printer from this PC...", Enabled: false, OnClicked: func() { start(opRemove) }},
				PushButton{AssignTo: &editBtn, Text: "Edit...", Enabled: false, OnClicked: func() {
					path, ok := selected()
					if !ok {
						return
					}
					if a.editSavedSetup(dialog, path) {
						index := list.CurrentIndex()
						list.SetModel(labels())
						list.SetCurrentIndex(index)
						refreshDetail()
					}
				}},
			}},
			Composite{Layout: row(), Children: []Widget{
				PushButton{Text: "Export all JSON...", OnClicked: func() { a.exportSetups(dialog) }},
				PushButton{Text: "Import all JSON...", OnClicked: func() {
					if a.importSetups(dialog) {
						status.SetText("Folder: " + a.profilesDirectory())
						current, folderErr = savedSetupPaths(a.profilesDirectory())
						list.SetModel(labels())
						if len(current) > 0 {
							list.SetCurrentIndex(0)
						}
						refreshDetail()
					}
				}},
				HSpacer{},
				PushButton{Text: "Open another folder...", OnClicked: func() {
					picker := walk.FileDialog{Title: "Choose a setup folder"}
					if ok, err := picker.ShowBrowseFolder(dialog); err != nil {
						showErr(dialog, "Choose folder", err)
					} else if ok {
						a.profileDirPath = picker.FilePath
						current, folderErr = savedSetupPaths(picker.FilePath)
						list.SetModel(labels())
						status.SetText("Folder: " + picker.FilePath)
						if len(current) > 0 {
							list.SetCurrentIndex(0)
						}
						refreshDetail()
					}
				}},
				PushButton{AssignTo: &cancelBtn, Text: "Close", OnClicked: func() { dialog.Cancel() }},
			}},
		},
	}).Create(a.mw)
	if err != nil {
		showErr(a.mw, "Saved setups", err)
		return
	}
	list.CurrentIndexChanged().Attach(refreshDetail)
	list.ItemActivated().Attach(func() { start(opInstall) })
	list.SetModel(labels())
	if len(current) > 0 {
		list.SetCurrentIndex(0)
	}
	refreshDetail()
	a.runDialog(dialog)
}

// editSavedSetup edits one saved setup in place, keeping a backup. It reports
// whether the file was changed.
//
// This was an expanding panel inside a tab, which meant the list behind it
// could be re-selected while unsaved edits were open -- the old code carried a
// dirty check and a discard prompt to cope. A modal dialog makes that state
// impossible instead of guarding against it.
func (a *app) editSavedSetup(owner walk.Form, path string) bool {
	profile, loadErr := install.LoadProfile(path)
	if loadErr != nil {
		showErr(owner, "Cannot edit setup", fmt.Errorf("this setup could not be read; fix the JSON or capture a new setup before editing: %w", loadErr))
		return false
	}
	var dialog *walk.Dialog
	var nameEdit, driverEdit, targetEdit, packageEdit, archiveEdit *walk.LineEdit
	var status *walk.Label
	var saveBtn, cancelBtn *walk.PushButton
	saved := false

	packageID, archive := "", ""
	if profile.DriverPackage != nil {
		packageID, archive = profile.DriverPackage.ID, profile.DriverPackage.Archive
	}

	err := (Dialog{
		AssignTo: &dialog, Title: "Edit " + filepath.Base(path),
		MinSize: Size{Width: 620, Height: 340}, Layout: pagePadding(),
		DefaultButton: &saveBtn, CancelButton: &cancelBtn,
		Children: []Widget{
			Label{Text: path},
			Composite{Layout: formGrid(2), Children: []Widget{
				Label{Text: "Printer name:"}, LineEdit{AssignTo: &nameEdit, Text: profile.PrinterName, Accessibility: name("edit-name")},
				Label{Text: "Driver name:"}, LineEdit{AssignTo: &driverEdit, Text: profile.DriverName, Accessibility: name("edit-driver")},
				Label{Text: "IP address:"}, LineEdit{AssignTo: &targetEdit, Text: profile.Target, Accessibility: name("edit-target")},
				Label{Text: "Package ID (optional):"}, LineEdit{AssignTo: &packageEdit, Text: packageID, Accessibility: name("edit-package")},
				Label{Text: "Local archive:"}, LineEdit{AssignTo: &archiveEdit, Text: archive, Accessibility: name("edit-archive")},
			}},
			Label{Text: "Leave both package fields empty to use a driver already installed on the PC."},
			Label{AssignTo: &status, Text: "A backup is kept whenever you save changes."},
			VSpacer{},
			Composite{Layout: row(), Children: []Widget{
				HSpacer{},
				PushButton{AssignTo: &cancelBtn, Text: "Cancel", OnClicked: func() { dialog.Cancel() }},
				PushButton{AssignTo: &saveBtn, Text: "Save setup changes", OnClicked: func() {
					updated := profile
					updated.PrinterName = strings.TrimSpace(nameEdit.Text())
					updated.DriverName = strings.TrimSpace(driverEdit.Text())
					updated.Target = strings.TrimSpace(targetEdit.Text())
					if _, err := netip.ParseAddr(updated.Target); err != nil {
						status.SetText("Enter the printer's IP address, such as 192.168.1.25.")
						targetEdit.SetFocus()
						return
					}
					id, archivePath := strings.TrimSpace(packageEdit.Text()), strings.TrimSpace(archiveEdit.Text())
					switch {
					case id == "" && archivePath == "":
						updated.DriverPackage = nil
					case id == "" || archivePath == "":
						status.SetText("Enter both a package ID and a local archive, or leave both empty.")
						return
					default:
						updated.DriverPackage = &install.PackageSelection{ID: id, Archive: archivePath}
					}
					backup, err := install.EditProfile(path, updated)
					if err != nil {
						status.SetText("Couldn't save: " + err.Error())
						return
					}
					saved = true
					a.log("gui", "profile edit", []string{path}, "success", nil, time.Now())
					walk.MsgBox(dialog, "Saved", "Saved settings only — this PC's Windows printer is unchanged.\r\n"+
						"Use Update to match to review and apply this change. Changing the printer name creates a separate queue when applied.\r\n\r\n"+
						"The previous version is kept at:\r\n"+backup, walk.MsgBoxIconInformation)
					dialog.Accept()
				}},
			}},
		},
	}).Create(owner)
	if err != nil {
		showErr(owner, "Edit setup", err)
		return false
	}
	a.runDialog(dialog)
	return saved
}
