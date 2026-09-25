//go:build windows

package main

import (
	"os"
	"strings"
	"time"

	"github.com/spilloid/spoolsmith/internal/bundle"
	"github.com/spilloid/spoolsmith/internal/install"
	"github.com/tailscale/walk"
	. "github.com/tailscale/walk/declarative"
	"github.com/tailscale/win"
	"golang.org/x/sys/windows"
)

// The apply sheet is where every change to Windows is reviewed and run: a
// printer file opened, dropped, pasted or double-clicked in Explorer, a
// scanned or saved printer, a removal or an address change from This PC.
//
// It replaced the "Review and apply" page, which could be opened empty from
// the sidebar and showed its plan as a transcript behind a Preview button.
// The sheet has no empty state because it is only ever opened with something
// to review, prepares the preview itself, and shows the plan as the steps it
// will take. The transcript is still one click away under Details, and the
// one native confirmation of that plan still stands between the button and
// any change.
type reviewUI struct {
	sheetTitle     *walk.Label
	sheetSubtitle  *walk.Label
	sheetSource    *walk.Label
	sheetWarnings  *walk.Label
	sheetSteps     *walk.Label
	reviewHint     *walk.Label
	networkStatus  *walk.Label
	planDetailsBtn *walk.PushButton
	copyNotesBtn   *walk.PushButton
	advancedCheck  *walk.CheckBox
	advancedPanel  *walk.Composite
	detailsCheck   *walk.CheckBox
	detailsPanel   *walk.Composite
	offlineCheck   *walk.CheckBox
	updateCheck    *walk.CheckBox
	familyRow      *walk.Composite
	purgeRow       *walk.Composite
	networkTouched bool
	// pending is the operation the originating screen described. The sheet
	// shows it and runs it; it never asks the operator to restate it.
	pending operation
	// sheetManifest is the printer file being reviewed, when there is one.
	sheetManifest *bundle.Manifest
	// sheetOutcome is the latest preview or execution result on the sheet.
	sheetOutcome *install.Outcome
	// sheetDone is set once the reviewed plan has run; the sheet then shows
	// the result and its main button only closes it.
	sheetDone bool
	// needsElevation is set when the preview is complete but this process
	// cannot apply it. The main button then relaunches as administrator.
	needsElevation bool
	// returnTo is where Back and Done go.
	returnTo page
	// settingOptions is set while startOperation loads the option controls,
	// so their change events do not each start a preview.
	settingOptions bool
	// previewStale is set when an option changes while a preview is being
	// prepared; that preview is discarded and prepared again.
	previewStale bool
}

