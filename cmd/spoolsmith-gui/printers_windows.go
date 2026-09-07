//go:build windows

package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spilloid/spoolsmith/internal/evidence"
	"github.com/spilloid/spoolsmith/internal/inspect"
	"github.com/spilloid/spoolsmith/internal/install"
	"github.com/spilloid/spoolsmith/internal/probe"
	"github.com/tailscale/walk"
)

type printerUI struct {
	discoverUseBtn, discoverDetailsBtn, discoverCustomizeBtn              *walk.PushButton
	knownTarget                                                           *walk.LineEdit
	captureStatus, driverStatus, editStatus                               *walk.Label
	captureBrowseBtn, profileBrowseBtn, profileDetailsBtn                 *walk.PushButton
	profileSetupBtn, profileConfigureBtn, profileRemoveBtn, cancelEditBtn *walk.PushButton
	profileEditor                                                         *walk.GroupBox
	discoveryJSON                                                         string
	driversLoading, driversLoaded, captureBusy, refreshingProfiles        bool
	captureSuggestedName, captureSuggestedFile                            string
	editLoadedValues                                                      [5]string
	// setupEvidence is what the printer being set up reported about itself.
	// It only ever seeds a driver suggestion the operator can overwrite.
	setupEvidence evidence.Evidence
}

func (a *app) initializePrinters() {
	if executable, err := os.Executable(); err == nil {
		a.profileDir.SetText(defaultProfileDirectory(executable))
	}
	a.captureTarget.TextChanged().Attach(a.suggestCaptureFields)
	a.discoverList.CurrentIndexChanged().Attach(a.updateDiscoveryActions)
	a.profileList.CurrentIndexChanged().Attach(func() {
		if !a.refreshingProfiles {
			a.onViewProfile()
		}
	})
	// Walk makes the newly current page visible before publishing this event,
	// which is the only point an optional panel's visibility can be applied:
	// while its page is hidden the control already reports itself invisible, so
	// walk's SetVisible sees no change and does nothing. A panel hidden during
	// startup would otherwise appear, empty, the first time its page is opened.
	a.tabs.CurrentIndexChanged().Attach(func() {
		switch a.tabs.CurrentIndex() {
		case tabSetup:
			if !a.driversLoaded {
				a.onDrivers()
			}
		case tabProfiles:
			a.profileEditor.SetVisible(a.editPath != "")
		case tabReview:
			a.advancedPanel.SetVisible(a.advancedCheck.Checked())
		}
	})
	a.profileEditor.SetVisible(false)
	a.updateDiscoveryActions()
	a.onRefreshProfiles()
	a.suggestCaptureFields()
}

func (a *app) profilesDirectory() string {
	if dir := strings.TrimSpace(a.profileDir.Text()); dir != "" {
		return dir
	}
	return "profiles"
}

