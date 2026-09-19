//go:build windows

package main

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"

	"github.com/spilloid/spoolsmith/internal/intune"
	"github.com/tailscale/walk"
	. "github.com/tailscale/walk/declarative"
)

// The wizard packages local files; it never installs a queue or accesses a tenant.
func (a *app) onIntuneWizard() {
	var dialog *walk.Dialog
	var profile, binary, pin, id, revision, display, location, description, output *walk.LineEdit
	var offline, prerequisite, adopt, advanced *walk.CheckBox
	var advancedPanel *walk.Composite
	var pages *walk.TabWidget
	var preview *walk.TextEdit
	var exportButton *walk.PushButton
	var prepared *intune.Prepared
	var reviewedOutput, suggestedOutput string
	var previous intune.Options
	invalidate := func() {
		prepared = nil
		if exportButton != nil {
			exportButton.SetEnabled(false)
		}
	}
	suggestOutput := func() {
		invalidate()
		if output == nil || profile == nil || id == nil || revision == nil {
			return
		}
		if output.Text() == "" || output.Text() == suggestedOutput {
			rev, _ := strconv.Atoi(revision.Text())
			path, err := intune.SuggestOutput(filepath.Dir(profile.Text()), id.Text(), rev)
			if err == nil {
				output.SetText(path)
				suggestedOutput = path
			}
		}
	}
	profileChanged := func() {
		invalidate()
		if profile == nil || id == nil || display == nil || description == nil {
			return
		}
		defaults, err := intune.ProfileDefaults(profile.Text())
		if err != nil {
			return // A partly typed path is validated when the user requests review.
		}
		for _, field := range []struct {
			edit      *walk.LineEdit
			old, next string
		}{{id, previous.ID, defaults.ID}, {display, previous.DisplayName, defaults.DisplayName}, {description, previous.Description, defaults.Description}} {
			if field.edit.Text() == "" || field.edit.Text() == field.old {
				field.edit.SetText(field.next)
			}
		}
		previous = defaults
		suggestOutput()
	}
	binaryChanged := func() {
		invalidate()
		if binary != nil && pin != nil {
			hash, _ := intune.HashBinary(binary.Text())
			pin.SetText(hash) // Clear any old pin, including when the new path is incomplete.
		}
	}
	browse := func(edit **walk.LineEdit, filter string) {
		picker := walk.FileDialog{Title: "Select a local file", Filter: filter}
		if ok, err := picker.ShowOpen(dialog); err != nil {
			showErr(dialog, "Select file", err)
		} else if ok {
			(*edit).SetText(picker.FilePath)
		}
	}
	review := func() {
		suggestOutput()
		rev, err := strconv.Atoi(revision.Text())
		if err != nil {
			showErr(dialog, "Package validation", fmt.Errorf("revision must be a positive integer"))
			return
		}
		opts := intune.Options{ProfilePath: profile.Text(), BinaryPath: binary.Text(), BinarySHA256: pin.Text(), ID: id.Text(), Revision: rev, DisplayName: display.Text(), Location: location.Text(), Description: description.Text(), Offline: offline.Checked(), DriverPrerequisite: prerequisite.Checked(), Adopt: adopt.Checked()}
		candidate, err := intune.Prepare(opts)
		if err != nil {
			showErr(dialog, "Package validation", err)
			return
		}
		if output.Text() == "" {
			showErr(dialog, "Export folder", fmt.Errorf("select an export folder"))
			return
		}
		reviewedOutput = output.Text()
		data, _ := json.MarshalIndent(candidate.Manifest, "", "  ")
		// lines() converts LF to CRLF for the native edit control; see its
		// doc comment in handlers_windows.go. Building this string with LF
		// throughout and converting once, rather than hand-mixing "\r\n" with
		// json.MarshalIndent's LF output, is what dev-process.md already
		// recorded once as the fix for an unreadable-plan regression.
		preview.SetText(lines("Export folder: " + reviewedOutput + "\n\n" + string(data)))
		pages.SetCurrentIndex(1)
		prepared = candidate
		exportButton.SetEnabled(true)
	}
	err := (Dialog{AssignTo: &dialog, Title: "Build an Intune printer app", MinSize: Size{Width: 760, Height: 560}, Size: Size{Width: 880, Height: 740}, Layout: VBox{Spacing: 10}, Children: []Widget{
		Label{Text: "Package one prevalidated printer for Required or Company Portal deployment."},
		TabWidget{AssignTo: &pages, OnCurrentIndexChanged: func() {
			if pages.CurrentIndex() != 1 {
				invalidate()
				if advancedPanel != nil && advanced != nil {
					advancedPanel.SetVisible(advanced.Checked())
				}
			}
		}, Pages: []TabPage{
			{Title: "1. Package settings", Layout: VBox{Spacing: 8}, Children: []Widget{
				Label{Text: "Choose a validated profile or SpoolSmith bundle (.ssb) and approved Windows x64 CLI. Review the package before exporting."},
				Composite{Layout: HBox{}, Children: []Widget{LineEdit{AssignTo: &profile, CueBanner: "Profile JSON or SpoolSmith bundle (.ssb) file", Accessibility: name("intune-profile"), OnTextChanged: profileChanged}, PushButton{Text: "Browse profile...", OnClicked: func() {
					browse(&profile, "Profile or bundle (*.json;*.ssb)|*.json;*.ssb|JSON files (*.json)|*.json|SpoolSmith bundle (*.ssb)|*.ssb|All files (*.*)|*.*")
				}}}},
				Composite{Layout: HBox{}, Children: []Widget{LineEdit{AssignTo: &binary, CueBanner: "SpoolSmith CLI .exe", Accessibility: name("intune-binary"), OnTextChanged: binaryChanged}, PushButton{Text: "Browse CLI...", OnClicked: func() { browse(&binary, "Windows executable (*.exe)|*.exe") }}}},
				Label{Text: "The CLI hash is calculated automatically and included in review. Use a build approved by your organization."},
				Composite{Layout: Grid{Columns: 2, Spacing: 8}, Children: []Widget{
					Label{Text: "App display name:"}, LineEdit{AssignTo: &display, Accessibility: name("intune-display-name"), OnTextChanged: invalidate},
					Label{Text: "Export folder:"}, LineEdit{AssignTo: &output, Accessibility: name("intune-output"), OnTextChanged: invalidate},
				}},
				Label{Text: "An unused folder beside the profile is suggested. You can edit the path; existing folders are never overwritten."},
				CheckBox{AssignTo: &prerequisite, Text: "Driver is managed separately and will be registered before installation", OnCheckedChanged: invalidate},
				Label{Text: "Leave unchecked to include the profile's supported local archive or the selected bundle's driver payload. A profile or bundle without one requires this choice."},
				CheckBox{AssignTo: &advanced, Text: "Advanced settings (updates, metadata and policy)", OnCheckedChanged: func() {
					if advancedPanel != nil {
						advancedPanel.SetVisible(advanced.Checked())
					}
				}},
				Composite{AssignTo: &advancedPanel, Layout: VBox{Spacing: 6}, Children: []Widget{
					Composite{Layout: Grid{Columns: 2, Spacing: 6}, Children: []Widget{
						Label{Text: "Stable deployment ID:"}, LineEdit{AssignTo: &id, Accessibility: name("intune-id"), OnTextChanged: suggestOutput},
						Label{Text: "Revision:"}, LineEdit{AssignTo: &revision, Text: "1", Accessibility: name("intune-revision"), OnTextChanged: suggestOutput},
						Label{Text: "Location (optional):"}, LineEdit{AssignTo: &location, Accessibility: name("intune-location"), OnTextChanged: invalidate},
						Label{Text: "Description:"}, LineEdit{AssignTo: &description, Accessibility: name("intune-description"), OnTextChanged: invalidate},
						Label{Text: "CLI SHA-256 (automatic; editable):"}, LineEdit{AssignTo: &pin, Accessibility: name("intune-binary-sha256"), OnTextChanged: invalidate},
					}},
					CheckBox{AssignTo: &offline, Text: "Offline setup — the installed app won't check the printer is really there", OnCheckedChanged: invalidate},
					CheckBox{AssignTo: &adopt, Text: "Allow adoption of an existing, exactly matching unmanaged queue", OnCheckedChanged: invalidate},
					Label{Text: "For updates, keep the existing ID and queue name and increase the revision. Reuse any previously chosen custom ID."},
				}},
				Label{Text: "Default: the app checks the printer is really there when it installs, no adoption, revision 1. Offline setup still needs connectivity for printing."},
				VSpacer{}, PushButton{Text: "Validate and preview package", OnClicked: review},
			}},
			{Title: "2. Review and export", Layout: VBox{Spacing: 8}, Children: []Widget{
				Label{Text: "Review the destination, deployment ID, commands, payload hashes and policy. Export creates local files."},
				TextEdit{AssignTo: &preview, ReadOnly: true, VScroll: true, HScroll: true, Accessibility: name("intune-preview")},
				Label{Text: "After export, README.txt guides content preparation and Intune setup. Pilot on Windows before broad deployment."},
				PushButton{Text: "Back to settings", OnClicked: func() { pages.SetCurrentIndex(0) }},
				PushButton{AssignTo: &exportButton, Text: "Export reviewed package", Enabled: false, OnClicked: func() {
					if prepared == nil {
						return
					}
					if err := prepared.Export(reviewedOutput); err != nil {
						showErr(dialog, "Export", err)
						return
					}
					invalidate()
					walk.MsgBox(dialog, "Package exported", "Open README.txt in "+reviewedOutput+" for Microsoft Content Prep and Intune instructions.", walk.MsgBoxOK|walk.MsgBoxIconInformation)
				}},
			}},
		}},
		PushButton{Text: "Close", OnClicked: func() { dialog.Accept() }},
	}}).Create(a.mw)
	if err != nil {
		showErr(a.mw, "Intune packaging", err)
		return
	}
	// Walk's visibility query includes ancestors. Apply the collapsed state
	// after the dialog becomes visible, and again when returning to settings.
	// Create has already created the child HWNDs. Hiding their parent may omit
	// them from UIA's tree; expanding does not require another creation pass.
	dialog.VisibleChanged().Attach(func() {
		if dialog.Visible() && pages.CurrentIndex() == 0 {
			advancedPanel.SetVisible(advanced.Checked())
		}
	})
	a.runDialog(dialog)
}