// sheetPage is the apply sheet's layout.
func sheetPage(a *app) Composite {
	return contentPage(a, pageReview,
		Composite{Layout: row(), Children: []Widget{
			PushButton{Text: "‹ Back", OnClicked: a.closeSheet, Accessibility: name("sheet-back")},
			HSpacer{},
		}},
		Label{AssignTo: &a.sheetTitle, Text: "Printer", Font: Font{Family: "Segoe UI", PointSize: 18, Bold: true}, TextColor: colorBrand},
		Label{AssignTo: &a.sheetSubtitle, Text: " ", Font: Font{Family: "Segoe UI", PointSize: 11}},
		Label{AssignTo: &a.sheetSource, Text: " ", TextColor: colorHint},
		Label{AssignTo: &a.sheetWarnings, Text: " ", TextColor: colorWarning, Visible: false},
		Composite{Background: SolidColorBrush{Color: colorSurface}, Layout: VBox{Margins: Margins{Left: 14, Top: 12, Right: 14, Bottom: 12}, Spacing: 8}, Children: []Widget{
			Label{AssignTo: &a.reviewHint, Text: "Preparing...", TextColor: colorHint, Background: SolidColorBrush{Color: colorSurface}},
			Label{AssignTo: &a.sheetSteps, Text: " ", Font: Font{Family: "Segoe UI", PointSize: 11}, Background: SolidColorBrush{Color: colorSurface}},
			// Stretches the box to the page width.
			HSpacer{},
		}},
		Composite{AssignTo: &a.advancedPanel, Visible: false, Layout: VBox{MarginsZero: true, Spacing: 8}, Children: []Widget{
			CheckBox{AssignTo: &a.updateCheck, Text: "Update an existing queue to match this printer file"},
			CheckBox{AssignTo: &a.offlineCheck, Text: "Offline setup — the printer will not be contacted or checked"},
			Composite{AssignTo: &a.familyRow, Layout: row(), Children: []Widget{
				Label{Text: "Printer family:"},
				ComboBox{AssignTo: &a.forceFamilyCombo, Model: a.familyLabels, CurrentIndex: 0, Accessibility: name("mutate-force-family")},
			}},
			Composite{AssignTo: &a.purgeRow, Layout: row(), Children: []Widget{
				CheckBox{AssignTo: &a.purgeDriverCheck, Text: "Also remove the driver, if nothing else uses it"},
				HSpacer{},
			}},
			CheckBox{AssignTo: &a.dryRunOnlyCheck, Text: "Preview only (never apply)"},
		}},
		Composite{AssignTo: &a.detailsPanel, Visible: false, Layout: VBox{MarginsZero: true, Spacing: 6}, Children: []Widget{
			TextEdit{AssignTo: &a.planOut, ReadOnly: true, VScroll: true, MinSize: Size{Height: 120}, Accessibility: name("mutate-output")},
			Composite{Layout: row(), Children: []Widget{
				PushButton{AssignTo: &a.planDetailsBtn, Text: "Full plan and result", Enabled: false, OnClicked: a.onPlanDetails},
				PushButton{AssignTo: &a.previewBtn, Text: "Check again", OnClicked: a.onPreview},
				HSpacer{},
			}},
		}},
		VSpacer{},
		Composite{Layout: row(), Children: []Widget{
			CheckBox{AssignTo: &a.detailsCheck, Text: "Details", OnCheckedChanged: a.onToggleDetails},
			CheckBox{AssignTo: &a.advancedCheck, Text: "More options", OnCheckedChanged: a.onToggleAdvanced},
			HSpacer{},
			PushButton{AssignTo: &a.copyNotesBtn, Text: "Copy notes for the ticket", Visible: false, OnClicked: a.onCopyNotes},
			PushButton{AssignTo: &a.executeBtn, Text: "Install", Enabled: false, OnClicked: a.onExecute, MinSize: Size{Width: 150}},
		}},
	)
}

func (a *app) initializeReview() {
	a.returnTo = pageThisPC
	a.updateReviewControls()
}

// isElevated reports whether this process is running as Administrator. Every
// screen that offers to carry a driver payload -- which needs the protected
// driver store -- checks this up front, so the checkbox that can never
// succeed is disabled with its reason instead of failing after the operator
// has already committed to it.
func isElevated() bool {
	return windows.GetCurrentProcessToken().IsElevated()
}

// startOperation opens the sheet for one fully described intention and
// prepares its preview straight away.
//
// Every way of beginning a change goes through here, so the sheet always
// knows what it is reviewing without inspecting widgets on other pages.
func (a *app) startOperation(op operation) {
	if a.mutationBusy {
		walk.MsgBox(a.mw, "Please wait", "Finish the current printer operation before starting another.", walk.MsgBoxIconInformation)
		return
	}
	if a.current != pageReview {
		a.returnTo = a.current
		if a.returnTo < 0 {
			a.returnTo = pageThisPC
		}
	}
	a.pending = op.normalized()
	a.sheetManifest = nil
	if path := shownOr(a.pending.BundlePath, a.pending.ProfilePath); strings.TrimSpace(path) != "" {
		if opened, err := bundle.Open(path); err == nil {
			manifest := opened.Manifest
			a.sheetManifest = &manifest
			opened.Close()
		}
	}
	a.sheetDone = false
	a.sheetOutcome = nil
	a.resetPending()
	a.previewStale = false
	a.settingOptions = true
	a.offlineCheck.SetChecked(a.pending.Offline)
	a.updateCheck.SetChecked(a.pending.UpdateExisting)
	a.purgeDriverCheck.SetChecked(a.pending.PurgeDriver)
	a.forceFamilyCombo.SetCurrentIndex(0)
	a.settingOptions = false
	setShown(a.copyNotesBtn, false)
	a.renderSheet()
	a.goTo(pageReview)
	a.onPreview()
}

// closeSheet leaves the sheet for wherever it was opened from.
func (a *app) closeSheet() {
	if a.mutationExecuting {
		return
	}
	a.goTo(a.returnTo)
}