func (a *app) onDiscover() {
	if a.discoverCancel != nil {
		return
	}
	cidr, err := discoveryNetwork(a.discoverCIDR.Text())
	if err != nil {
		a.discoverOut.SetText(err.Error())
		a.discoverCIDR.SetFocus()
		return
	}
	a.discoverBtn.SetEnabled(false)
	a.discoverCIDR.SetEnabled(false)
	a.discoverList.SetEnabled(false)
	a.discoverCancelBtn.SetEnabled(true)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	a.discoverCancel = cancel
	a.discovered = nil
	a.discoveryJSON = ""
	a.discoverList.SetModel([]string{})
	a.updateDiscoveryActions()
	a.discoverOut.SetText("Looking for printers on " + cidr + "...\r\nThis can take up to two minutes. You can cancel and use an IP address at any time.")
	start := time.Now()
	go func() {
		defer cancel()
		result, err := probe.Discover(ctx, cidr)
		response := struct {
			Network    string                  `json:"network"`
			Scanned    int                     `json:"scanned"`
			Candidates []inspect.InspectResult `json:"candidates"`
			Error      string                  `json:"error,omitempty"`
		}{Network: result.Network, Scanned: result.Scanned, Candidates: []inspect.InspectResult{}}
		for _, candidate := range result.Candidates {
			response.Candidates = append(response.Candidates, inspect.Inspect(candidate.Evidence))
		}
		status := "success"
		if err != nil {
			response.Error = err.Error()
			status = "error"
		}
		a.log("gui", "discover", []string{cidr}, status, err, start)
		a.mw.Synchronize(func() {
			a.discoverBtn.SetEnabled(true)
			a.discoverCIDR.SetEnabled(true)
			a.discoverList.SetEnabled(true)
			a.discoverCancelBtn.SetEnabled(false)
			a.discoverCancel = nil
			a.discoveryJSON = prettyJSON(response)
			a.discovered = result.Candidates
			labels := []string{}
			for _, candidate := range result.Candidates {
				label := printerIdentity(candidate.Evidence) + "  ·  " + candidate.Evidence.IP
				if len(savedProfilesForIP(a.profilesDirectory(), candidate.Evidence.IP)) > 0 {
					label += "  ·  Saved settings available"
				}
				labels = append(labels, label)
			}
			a.discoverList.SetModel(labels)
			// Every outcome names the network that was actually scanned, so a
			// result is never ambiguous about which subnet produced it.
			network := response.Network
			if network == "" {
				network = cidr
			}
			checked := fmt.Sprintf("%d %s", result.Scanned, plural(result.Scanned, "address", "addresses"))
			summary := fmt.Sprintf("Found %d possible %s on %s after checking %s.\r\nSelect a printer to continue. Confirm its model before choosing a driver.",
				len(labels), plural(len(labels), "printer", "printers"), network, checked)
			if len(labels) == 0 {
				summary = fmt.Sprintf("No printers found on %s after checking %s.\r\nCheck that the printer is awake and on this network, try another subnet, or enter its IP address below.", network, checked)
			}
			if errors.Is(err, context.Canceled) {
				summary = "Scan canceled.\r\n" + summary
			} else if errors.Is(err, context.DeadlineExceeded) {
				summary = "The scan reached its time limit. Results may be incomplete.\r\n" + summary
			} else if err != nil {
				summary = "Some addresses could not be checked.\r\n" + summary + "\r\nOpen scan details for more information."
			}
			a.discoverOut.SetText(summary)
			if len(labels) == 1 {
				a.discoverList.SetCurrentIndex(0)
			}
			a.updateDiscoveryActions()
		})
	}()
}

func (a *app) updateDiscoveryActions() {
	idx := a.discoverList.CurrentIndex()
	ready := a.discoverCancel == nil && idx >= 0 && idx < len(a.discovered) && !a.captureBusy && !a.mutationBusy
	a.discoverUseBtn.SetEnabled(ready)
	a.discoverUseBtn.SetText("Set up selected printer")
	if ready && len(savedProfilesForIP(a.profilesDirectory(), a.discovered[idx].Evidence.IP)) == 1 {
		a.discoverUseBtn.SetText("Review saved setup")
	}
	if a.discoverCustomizeBtn != nil {
		a.discoverCustomizeBtn.SetEnabled(ready)
	}
	a.discoverDetailsBtn.SetEnabled(a.discoveryJSON != "" && a.discoverCancel == nil)
}

func (a *app) onDiscoveryDetails() {
	if a.discoveryJSON != "" {
		a.showDetails("Scan details", a.discoveryJSON)
	}
}

func (a *app) onUseDiscovered() {
	if a.discoverCancel != nil || a.captureBusy || a.mutationBusy {
		return
	}
	idx := a.discoverList.CurrentIndex()
	if idx < 0 || idx >= len(a.discovered) {
		return
	}
	e := a.discovered[idx].Evidence
	if !a.reviewSavedPrinter(e.IP) {
		a.openPrinterSetup(e)
	}
}

func (a *app) onCustomizeDiscovered() {
	if a.discoverCancel != nil || a.captureBusy || a.mutationBusy {
		return
	}
	idx := a.discoverList.CurrentIndex()
	if idx >= 0 && idx < len(a.discovered) {
		a.openPrinterSetup(a.discovered[idx].Evidence)
	}
}

