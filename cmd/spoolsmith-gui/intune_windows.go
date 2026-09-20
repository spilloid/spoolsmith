//go:build windows

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

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
	var exportButton, prepButton *walk.PushButton
	var prepTool, prepOut *walk.LineEdit
	var prepToolBrowse, prepOutBrowse *walk.PushButton
	var prepStatus *walk.Label
	var prepared *intune.Prepared
	var reviewedOutput, suggestedOutput, suggestedPrepOutput, exportedFolder string
	var stopPrep context.CancelFunc
	running := false
	var previous intune.Options
	// Microsoft's tool is optional, but when the administrator already put it
	// beside this executable there is nothing to ask: offer it filled in.
	var exeDir string
	if exe, err := os.Executable(); err == nil {
		exeDir = filepath.Dir(exe)
	}
	foundTool := intune.FindContentPrepTool(exeDir)
	invalidate := func() {
		prepared = nil
		if exportButton != nil {
			exportButton.SetEnabled(false)
		}
	}
	// setBusy locks the page while Microsoft's tool runs, and otherwise restores
	// each button to what the current review and export state allow.
	setBusy := func(busy bool) {
		running = busy
		for _, control := range []walk.Widget{prepTool, prepOut, prepToolBrowse, prepOutBrowse} {
			control.SetEnabled(!busy)
		}
		exportButton.SetEnabled(!busy && prepared != nil)
		prepButton.SetEnabled(!busy && exportedFolder != "")
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
		// A new review is a new export: the previous folder is no longer what this
		// page is about, and its package output belongs beside the new one.
		exportedFolder = ""
		prepStatus.SetText("")
		if next := intune.SuggestPrepOutput(reviewedOutput); prepOut.Text() == "" || prepOut.Text() == suggestedPrepOutput {
			prepOut.SetText(next)
			suggestedPrepOutput = next
		}
		setBusy(false)
	}
	// runPrep packages an exported folder with Microsoft's own tool. It runs off
	// the UI thread and can be stopped by closing the dialog; the tool is killed
	// with its context, and the exported folder is left exactly as it was.
	runPrep := func(source string) {
		tool, out := strings.TrimSpace(prepTool.Text()), strings.TrimSpace(prepOut.Text())
		ctx, stop := context.WithTimeout(context.Background(), 10*time.Minute)
		stopPrep = stop
		setBusy(true)
		prepStatus.SetText("Creating the .intunewin package with Microsoft's tool...")
		started := time.Now()
		go func() {
			defer stop()
			text, err := intune.PrepareContent(ctx, tool, source, out)
			a.log("gui", "intune content prep", []string{out}, statusOf(err), err, started)
			a.mw.Synchronize(func() {
				setBusy(false)
				switch {
				case err == nil:
					prepStatus.SetText("Created " + filepath.Join(out, intune.PreparedPackageName))
					walk.MsgBox(dialog, "Package ready", "Created "+filepath.Join(out, intune.PreparedPackageName)+"\n\nUpload it as a Windows app (Win32) in Intune, using the commands in README.txt.", walk.MsgBoxOK|walk.MsgBoxIconInformation)
				case errors.Is(ctx.Err(), context.Canceled):
					prepStatus.SetText("Stopped. The exported folder is unchanged.")
				default:
					prepStatus.SetText("The .intunewin package was not created.")
					detail := strings.TrimSpace(text)
					if len(detail) > 1500 {
						detail = "..." + detail[len(detail)-1500:]
					}
					if detail != "" {
						detail = "\n\nTool output:\n" + detail
					}
					walk.MsgBox(dialog, "Content prep failed", "The package folder was exported, but Microsoft's tool did not produce a .intunewin file: "+err.Error()+detail, walk.MsgBoxOK|walk.MsgBoxIconError)
				}
			})
		}()
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
				Label{Text: "Optional: also create the .intunewin file with Microsoft's Win32 Content Prep Tool (IntuneWinAppUtil.exe)."},
				Label{Text: "It is filled in when it sits beside this program. Otherwise README.txt has the command to run yourself."},
				Composite{Layout: formGrid(3), Children: []Widget{
					Label{Text: "Content Prep tool:"},
					LineEdit{AssignTo: &prepTool, Text: foundTool, CueBanner: "Optional: path to IntuneWinAppUtil.exe", Accessibility: name("intune-prep-tool")},
					PushButton{AssignTo: &prepToolBrowse, Text: "Browse...", OnClicked: func() {
						browse(&prepTool, "IntuneWinAppUtil (IntuneWinAppUtil.exe)|IntuneWinAppUtil.exe|Windows executable (*.exe)|*.exe")
					}},
					Label{Text: ".intunewin output folder:"},
					LineEdit{AssignTo: &prepOut, Accessibility: name("intune-prep-output")},
					PushButton{AssignTo: &prepOutBrowse, Text: "Browse...", OnClicked: func() {
						picker := walk.FileDialog{Title: "Choose a folder for the .intunewin package", FilePath: prepOut.Text()}
						if ok, err := picker.ShowBrowseFolder(dialog); err != nil {
							showErr(dialog, "Choose folder", err)
						} else if ok {
							prepOut.SetText(picker.FilePath)
						}
					}},
				}},
				Label{AssignTo: &prepStatus, Text: "", Accessibility: name("intune-prep-status")},
				Composite{Layout: HBox{MarginsZero: true, Spacing: 8}, Children: []Widget{
					PushButton{Text: "Back to settings", OnClicked: func() { pages.SetCurrentIndex(0) }},
					PushButton{AssignTo: &exportButton, Text: "Export reviewed package", Enabled: false, OnClicked: func() {
						if prepared == nil || running {
							return
						}
						// Refuse an unusable tool or output before exporting anything.
						tool := strings.TrimSpace(prepTool.Text())
						if tool != "" {
							if _, _, err := intune.CheckContentPrep(tool, reviewedOutput, prepOut.Text()); err != nil {
								showErr(dialog, "Content prep", err)
								return
							}
						}
						if err := prepared.Export(reviewedOutput); err != nil {
							showErr(dialog, "Export", err)
							return
						}
						exportedFolder = reviewedOutput
						invalidate()
						setBusy(false)
						if tool == "" {
							prepStatus.SetText("Exported. Choose Microsoft's tool above to create the .intunewin file, or follow README.txt.")
							walk.MsgBox(dialog, "Package exported", "Open README.txt in "+reviewedOutput+" for Microsoft Content Prep and Intune instructions, or choose IntuneWinAppUtil.exe on this page and create the .intunewin file here.", walk.MsgBoxOK|walk.MsgBoxIconInformation)
							return
						}
						runPrep(reviewedOutput)
					}},
					PushButton{AssignTo: &prepButton, Text: "Create .intunewin from the exported folder", Enabled: false, OnClicked: func() {
						if running || exportedFolder == "" {
							return
						}
						if _, _, err := intune.CheckContentPrep(prepTool.Text(), exportedFolder, prepOut.Text()); err != nil {
							showErr(dialog, "Content prep", err)
							return
						}
						runPrep(exportedFolder)
					}},
				}},
			}},
		}},
		PushButton{Text: "Close", OnClicked: func() { dialog.Accept() }},
	}}).Create(a.mw)
	if err != nil {
		showErr(a.mw, "Intune packaging", err)
		return
	}
	dialog.Closing().Attach(func(canceled *bool, _ walk.CloseReason) {
		if running {
			*canceled = true
			if stopPrep != nil {
				stopPrep()
			}
		}
	})
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