// currentOperation folds the option controls into the pending operation.
//
// The controls are read here rather than written back into pending as they
// change, so there is one direction of flow and no way for a stale widget to
// disagree with the operation being run. normalized() then drops anything that
// does not apply to this kind.
func (a *app) currentOperation() operation {
	op := a.pending
	if a.updateCheck != nil {
		op.UpdateExisting = a.updateCheck.Checked()
	}
	if a.offlineCheck != nil {
		op.Offline = a.offlineCheck.Checked()
	}
	if op.Kind == opRemove && a.purgeDriverCheck != nil {
		op.PurgeDriver = a.purgeDriverCheck.Checked()
	}
	if op.Kind == opInstall && strings.TrimSpace(op.ProfilePath) == "" && a.forceFamilyCombo != nil {
		if index := a.forceFamilyCombo.CurrentIndex(); index > 0 && index < len(a.familyIDs) {
			op.ForceFamily = a.familyIDs[index]
		}
	}
	return op.normalized()
}

func (a *app) onToggleAdvanced() {
	if a.advancedPanel != nil {
		a.advancedPanel.SetVisible(a.advancedCheck.Checked())
		a.updateReviewControls()
	}
}

func (a *app) onToggleDetails() {
	if a.detailsPanel != nil {
		a.detailsPanel.SetVisible(a.detailsCheck.Checked())
	}
}

// renderSheet draws the header, warnings and checklist from what the sheet
// currently knows: the operation and file before a preview, then the plan,
// then the result.
func (a *app) renderSheet() {
	if a.sheetTitle == nil {
		return
	}
	op := a.currentOperation()
	var plan *install.Plan
	outcome := install.Outcome{}
	if a.sheetOutcome != nil {
		outcome = *a.sheetOutcome
		plan = outcome.Plan
	}
	header := headerFor(op, plan, a.sheetManifest)
	a.sheetTitle.SetText(header.Title)
	a.sheetSubtitle.SetText(shownOr(header.Subtitle, op.Summary()))
	a.sheetSource.SetText(shownOr(header.Source, " "))
	setShown(a.sheetSource, header.Source != "")
	warnings := sheetWarnings(outcome, a.sheetManifest)
	a.sheetWarnings.SetText(shownOr(wrapText(strings.Join(warnings, "\n"), sheetWrap), " "))
	setShown(a.sheetWarnings, len(warnings) > 0)
	steps := checklistText(planChecklist(outcome), "\r\n")
	a.sheetSteps.SetText(shownOr(wrapText(steps, sheetWrap), " "))
	setShown(a.sheetSteps, steps != "")
	a.updateReviewControls()
}

// updateReviewControls shows only the options that apply to this operation,
// and sets the main button for what pressing it will do now.
//
// Hiding options that do not apply makes the applicable set obvious, and an
// option that is not shown cannot be set for an operation it does not apply to.
func (a *app) updateReviewControls() {
	if a.executeBtn == nil {
		return
	}
	op := a.currentOperation()
	busy := a.mutationBusy

	if a.familyRow != nil {
		a.familyRow.SetVisible(op.Kind == opInstall && strings.TrimSpace(op.ProfilePath) == "")
	}
	if a.purgeRow != nil {
		a.purgeRow.SetVisible(op.Kind == opRemove)
	}
	if a.offlineCheck != nil {
		// Offline only means something when the printer would otherwise be
		// contacted. A removal and a repoint never contact it.
		offlineApplies := (op.Kind == opInstall && op.ProfilePath != "") || op.Kind == opConfigure || op.Kind == opApply
		a.offlineCheck.SetVisible(offlineApplies)
		a.offlineCheck.SetEnabled(offlineApplies && !busy)
	}
	a.updateCheck.SetVisible(op.Kind == opApply)
	a.updateCheck.SetEnabled(!busy)
	for _, control := range []walk.Widget{a.forceFamilyCombo, a.purgeDriverCheck, a.dryRunOnlyCheck} {
		if control != nil {
			control.SetEnabled(!busy && !a.sheetDone)
		}
	}
	if a.previewBtn != nil {
		a.previewBtn.SetEnabled(!busy && op.Kind != "" && !a.sheetDone)
	}
	caption := primaryCaption(op, isElevated())
	shield := !isElevated()
	switch {
	case a.sheetDone:
		caption, shield = "Done", false
	case a.dryRunOnlyCheck != nil && a.dryRunOnlyCheck.Checked():
		shield = false
		caption = strings.TrimSuffix(caption, " as administrator...")
	}
	// No trailing ellipsis once elevated: this button performs the reviewed
	// action after its confirmation, it does not open another window.
	a.executeBtn.SetText(caption)
	setShield(a.executeBtn, shield)
	a.executeBtn.SetEnabled(!busy && (a.sheetDone || a.hasPending() || a.needsElevation))
}