func (a *app) reviewSavedPrinter(target string) bool {
	matches := savedProfilesForIP(a.profilesDirectory(), target)
	if len(matches) == 1 {
		if a.selectProfileForReview(matches[0], "install") {
			a.onPreview()
		}
		return true
	}
	if len(matches) > 1 {
		a.onRefreshProfiles()
		a.selectSavedPath(matches[0])
		a.tabs.SetCurrentIndex(tabProfiles)
		a.profileOut.SetText(fmt.Sprintf("There are %d saved setups for %s. Select the printer name and driver you want, then choose Set up selected.\r\n\r\nTo create another setup, return to Find a printer and choose Set up with different settings.", len(matches), target))
		return true
	}
	return false
}

func (a *app) onKnownIP() {
	if a.captureBusy || a.mutationBusy {
		return
	}
	target := ""
	if a.knownTarget != nil {
		target = strings.TrimSpace(a.knownTarget.Text())
	}
	if target == "" {
		a.tabs.SetCurrentIndex(tabSetup)
		a.captureTarget.SetFocus()
		return
	}
	ip, err := netip.ParseAddr(target)
	if err != nil || ip.Zone() != "" {
		a.discoverOut.SetText("Enter the printer's IP address, such as 192.168.1.25. Use the network field above to scan a subnet.")
		a.knownTarget.SetFocus()
		return
	}
	if !a.reviewSavedPrinter(ip.String()) {
		a.openPrinterSetup(evidence.Evidence{IP: ip.String()})
	}
}

func (a *app) openPrinterSetup(e evidence.Evidence) {
	if a.captureBusy || a.mutationBusy {
		return
	}
	a.setupEvidence = e
	a.captureTarget.SetText(e.IP)
	if a.captureName.Text() == "" || a.captureName.Text() == a.captureSuggestedName {
		a.captureSuggestedName = printerIdentity(e)
		a.captureName.SetText(a.captureSuggestedName)
	}
	// Suggest the destination file explicitly rather than relying on the target
	// field's change handler, so arriving from discovery never lands on the
	// setup screen with an empty save path.
	a.suggestCaptureFile(e.IP)
	a.captureStatus.SetText("Printer: " + printerIdentity(e) + ". Choose its compatible Windows driver, then save and review.")
	a.tabs.SetCurrentIndex(tabSetup)
	a.onDrivers()
	a.captureDriver.SetFocus()
}

func (a *app) suggestCaptureFields() {
	if a.captureBusy {
		return
	}
	ip, err := netip.ParseAddr(strings.TrimSpace(a.captureTarget.Text()))
	if err != nil || ip.Zone() != "" {
		return
	}
	if name := a.captureName.Text(); name == "" || name == a.captureSuggestedName {
		a.captureSuggestedName = "Printer " + ip.String()
		a.captureName.SetText(a.captureSuggestedName)
	}
	a.suggestCaptureFile(ip.String())
}

// suggestCaptureFile fills in a destination the operator has not chosen. A path
// they typed, or one carried over from a previous printer, is left alone.
func (a *app) suggestCaptureFile(ip string) {
	if file := a.captureFile.Text(); file == "" || file == a.captureSuggestedFile || file == "profiles/printer.json" {
		a.captureSuggestedFile = suggestedProfileFile(a.profilesDirectory(), ip)
		a.captureFile.SetText(a.captureSuggestedFile)
	}
}

