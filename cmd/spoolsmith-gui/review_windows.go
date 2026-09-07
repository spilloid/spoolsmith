//go:build windows

package main

import (
	"os"
	"strings"

	"github.com/tailscale/walk"
	"golang.org/x/sys/windows"
)

type reviewUI struct {
	targetLabel     *walk.Label
	reviewHint      *walk.Label
	accessStatus    *walk.Label
	networkStatus   *walk.Label
	reviewBrowseBtn *walk.PushButton
	planDetailsBtn  *walk.PushButton
	advancedCheck   *walk.CheckBox
	advancedPanel   *walk.Composite
	networkTouched  bool
	networkCanceled bool
}

func (a *app) initializeReview() {
	a.updateReviewControls()
	if windows.GetCurrentProcessToken().IsElevated() {
		a.accessStatus.SetText("Administrator mode. Printer changes always require your confirmation.")
	} else {
		a.accessStatus.SetText("To install or remove printers, close the app and choose Run as administrator.")
	}
}

// setReviewMode selects one operation and clears the others. Native radio
// exclusivity only applies to a user's click inside the group: walk's
// SetChecked sets the single control it is called on, so handing a saved
// printer to this screen in code would otherwise leave the previous mode
// checked alongside the new one, and the preview could describe a different
// operation than the one shown as selected.
func (a *app) setReviewMode(mode string) {
	for _, choice := range []struct {
		radio    *walk.RadioButton
		selected bool
	}{
		{a.modeInstall, mode == "install"},
		{a.modeConfigure, mode == "configure"},
		{a.modeUninstall, mode == "remove"},
	} {
		choice.radio.SetChecked(choice.selected)
	}
}

func (a *app) onToggleAdvanced() {
	if a.advancedPanel != nil {
		a.advancedPanel.SetVisible(a.advancedCheck.Checked())
	}
}

// Context-sensitive controls keep a queue name, IP and profile path from being
// presented as interchangeable inputs. Hidden advanced values cannot leak into
// operations for which they do not apply.
func (a *app) updateReviewControls() {
	if a.targetLabel == nil {
		return
	}
	busy := a.mutationBusy
	profile := a.useProfileCheck.Checked()
	remove := a.modeUninstall.Checked()
	configure := a.modeConfigure.Checked()
	a.useProfileCheck.SetEnabled(!busy && !configure)
	a.forceFamilyCombo.SetEnabled(!busy && !profile && !remove && !configure)
	a.purgeDriverCheck.SetEnabled(!busy && remove)
	a.reviewBrowseBtn.SetEnabled(!busy)
	switch {
	case profile:
		a.targetLabel.SetText("Profile file:")
		a.targetField.SetCueBanner("Choose a saved printer or browse for a profile")
	case remove:
		a.targetLabel.SetText("Printer name:")
		a.targetField.SetCueBanner("Exact name shown in Windows Printers & scanners")
	default:
		a.targetLabel.SetText("Printer IP:")
		a.targetField.SetCueBanner("IP address; use Add printer to choose a driver")
	}
	caption := "Add printer..."
	if configure {
		caption = "Update settings..."
	}
	if remove {
		caption = "Remove printer..."
	}
	a.executeBtn.SetText(caption)
}

func (a *app) onBrowseReview() {
	if a.mutationBusy {
		return
	}
	dialog := walk.FileDialog{Title: "Choose printer settings", Filter: "Printer profiles (*.json)|*.json|All files (*.*)|*.*", FilePath: a.targetField.Text()}
	accepted, err := dialog.ShowOpen(a.mw)
	if err != nil {
		showErr(a.mw, "Choose profile", err)
		return
	}
	if !accepted {
		return
	}
	a.useProfileCheck.SetChecked(true)
	a.targetField.SetText(dialog.FilePath)
	a.onPreview()
}

func (a *app) startNetworkDiscovery() {
	a.discoverCIDR.TextChanged().Attach(func() { a.networkTouched = true })
	a.knownTarget.TextChanged().Attach(func() { a.networkTouched = true })
	a.discoverBtn.Clicked().Attach(func() { a.networkTouched = true })
	if os.Getenv("SPOOLSMITH_GUI_NO_AUTOSCAN") == "1" {
		a.networkStatus.SetText("Automatic scan is off. Enter a network or printer IP to scan.")
		a.discoverOut.SetText("Enter a network above, or continue with a known printer IP below.")
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
		a.planOut.SetText("Settings changed. Choose Preview changes to review the updated plan.")
		a.reviewHint.SetText("Preview again before applying the changed settings.")
	}
}

func friendlyOperationError(message string) string {
	if strings.Contains(message, "administrator privileges") {
		return "Administrator access is needed. Close SpoolSmith, right-click the app and choose Run as administrator. Your saved printer settings will still be available."
	}
	if strings.Contains(message, "driver not found") {
		return "This driver is not installed on this computer. Install the compatible vendor driver, then return to Add printer and refresh the driver list."
	}
	return message
}