// setShield puts the UAC shield on a button whose action asks Windows for
// administrator rights.
func setShield(button *walk.PushButton, on bool) {
	var value uintptr
	if on {
		value = 1
	}
	button.SendMessage(win.BCM_SETSHIELD, 0, value)
}

func (a *app) invalidateReview() {
	if a.settingOptions || a.sheetDone || a.pending.Kind == "" || a.mutationExecuting {
		return
	}
	if a.mutationBusy {
		// A preview is being prepared with the old options.
		a.previewStale = true
		return
	}
	a.resetPending()
	a.onPreview()
}

// onCopyNotes puts plain-text notes about the finished change on the
// clipboard, for the ticket the work was done under.
func (a *app) onCopyNotes() {
	if a.sheetOutcome == nil {
		return
	}
	notes := ticketNotes(versionString(), hostName(), time.Now(), a.currentOperation(), *a.sheetOutcome)
	if err := walk.Clipboard().SetText(lines(notes)); err != nil {
		showErr(a.mw, "Copy notes", err)
		return
	}
	a.copyNotesBtn.SetText("Copied")
}

func (a *app) startNetworkDiscovery() {
	a.discoverCIDR.TextChanged().Attach(func() { a.networkTouched = true })
	a.discoverBtn.Clicked().Attach(func() { a.networkTouched = true })
	if os.Getenv("SPOOLSMITH_GUI_NO_AUTOSCAN") == "1" {
		a.networkStatus.SetText("Automatic scan is off. Enter a network or printer IP to scan.")
		a.otherNetworkShown = true
		a.otherNetworkBtn.SetText("Hide network or IP")
		return
	}
	a.networkStatus.SetText("Finding your network...")
	go func() {
		cidr, description, err := recommendedNetwork()
		a.mw.Synchronize(func() {
			if a.networkTouched || a.discoverCancel != nil {
				return
			}
			if err != nil {
				a.networkStatus.SetText("Couldn't pick a network to scan: " + err.Error())
				a.showOtherNetwork(true)
				return
			}
			a.networkStatus.SetText(description)
			a.discoverCIDR.SetText(cidr)
			a.networkTouched = false
			a.onDiscover()
		})
	}()
}

func (a *app) onCancelDiscovery() {
	if a.discoverCancel != nil {
		a.discoverCancel()
		a.discoverOut.SetText("Stopping the scan. Printers already found stay in the list.")
	}
}

// friendlyOperationError translates the handful of errors an operator hits
// often into plain guidance, with the original message kept underneath as a
// detail line -- translated, not replaced, so a case this doesn't recognize
// (or a report sent along with a ticket) still carries the real text.
func friendlyOperationError(message string) string {
	lead, ok := friendlyErrorLead(message)
	if !ok {
		return message
	}
	return lead + "\r\n\r\nDetails: " + message
}

func friendlyErrorLead(message string) (string, bool) {
	switch {
	case strings.Contains(message, "administrator") || strings.Contains(message, "Administrator"):
		return "Administrator access is needed. Use the button with the shield to continue as administrator; Windows will ask first.", true
	case strings.Contains(message, "driver not found"):
		return "This driver is not installed on this computer. Install the exact compatible Windows driver, then check again.", true
	case strings.Contains(message, "already exists"):
		return "A file with that name already exists. Choose a different name or location -- nothing here is ever overwritten automatically.", true
	case strings.Contains(message, "changed: saved"):
		return "This printer answered, but its identity does not match what was saved. Confirm it's still the same device -- a different printer may now be at this address -- before recapturing.", true
	case strings.Contains(message, "collect evidence") || strings.Contains(message, "did not identify itself") || strings.Contains(message, "could not be contacted"):
		return "The printer could not be reached. Check that it's powered on and connected to the network, then try again.", true
	case strings.Contains(message, "bundle:") || strings.Contains(message, "zip:"):
		return "This file could not be read as a printer file. It may be damaged, or not a SpoolSmith printer file at all.", true
	default:
		return "", false
	}
}

// setHint is the one line of guidance above the steps.
func (a *app) setHint(text string) {
	a.reviewHint.SetText(wrapText(text, sheetWrap))
}

// sheetWrap is how many characters a sheet line holds at the window's
// minimum width.
const sheetWrap = 96
