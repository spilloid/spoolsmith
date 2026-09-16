//go:build windows

package main

import (
	"os"
	"strings"

	"github.com/tailscale/walk"
	"golang.org/x/sys/windows"
)

type reviewUI struct {
	summaryLabel    *walk.Label
	reviewHint      *walk.Label
	accessStatus    *walk.Label
	networkStatus   *walk.Label
	planDetailsBtn  *walk.PushButton
	advancedCheck   *walk.CheckBox
	advancedPanel   *walk.Composite
	offlineCheck    *walk.CheckBox
	updateCheck     *walk.CheckBox
	familyRow       *walk.Composite
	purgeRow        *walk.Composite
	networkTouched  bool
	networkCanceled bool
	// pending is the operation the originating screen described. The review
	// screen shows it and runs it; it never asks the operator to restate it.
	pending operation
}

func (a *app) initializeReview() {
	a.updateReviewControls()
	if windows.GetCurrentProcessToken().IsElevated() {
		a.accessStatus.SetText("Administrator mode. Printer changes always require your confirmation.")
	} else {
		a.accessStatus.SetText("To install or remove printers, close the app and choose Run as administrator.")
	}
}

// startOperation hands one fully described intention to the review screen.
//
// Every screen that can begin a change goes through here, so the review screen
// always knows what it is reviewing without inspecting widgets that belong to
// other pages.
func (a *app) startOperation(op operation) {
	if a.mutationBusy {
		walk.MsgBox(a.mw, "Please wait", "Finish the current printer operation before starting another.", walk.MsgBoxIconInformation)
		return
	}
	a.pending = op.normalized()
	a.resetPending()
	a.offlineCheck.SetChecked(false)
	a.updateCheck.SetChecked(false)
	a.purgeDriverCheck.SetChecked(op.PurgeDriver)
	a.forceFamilyCombo.SetCurrentIndex(0)
	a.updateReviewControls()
	a.planOut.SetText("Choose Preview changes to see exactly what will happen. Nothing is changed until you confirm.")
	a.reviewHint.SetText("Nothing has changed yet.")
	a.tabs.SetCurrentIndex(tabReview)
	if a.previewBtn != nil {
		a.previewBtn.SetFocus()
	}
}

// currentOperation folds the advanced controls into the pending operation.
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

// updateReviewControls shows only the options that apply to this operation.
//
// The previous screen kept every advanced control visible and enabled or
// disabled them, which asked the operator to read a row of greyed-out choices
// to work out which ones were relevant. Hiding them makes the applicable set
// obvious, and an option that is not shown cannot be set for an operation it
// does not apply to.
func (a *app) updateReviewControls() {
	if a.summaryLabel == nil {
		return
	}
	op := a.currentOperation()
	busy := a.mutationBusy

	summary := op.Summary()
	if summary == "" {
		summary = "Choose a printer from This PC, or add one, to see its changes here."
	}
	a.summaryLabel.SetText(summary)

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
			control.SetEnabled(!busy)
		}
	}
	if a.previewBtn != nil {
		a.previewBtn.SetEnabled(!busy && op.Kind != "")
	}
	if a.executeBtn != nil {
		caption := op.Title()
		if op.Kind == "" {
			caption = "Apply"
		}
		a.executeBtn.SetText(caption + "...")
	}
}

func (a *app) startNetworkDiscovery() {
	a.discoverCIDR.TextChanged().Attach(func() { a.networkTouched = true })
	a.discoverBtn.Clicked().Attach(func() { a.networkTouched = true })
	if os.Getenv("SPOOLSMITH_GUI_NO_AUTOSCAN") == "1" {
		a.networkStatus.SetText("Automatic scan is off. Enter a network or printer IP to scan.")
		a.discoverOut.SetText("Enter a network above to scan, or enter one printer address and choose Use IP directly.")
		return
	}
	a.discoverCancelBtn.SetEnabled(true)
	go func() {
		cidr, description, err := recommendedNetwork()
		a.mw.Synchronize(func() {
			if a.discoverCancel == nil {
				a.discoverCancelBtn.SetEnabled(false)
			}
			if a.networkTouched || a.networkCanceled || a.discoverCancel != nil {
				if a.networkCanceled {
					a.networkStatus.SetText("Automatic scan canceled. Enter a network or printer IP when ready.")
				}
				return
			}
			if err != nil {
				a.networkStatus.SetText("Choose a network manually, or continue with a known printer IP.")
				a.discoverOut.SetText(err.Error())
				return
			}
			a.networkStatus.SetText(description)
			a.discoverCIDR.SetText(cidr)
			a.onDiscover()
		})
	}()
}

func (a *app) onCancelDiscovery() {
	a.networkCanceled = true
	if a.discoverCancel != nil {
		a.discoverCancel()
		a.discoverOut.SetText("Stopping the scan. Printers already found will remain available.")
	} else {
		a.discoverCancelBtn.SetEnabled(false)
		a.discoverOut.SetText("Automatic scan canceled. You can scan a network or enter a printer IP.")
	}
}

func (a *app) invalidateReview() {
	if a.mutationBusy {
		return
	}
	hadPreview := a.previewJSON != ""
	a.resetPending()
	a.updateReviewControls()
	if hadPreview {
		a.planOut.SetText("Options changed. Choose Preview changes to review the updated plan.")
		a.reviewHint.SetText("Preview again before applying the changed options.")
	}
}

func friendlyOperationError(message string) string {
	if strings.Contains(message, "administrator") || strings.Contains(message, "Administrator") {
		return "Administrator access is needed. Close SpoolSmith, right-click the app and choose Run as administrator. Your saved printer settings will still be available."
	}
	if strings.Contains(message, "driver not found") {
		return "This driver is not installed on this computer. Install the compatible vendor driver, then return to Add a printer and refresh the driver list."
	}
	return message
}
