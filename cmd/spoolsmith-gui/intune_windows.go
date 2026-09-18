//go:build windows

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"

	"github.com/spilloid/spoolsmith/internal/intune"
	"github.com/tailscale/walk"
	. "github.com/tailscale/walk/declarative"
)

// The wizard packages local files; it never installs a queue or accesses a tenant.
func (a *app) onIntuneWizard() {
	var dialog *walk.Dialog
	var profile, binary, pin, id, revision, display, location, description, output *walk.LineEdit
	var offline, prerequisite, adopt *walk.CheckBox
	var pages *walk.TabWidget
	var preview *walk.TextEdit
	var exportButton *walk.PushButton
	var prepared *intune.Prepared
	var reviewedOutput string
	browse := func(edit **walk.LineEdit, filter string) {
		picker := walk.FileDialog{Title: "Select a local file", Filter: filter}
		if ok, err := picker.ShowOpen(dialog); err != nil {
			showErr(dialog, "Select file", err)
		} else if ok {
			(*edit).SetText(picker.FilePath)
		}
	}
	review := func() {
		prepared = nil
		exportButton.SetEnabled(false)
		rev, _ := strconv.Atoi(revision.Text())
		opts := intune.Options{ProfilePath: profile.Text(), BinaryPath: binary.Text(), BinarySHA256: pin.Text(), ID: id.Text(), Revision: rev, DisplayName: display.Text(), Location: location.Text(), Description: description.Text(), Offline: offline.Checked(), DriverPrerequisite: prerequisite.Checked(), Adopt: adopt.Checked()}
		candidate, err := intune.Prepare(opts)
		if err != nil {
			showErr(dialog, "Package validation", err)
			return
		}
		if output.Text() == "" {
			showErr(dialog, "Export folder", fmt.Errorf("enter a new export folder"))
			return
		}
		prepared = candidate
		reviewedOutput = output.Text()
		data, _ := json.MarshalIndent(candidate.Manifest, "", "  ")
		preview.SetText("Export folder: " + reviewedOutput + "\r\n\r\n" + string(data))
		pages.SetCurrentIndex(2)
		exportButton.SetEnabled(true)
	}
	err := (Dialog{AssignTo: &dialog, Title: "Build an Intune printer app", MinSize: Size{Width: 760, Height: 560}, Size: Size{Width: 880, Height: 680}, Layout: VBox{Spacing: 10}, Children: []Widget{
		Label{Text: "Package one prevalidated printer for Required or Company Portal deployment."},
		TabWidget{AssignTo: &pages, OnCurrentIndexChanged: func() {
			if exportButton != nil && pages.CurrentIndex() != 2 {
				prepared = nil
				exportButton.SetEnabled(false)
			}
		}, Pages: []TabPage{
			{Title: "1. Profile and driver", Layout: VBox{Spacing: 10}, Children: []Widget{
				Label{Text: "Select a profile captured and validated by an administrator. Only Windows x64 is supported."},
				Composite{Layout: HBox{}, Children: []Widget{LineEdit{AssignTo: &profile, CueBanner: "Profile JSON file", Accessibility: name("intune-profile")}, PushButton{Text: "Browse profile...", OnClicked: func() { browse(&profile, "JSON files (*.json)|*.json") }}}},
				Composite{Layout: HBox{}, Children: []Widget{LineEdit{AssignTo: &binary, CueBanner: "SpoolSmith CLI .exe with offline and status support", Accessibility: name("intune-binary")}, PushButton{Text: "Browse CLI...", OnClicked: func() { browse(&binary, "Windows executable (*.exe)|*.exe") }}}},
				Label{Text: "Review this binary's SHA-256 before export; v0.4.0 lacks the required commands."},
				Composite{Layout: HBox{}, Children: []Widget{LineEdit{AssignTo: &pin, CueBanner: "Approved CLI SHA-256", Accessibility: name("intune-binary-sha256")}, PushButton{Text: "Calculate hash", OnClicked: func() {
					f, e := os.Open(binary.Text())
					if e != nil {
						showErr(dialog, "Binary hash", e)
						return
					}
					defer f.Close()
					h := sha256.New()
					if _, e = io.Copy(h, f); e != nil {
						showErr(dialog, "Binary hash", e)
						return
					}
					pin.SetText(hex.EncodeToString(h.Sum(nil)))
				}}}},
				CheckBox{AssignTo: &prerequisite, Text: "Driver is managed separately and will be registered before installation"},
				Label{Text: "Leave this unchecked to include the profile's supported local archive. Arbitrary OEM installers are unsupported."},
				VSpacer{}, PushButton{Text: "Next: Deployment", OnClicked: func() { pages.SetCurrentIndex(1) }},
			}},
			{Title: "2. Deployment", Layout: VBox{Spacing: 8}, Children: []Widget{
				Composite{Layout: Grid{Columns: 2, Spacing: 8}, Children: []Widget{
					Label{Text: "Stable deployment ID:"}, LineEdit{AssignTo: &id, CueBanner: "accounting-copier", Accessibility: name("intune-id")},
					Label{Text: "Revision:"}, LineEdit{AssignTo: &revision, Text: "1", Accessibility: name("intune-revision")},
					Label{Text: "App display name:"}, LineEdit{AssignTo: &display, Accessibility: name("intune-display-name")},
					Label{Text: "Location:"}, LineEdit{AssignTo: &location, Accessibility: name("intune-location")},
					Label{Text: "Description:"}, LineEdit{AssignTo: &description, Accessibility: name("intune-description")},
					Label{Text: "New export folder:"}, LineEdit{AssignTo: &output, CueBanner: "C:\\Packages\\accounting-r1", Accessibility: name("intune-output")},
				}},
				CheckBox{AssignTo: &offline, Text: "Provision offline: skip live identity validation"},
				Label{Text: "Default: verify live identity. Offline uses the prevalidated profile; printing still needs network connectivity."},
				CheckBox{AssignTo: &adopt, Text: "Allow adoption of an existing, exactly matching unmanaged queue"},
				Label{Text: "Updates keep the same ID and queue name. Increase the revision for configuration changes."},
				VSpacer{}, PushButton{Text: "Validate and preview package", OnClicked: review},
			}},
			{Title: "3. Review and export", Layout: VBox{Spacing: 8}, Children: []Widget{
				Label{Text: "Review the commands, payload hashes and policy. Export creates local files."},
				TextEdit{AssignTo: &preview, ReadOnly: true, VScroll: true, HScroll: true, Accessibility: name("intune-preview")},
				Label{Text: "After export, README.txt guides content preparation and Intune setup. Pilot on Windows before broad deployment."},
				PushButton{AssignTo: &exportButton, Text: "Export reviewed package", Enabled: false, OnClicked: func() {
					if prepared == nil {
						return
					}
					if err := prepared.Export(reviewedOutput); err != nil {
						showErr(dialog, "Export", err)
						return
					}
					exportButton.SetEnabled(false)
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
	a.runDialog(dialog)
}