func (a *app) onDrivers() {
	if a.driversLoading || a.captureBusy {
		return
	}
	a.driversLoading = true
	a.refreshDrivers.SetEnabled(false)
	a.driverStatus.SetText("Loading the printer drivers installed on this PC...")
	start := time.Now()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		names, err := install.DriverNames(ctx, a.env)
		status := "success"
		if err != nil {
			status = "error"
		}
		a.log("gui", "drivers", nil, status, err, start)
		a.mw.Synchronize(func() {
			a.driversLoading = false
			a.refreshDrivers.SetEnabled(!a.captureBusy)
			if err != nil {
				a.driverStatus.SetText("Couldn't load drivers. Try Refresh drivers, or enter the exact driver name. " + err.Error())
				return
			}
			a.driversLoaded = true
			selected := a.captureDriver.Text()
			a.captureDriver.SetModel(names)
			a.captureDriver.SetCurrentIndex(-1)
			a.captureDriver.SetText(selected)
			// Typing an exact Windows driver name is the step operators get
			// wrong most often, so an unambiguous match against the model the
			// printer reported is filled in as a starting point. It is only a
			// suggestion: it never overwrites a choice already made, and
			// preflight still verifies the driver is really registered.
			suggestion := ""
			if strings.TrimSpace(selected) == "" {
				if suggestion = suggestDriver(names, a.setupEvidence); suggestion != "" {
					a.captureDriver.SetText(suggestion)
				}
			}
			switch {
			case len(names) == 0:
				a.driverStatus.SetText("No printer drivers are installed. Install the driver for your printer, then choose Refresh drivers.")
			case suggestion != "":
				a.driverStatus.SetText("Suggested a driver from the detected model. Change it if this is not the right one.")
			default:
				a.driverStatus.SetText(fmt.Sprintf("%d installed drivers available. Choose the driver that supports your printer model.", len(names)))
			}
		})
	}()
}

func (a *app) useSelectedProfile(mode string) {
	if path, ok := a.selectedProfile(); ok && a.selectProfileForReview(path, mode) {
		a.onPreview()
	}
}

func (a *app) selectProfileForReview(path, mode string) bool {
	if a.mutationBusy {
		return false
	}
	if a.editDirty() && sameProfilePath(path, a.editPath) {
		a.tabs.SetCurrentIndex(tabProfiles)
		a.profileOut.SetText("Save your changes or cancel editing before reviewing this printer's setup.")
		return false
	}
	if _, err := install.LoadProfile(path); err != nil {
		a.profileOut.SetText("This saved setup cannot be used: " + err.Error())
		a.tabs.SetCurrentIndex(tabProfiles)
		return false
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		a.profileOut.SetText("Couldn't open the saved setup: " + err.Error())
		return false
	}
	a.useProfileCheck.SetChecked(true)
	a.targetField.SetText(absolute)
	a.forceFamilyCombo.SetCurrentIndex(0)
	a.purgeDriverCheck.SetChecked(false)
	a.setReviewMode(mode)
	a.resetPending()
	a.planOut.SetText("Reviewing saved printer: " + absolute)
	a.tabs.SetCurrentIndex(tabReview)
	return true
}

func (a *app) onRefreshProfiles() {
	selected, _ := a.selectedProfile()
	dir := a.profilesDirectory()
	entries, err := os.ReadDir(dir)
	a.refreshingProfiles = true
	a.profileFiles = nil
	labels := []string{}
	if err == nil {
		for _, entry := range entries {
			if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".json") {
				continue
			}
			path, _ := filepath.Abs(filepath.Join(dir, entry.Name()))
			label := entry.Name() + "  ·  Needs attention"
			if p, loadErr := install.LoadProfile(path); loadErr == nil {
				label = p.PrinterName + "  ·  " + p.Target
			}
			a.profileFiles = append(a.profileFiles, path)
			labels = append(labels, label)
		}
	}
	a.profileList.SetModel(labels)
	a.refreshingProfiles = false
	if len(labels) > 0 {
		if !a.selectSavedPath(selected) {
			a.profileList.SetCurrentIndex(0)
		}
		a.onViewProfile()
	} else {
		a.updateProfileActions(false, false)
		switch {
		// The empty state names the screens that create a printer, rather than
		// describing the absence and leaving the next step to be guessed.
		case os.IsNotExist(err):
			a.profileOut.SetText("No saved printers yet.\r\n\r\nUse Find a printer to scan your network, or Add printer to enter an IP address yourself. This folder is created when you save your first printer.")
		case err != nil:
			a.profileOut.SetText("Couldn't read the saved-printer folder. Choose another folder or check its permissions.\r\n\r\n" + err.Error())
		default:
			a.profileOut.SetText("No saved printers in this folder.\r\n\r\nUse Find a printer to scan your network, or Add printer to enter an IP address yourself. You can also browse to another folder.")
		}
	}
	a.updateDiscoveryActions()
}

