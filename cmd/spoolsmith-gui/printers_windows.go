//go:build windows

package main

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spilloid/spoolsmith/internal/bundle"
	"github.com/spilloid/spoolsmith/internal/evidence"
	"github.com/spilloid/spoolsmith/internal/inspect"
	"github.com/spilloid/spoolsmith/internal/install"
	"github.com/spilloid/spoolsmith/internal/probe"
	"github.com/tailscale/walk"
)

type printerUI struct {
	searchGroup                                *walk.Composite
	discoverUseBtn, discoverDetailsBtn         *walk.PushButton
	captureStatus, driverStatus                *walk.Label
	captureBrowseBtn                           *walk.PushButton
	setupGroup                                 *walk.GroupBox
	intuneBtn                                  *walk.PushButton
	discoveryJSON                              string
	driversLoading, driversLoaded, captureBusy bool
	captureSuggestedName, captureSuggestedFile string
	// captureDriverTarget is the IP address the current captureDriver value
	// was chosen for. It is compared against captureTarget's current text
	// wherever the target can change -- selecting a different printer and
	// editing the IP field directly -- so a driver chosen for one printer
	// is never silently carried into another's setup.
	captureDriverTarget string
	profileDirPath      string
	// setupEvidence is what the printer being set up reported about itself.
	// It only ever seeds a driver suggestion the operator can overwrite.
	setupEvidence evidence.Evidence
}

func (a *app) initializePrinters() {
	if executable, err := os.Executable(); err == nil {
		a.profileDirPath = defaultProfileDirectory(executable)
	}
	if dir := strings.TrimSpace(os.Getenv("SPOOLSMITH_PROFILES_DIR")); dir != "" {
		a.profileDirPath = dir
	}
	a.captureTarget.TextChanged().Attach(a.suggestCaptureFields)
	a.discoverList.CurrentIndexChanged().Attach(a.updateDiscoveryActions)
	// Walk makes the newly current page visible before publishing this event,
	// which is the only point an optional panel's visibility can be applied:
	// while its page is hidden the control already reports itself invisible, so
	// walk's SetVisible sees no change and does nothing. A panel hidden during
	// startup would otherwise appear, empty, the first time its page is opened.
	a.tabs.CurrentIndexChanged().Attach(func() {
		switch a.tabs.CurrentIndex() {
		case tabAdd:
			a.setupGroup.SetVisible(a.setupOpen)
			a.searchGroup.SetVisible(!a.setupOpen)
			if a.setupOpen && !a.driversLoaded {
				a.onDrivers()
			}
		case tabReview:
			a.advancedPanel.SetVisible(a.advancedCheck.Checked())
			a.updateReviewControls()
		}
	})
	a.setupGroup.SetVisible(false)
	a.updateDiscoveryActions()
}