func (a *app) selectSavedPath(path string) bool {
	if path == "" {
		return false
	}
	for index, candidate := range a.profileFiles {
		if sameProfilePath(candidate, path) {
			a.profileList.SetCurrentIndex(index)
			return true
		}
	}
	return false
}

func (a *app) selectedProfile() (string, bool) {
	index := a.profileList.CurrentIndex()
	if index < 0 || index >= len(a.profileFiles) {
		return "", false
	}
	return a.profileFiles[index], true
}

func (a *app) updateProfileActions(selected, valid bool) {
	for _, button := range []*walk.PushButton{a.profileSetupBtn, a.profileConfigureBtn, a.profileRemoveBtn} {
		if button != nil {
			button.SetEnabled(valid && !a.mutationBusy)
		}
	}
	a.loadEditBtn.SetEnabled(valid && !a.mutationBusy)
	if a.profileDetailsBtn != nil {
		a.profileDetailsBtn.SetEnabled(selected)
	}
	if a.viewBtn != nil {
		a.viewBtn.SetEnabled(selected)
	}
}

func (a *app) onViewProfile() {
	path, ok := a.selectedProfile()
	if !ok {
		a.updateProfileActions(false, false)
		return
	}
	p, err := install.LoadProfile(path)
	a.updateProfileActions(true, err == nil)
	if err != nil {
		a.profileOut.SetText("This saved printer needs attention before it can be used.\r\n\r\n" + err.Error() + "\r\n\r\nFile: " + path + "\r\nInspect its file details, or add the printer again to save a new setup.")
		return
	}
	summary := profileSummary(p, path)
	if a.editPath != "" && a.profileEditor.Visible() && !sameProfilePath(a.editPath, path) {
		summary += "\r\n\r\nThe open editor still contains " + filepath.Base(a.editPath) + ". Save or cancel those changes before editing another printer."
	}
	a.profileOut.SetText(summary)
}

func (a *app) onProfileDetails() {
	path, ok := a.selectedProfile()
	if !ok {
		return
	}
	info, err := os.Stat(path)
	if err == nil && (!info.Mode().IsRegular() || info.Size() > 1024*1024) {
		err = fmt.Errorf("expected a regular printer file no larger than 1 MiB")
	}
	if err != nil {
		a.profileOut.SetText("Couldn't read file details: " + err.Error())
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		a.profileOut.SetText("Couldn't read file details: " + err.Error())
		return
	}
	a.showDetails("Saved printer file · "+filepath.Base(path), lines(string(data)))
}

func (a *app) onBrowseProfiles() {
	dialog := walk.FileDialog{Title: "Choose saved-printer folder", InitialDirPath: a.profilesDirectory()}
	if ok, err := dialog.ShowBrowseFolder(a.mw); err != nil {
		a.profileOut.SetText("Couldn't open folder browser: " + err.Error())
	} else if ok {
		a.profileDir.SetText(dialog.FilePath)
		a.onRefreshProfiles()
		a.suggestCaptureFields()
	}
}

func (a *app) onBrowseCapture() {
	if a.captureBusy {
		return
	}
	dialog := walk.FileDialog{Title: "Save printer setup", FilePath: a.captureFile.Text(), Filter: "Printer setup (*.json)|*.json", InitialDirPath: a.profilesDirectory()}
	if ok, err := dialog.ShowSave(a.mw); err != nil {
		a.captureStatus.SetText("Couldn't open file browser: " + err.Error())
	} else if ok {
		path := dialog.FilePath
		if filepath.Ext(path) == "" {
			path += ".json"
		}
		a.captureFile.SetText(path)
	}
}

func (a *app) setCaptureBusy(busy bool) {
	a.captureBusy = busy
	for _, widget := range []walk.Widget{a.captureTarget, a.captureFile, a.captureName, a.captureDriver, a.captureBtn} {
		widget.SetEnabled(!busy)
	}
	if a.captureBrowseBtn != nil {
		a.captureBrowseBtn.SetEnabled(!busy)
	}
	a.refreshDrivers.SetEnabled(!busy && !a.driversLoading)
	a.updateDiscoveryActions()
}

func (a *app) onCaptureProfile() {
	if a.captureBusy || a.mutationBusy {
		return
	}
	a.suggestCaptureFields()
	target := strings.TrimSpace(a.captureTarget.Text())
	file := strings.TrimSpace(a.captureFile.Text())
	name := strings.TrimSpace(a.captureName.Text())
	driver := strings.TrimSpace(a.captureDriver.Text())
	ip, err := netip.ParseAddr(target)
	if err != nil || ip.Zone() != "" {
		a.captureStatus.SetText("Enter the printer's IP address, such as 192.168.1.25. A subnet or hostname cannot be saved as a printer.")
		a.captureTarget.SetFocus()
		return
	}
	for _, field := range []struct {
		text, message string
		widget        walk.Widget
	}{
		{name, "Give the printer a name to display in Windows.", a.captureName},
		{driver, "Choose a compatible installed driver, or enter its exact Windows driver name.", a.captureDriver},
		{file, "Choose a file to save this printer setup.", a.captureFile},
	} {
		if field.text == "" {
			a.captureStatus.SetText(field.message)
			field.widget.SetFocus()
			return
		}
	}
	if !strings.EqualFold(filepath.Ext(file), ".json") {
		a.captureStatus.SetText("Choose a filename ending in .json so this setup appears in Saved printers.")
		a.captureFile.SetFocus()
		return
	}
	if _, err := os.Stat(file); err == nil {
		a.captureStatus.SetText("That file already exists. Choose a new filename, or edit the existing setup in Saved printers.")
		a.captureFile.SetFocus()
		return
	} else if !os.IsNotExist(err) {
		a.captureStatus.SetText("Couldn't access the save location. Choose another file or check folder permissions. " + err.Error())
		a.captureFile.SetFocus()
		return
	}
	a.setCaptureBusy(true)
	a.captureStatus.SetText("Checking the printer and saving its setup... The review will open when it is ready.")
	start := time.Now()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		result, finalErr := probe.Collect(ctx, ip.String())
		if finalErr == nil {
			p := install.Profile{Version: 1, Target: result.Evidence.IP, Evidence: result.Evidence, PrinterName: name, DriverName: driver}
			finalErr = install.SaveProfile(file, p)
		}
		status := "success"
		if finalErr != nil {
			status = "error"
		}
		a.log("gui", "profile capture", []string{target, file}, status, finalErr, start)
		a.mw.Synchronize(func() {
			a.setCaptureBusy(false)
			if finalErr != nil {
				a.captureStatus.SetText("Couldn't save this printer. Check that it is awake, verify the IP and driver, then try again. " + finalErr.Error())
				return
			}
			a.captureStatus.SetText("Saved " + name + ". Review the setup before applying it.")
			a.profileDir.SetText(filepath.Dir(file))
			a.onRefreshProfiles()
			a.selectSavedPath(file)
			if !a.mutationBusy && a.selectProfileForReview(file, "install") {
				a.onPreview()
			}
		})
	}()
}

func (a *app) currentEditValues() [5]string {
	return [5]string{a.editName.Text(), a.editDriver.Text(), a.editTarget.Text(), a.editPackage.Text(), a.editArchive.Text()}
}

func (a *app) editDirty() bool {
	return a.editPath != "" && a.currentEditValues() != a.editLoadedValues
}