func (a *app) profilesDirectory() string {
	if dir := strings.TrimSpace(a.profileDirPath); dir != "" {
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
	a.discoverOut.SetText("Looking for printers on " + cidr + "...\r\nThis can take up to two minutes. You can cancel and type an IP address at any time.")
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
		if err != nil {
			response.Error = err.Error()
		}
		for _, candidate := range result.Candidates {
			response.Candidates = append(response.Candidates, inspect.Inspect(candidate.Evidence))
		}
		a.log("gui", "discover", []string{cidr}, statusOf(err), err, start)
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
				summary = fmt.Sprintf("No printers found on %s after checking %s.\r\nCheck that the printer is awake and on this network, try another subnet, or type its IP address above.", network, checked)
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
	if a.discoverUseBtn == nil {
		return
	}
	idx := a.discoverList.CurrentIndex()
	ready := a.discoverCancel == nil && idx >= 0 && idx < len(a.discovered) && !a.captureBusy && !a.mutationBusy
	a.discoverUseBtn.SetEnabled(ready)
	a.discoverUseBtn.SetText("Use this printer")
	if ready && len(savedProfilesForIP(a.profilesDirectory(), a.discovered[idx].Evidence.IP)) == 1 {
		a.discoverUseBtn.SetText("Use saved setup")
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

// reviewSavedPrinter reuses a setup already saved for this address rather than
// making the operator re-enter a driver they chose once before.
func (a *app) reviewSavedPrinter(target string) bool {
	matches := savedProfilesForIP(a.profilesDirectory(), target)
	switch len(matches) {
	case 0:
		return false
	case 1:
		return a.startProfileOperation(matches[0], opInstall)
	default:
		a.showSavedSetups(matches, fmt.Sprintf("There are %d saved setups for %s. Choose the one you want.", len(matches), target), nil)
		return true
	}
}

// startProfileOperation validates a saved setup before it can become an
// operation, so a corrupt file is reported where it was chosen.
func (a *app) startProfileOperation(path string, kind operationKind) bool {
	profile, err := bundle.LoadProfile(path)
	if err != nil {
		showErr(a.mw, "Saved setup", fmt.Errorf("this saved setup cannot be used: %w", err))
		return false
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		absolute = path
	}
	a.startOperation(operation{
		Kind:        kind,
		ProfilePath: absolute,
		PrinterName: profile.PrinterName,
		Target:      profile.Target,
	})
	return true
}

// openPrinterSetup reveals the settings for the printer just chosen, on the
// same page, rather than moving the operator to another tab mid-task.
func (a *app) openPrinterSetup(e evidence.Evidence) {
	if a.captureBusy || a.mutationBusy {
		return
	}
	// A driver chosen for one printer must never silently carry over to a
	// different one. Only keep it when this is the same target being
	// revisited (e.g. Refresh drivers); clear it when switching printers.
	// captureDriverTarget -- not just the current text field -- is the
	// source of truth, because the operator can also retype the IP field
	// directly (see suggestCaptureFields) without ever calling this function.
	if a.captureDriverTarget != strings.TrimSpace(e.IP) {
		a.captureDriver.SetText("")
		a.driversLoaded = false
	}
	a.captureDriverTarget = strings.TrimSpace(e.IP)
	a.setupEvidence = e
	a.setupOpen = true
	a.captureTarget.SetText(e.IP)
	if a.captureName.Text() == "" || a.captureName.Text() == a.captureSuggestedName {
		a.captureSuggestedName = printerIdentity(e)
		a.captureName.SetText(a.captureSuggestedName)
	}
	// Suggest the destination file explicitly rather than relying on the target
	// field's change handler, so arriving from discovery never lands with an
	// empty save path.
	a.suggestCaptureFile(e.IP)
	a.captureStatus.SetText("Printer: " + printerIdentity(e) + ". Choose its compatible Windows driver, then save and review.")
	a.searchGroup.SetVisible(false)
	a.setupGroup.SetVisible(true)
	a.tabs.SetCurrentIndex(tabAdd)
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
	// Typing a different target directly (not by selecting a printer, which
	// openPrinterSetup already guards) must equally stop a driver chosen for
	// the previous address from silently applying to this one.
	if a.captureDriverTarget != "" && a.captureDriverTarget != ip.String() {
		a.captureDriver.SetText("")
		a.driversLoaded = false
	}
	a.captureDriverTarget = ip.String()
	if name := a.captureName.Text(); name == "" || name == a.captureSuggestedName {
		a.captureSuggestedName = "Printer " + ip.String()
		a.captureName.SetText(a.captureSuggestedName)
	}
	a.suggestCaptureFile(ip.String())
}

// suggestCaptureFile fills in a destination the operator has not chosen. A path
// they typed, or one carried over from a previous printer, is left alone.
func (a *app) suggestCaptureFile(ip string) {
	if file := a.captureFile.Text(); file == "" || file == a.captureSuggestedFile || file == "profiles/printer.ssb" {
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
		a.log("gui", "drivers", nil, statusOf(err), err, start)
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

func (a *app) onBrowseCapture() {
	dialog := walk.FileDialog{Title: "Save printer setup", Filter: "Printer setups (*.ssb)|*.ssb|All files (*.*)|*.*", FilePath: a.captureFile.Text()}
	accepted, err := dialog.ShowSave(a.mw)
	if err != nil {
		showErr(a.mw, "Save printer setup", err)
		return
	}
	if accepted {
		a.captureFile.SetText(dialog.FilePath)
		a.captureSuggestedFile = dialog.FilePath
	}
}

// onOpenBundle sets up a printer from a file copied off another PC.
func (a *app) onOpenBundle() {
	if a.mutationBusy {
		return
	}
	dialog := walk.FileDialog{Title: "Open a copied printer (.ssb)", Filter: "Printer files (*.ssb)|*.ssb|All files (*.*)|*.*"}
	accepted, err := dialog.ShowOpen(a.mw)
	if err != nil {
		showErr(a.mw, "Open printer file", err)
		return
	}
	if !accepted {
		return
	}
	opened, err := bundle.Open(dialog.FilePath)
	if err != nil {
		showErr(a.mw, "Open printer file", err)
		return
	}
	defer opened.Close()
	if err := opened.Verify(); err != nil {
		showErr(a.mw, "Open printer file", err)
		return
	}
	// A bundle whose printer never answered during copy has no live identity
	// to ever check here either -- say so on the review screen up front
	// rather than let the operator discover it mid-run.
	a.startOperation(operation{
		Kind:        opApply,
		BundlePath:  dialog.FilePath,
		PrinterName: opened.Manifest.Profile.PrinterName,
		Target:      opened.Manifest.Profile.Target,
		Offline:     opened.Manifest.Profile.Evidence.Provenance != "captured",
	})
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
	if !strings.EqualFold(filepath.Ext(file), ".ssb") {
		a.captureStatus.SetText("Choose a filename ending in .ssb so this setup can be reused later.")
		a.captureFile.SetFocus()
		return
	}
	if _, err := os.Stat(file); err == nil {
		a.captureStatus.SetText("That file already exists. Choose a new filename, or open the existing setup instead.")
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
		savingFailed := false
		if finalErr == nil {
			p := install.Profile{Version: 1, Target: result.Evidence.IP, Evidence: result.Evidence, PrinterName: name, DriverName: driver}
			if finalErr = bundle.SaveProfile(file, p); finalErr != nil {
				savingFailed = true
			}
		}
		a.log("gui", "profile capture", []string{target, file}, statusOf(finalErr), finalErr, start)
		a.mw.Synchronize(func() {
			a.setCaptureBusy(false)
			if finalErr != nil {
				if savingFailed {
					a.captureStatus.SetText("The printer answered, but its setup could not be saved to " + file + ". Check the destination and folder permissions. " + finalErr.Error())
				} else {
					a.captureStatus.SetText("Couldn't save this printer. Check that it is awake, verify the IP and driver, then try again. " + finalErr.Error())
				}
				return
			}
			a.captureStatus.SetText("Saved " + name + ".")
			a.profileDirPath = filepath.Dir(file)
			a.startProfileOperation(file, opInstall)
		})
	}()
}