func (a *app) confirmDiscardEdit() bool {
	return !a.editDirty() || walk.MsgBox(a.mw, "Unsaved printer settings", "Discard the unsaved changes to "+filepath.Base(a.editPath)+"?", walk.MsgBoxYesNo|walk.MsgBoxIconQuestion|walk.MsgBoxDefButton2) == walk.DlgCmdYes
}

func (a *app) onLoadEdit() {
	if a.mutationBusy {
		return
	}
	path, ok := a.selectedProfile()
	if !ok {
		return
	}
	if a.profileEditor.Visible() && sameProfilePath(path, a.editPath) {
		a.editName.SetFocus()
		return
	}
	if !a.confirmDiscardEdit() {
		return
	}
	profile, err := install.LoadProfile(path)
	if err != nil {
		a.profileOut.SetText("Couldn't open settings: " + err.Error())
		return
	}
	original, err := os.ReadFile(path)
	if err != nil {
		a.profileOut.SetText("Couldn't open settings: " + err.Error())
		return
	}
	a.editPath, a.editOriginal = path, original
	a.editPathLabel.SetText("Editing " + profile.PrinterName + " · " + path)
	a.editName.SetText(profile.PrinterName)
	a.editDriver.SetText(profile.DriverName)
	a.editTarget.SetText(profile.Target)
	a.editPackage.SetText("")
	a.editArchive.SetText("")
	if profile.DriverPackage != nil {
		a.editPackage.SetText(profile.DriverPackage.ID)
		a.editArchive.SetText(profile.DriverPackage.Archive)
	}
	a.editLoadedValues = a.currentEditValues()
	if a.editStatus != nil {
		a.editStatus.SetText("Save changes to update this file. A backup of the previous settings is kept automatically.")
	}
	a.profileEditor.SetVisible(true)
	a.editName.SetFocus()
}

func (a *app) onCancelEdit() {
	if !a.confirmDiscardEdit() {
		return
	}
	a.closeProfileEditor()
	a.onViewProfile()
}

func (a *app) closeProfileEditor() {
	a.profileEditor.SetVisible(false)
	a.editPath = ""
	a.editOriginal = nil
}

func (a *app) editError(message string) {
	if a.editStatus != nil {
		a.editStatus.SetText(message)
	} else {
		a.profileOut.SetText(message)
	}
}

func (a *app) onSaveEdit() {
	if a.mutationBusy || a.editPath == "" {
		return
	}
	path := a.editPath
	current, err := os.ReadFile(path)
	if err != nil {
		a.editError("Couldn't read this saved printer: " + err.Error())
		return
	}
	if !bytes.Equal(current, a.editOriginal) {
		a.editError("This file changed outside SpoolSmith. Cancel editing, then open its settings again to load the latest version.")
		return
	}
	profile, err := install.LoadProfile(path)
	if err != nil {
		a.editError("Couldn't load this saved printer: " + err.Error())
		return
	}
	profile.PrinterName = strings.TrimSpace(a.editName.Text())
	profile.DriverName = strings.TrimSpace(a.editDriver.Text())
	profile.Target = strings.TrimSpace(a.editTarget.Text())
	profile.DriverPackage = nil
	if a.editPackage.Text() != "" || a.editArchive.Text() != "" {
		profile.DriverPackage = &install.PackageSelection{ID: strings.TrimSpace(a.editPackage.Text()), Archive: strings.TrimSpace(a.editArchive.Text())}
	}
	start := time.Now()
	backup, err := install.EditProfile(path, profile)
	status := "success"
	if err != nil {
		status = "error"
	}
	a.log("gui", "profile edit", []string{path}, status, err, start)
	if err != nil {
		a.editError("Couldn't save these settings: " + err.Error())
		return
	}
	a.closeProfileEditor()
	a.resetPending()
	a.onRefreshProfiles()
	a.selectSavedPath(path)
	a.profileOut.SetText("Saved changes to " + profile.PrinterName + ".\r\nPrevious settings: " + backup + "\r\n\r\n" + profileSummary(profile, path) + "\r\n\r\nChanging a printer name creates a separate Windows printer; remove the old one separately if needed.")
}
