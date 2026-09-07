//go:build windows

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spilloid/spoolsmith/internal/actionlog"
	"github.com/spilloid/spoolsmith/internal/catalog"
	"github.com/spilloid/spoolsmith/internal/inspect"
	"github.com/spilloid/spoolsmith/internal/install"
	"github.com/spilloid/spoolsmith/internal/probe"
	"github.com/tailscale/walk"
)

// app owns every widget handle and the same Workflow/Environment the CLI
// uses, so the GUI is a second transport over identical, already-authorized
// (D-0039/D-0040/D-0041) core logic rather than a separate mutation path.
type app struct {
	printerUI
	reviewUI
	mw       *walk.MainWindow
	tabs     *walk.TabWidget
	workflow install.Workflow
	env      install.Environment
	logger   *actionlog.Logger

	discoverCIDR      *walk.LineEdit
	discoverBtn       *walk.PushButton
	discoverOut       *walk.TextEdit
	discoverList      *walk.ListBox
	discovered        []probe.Result
	discoverCancel    context.CancelFunc
	discoverCancelBtn *walk.PushButton

	inspectTarget *walk.LineEdit
	inspectBtn    *walk.PushButton
	inspectOut    *walk.TextEdit

	familiesBtn *walk.PushButton
	probeTarget *walk.LineEdit
	probeBtn    *walk.PushButton
	catalogOut  *walk.TextEdit

	profileDir     *walk.LineEdit
	profileList    *walk.ListBox
	profileFiles   []string
	profileOut     *walk.TextEdit
	refreshBtn     *walk.PushButton
	viewBtn        *walk.PushButton
	loadEditBtn    *walk.PushButton
	saveEditBtn    *walk.PushButton
	captureTarget  *walk.LineEdit
	captureFile    *walk.LineEdit
	captureName    *walk.LineEdit
	captureDriver  *walk.ComboBox
	refreshDrivers *walk.PushButton
	captureBtn     *walk.PushButton
	editName       *walk.LineEdit
	editDriver     *walk.LineEdit
	editTarget     *walk.LineEdit
	editPath       string
	editOriginal   []byte
	editPathLabel  *walk.Label
	editPackage    *walk.LineEdit
	editArchive    *walk.LineEdit

	modeInstall      *walk.RadioButton
	modeConfigure    *walk.RadioButton
	modeUninstall    *walk.RadioButton
	useProfileCheck  *walk.CheckBox
	targetField      *walk.LineEdit
	forceFamilyCombo *walk.ComboBox
	familyIDs        []string
	familyLabels     []string
	purgeDriverCheck *walk.CheckBox
	dryRunOnlyCheck  *walk.CheckBox
	previewBtn       *walk.PushButton
	executeBtn       *walk.PushButton
	planOut          *walk.TextEdit
	mutationBusy     bool
	// mutationExecuting is narrower than mutationBusy: only an actual install,
	// configure or removal is in flight. A preview is read-only and must not
	// hold the window open, so closing is blocked on this flag alone.
	mutationExecuting bool
	previewJSON       string

	pendingInstall   *install.InstallOptions
	pendingUninstall *install.UninstallOptions

	logOut      *walk.TextEdit
	refreshLog  *walk.PushButton
	openLogPath *walk.PushButton
}

func newApp() *app {
	return &app{
		workflow: install.NewWorkflow(),
		env:      install.NewEnvironment(),
		logger:   actionlog.Default(),
	}
}

func (a *app) log(source, op string, args []string, status string, err error, start time.Time) {
	entry := actionlog.Entry{
		Source:   source,
		Op:       op,
		Args:     args,
		Status:   status,
		Duration: time.Since(start).Round(time.Millisecond).String(),
	}
	if err != nil {
		entry.Error = err.Error()
	}
	_ = a.logger.Record(entry)
}

func prettyJSON(v any) string {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Sprintf("encode result: %v", err)
	}
	return lines(string(data))
}

// lines converts LF to CRLF. Native Windows edit controls only break a line on
// CRLF, so text produced by the shared workflow and the JSON encoders — both of
// which write LF — otherwise renders as one unreadable run-on paragraph.
func lines(text string) string {
	return strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\n", "\r\n")
}

func showErr(owner walk.Form, title string, err error) {
	walk.MsgBox(owner, title, err.Error(), walk.MsgBoxIconError)
}

// --- Inspect ------------------------------------------------------------

func (a *app) onInspect() {
	target := strings.TrimSpace(a.inspectTarget.Text())
	if target == "" {
		showErr(a.mw, "Inspect", fmt.Errorf("enter a target IP address or fixture file path"))
		return
	}
	a.inspectBtn.SetEnabled(false)
	a.inspectOut.SetText("Inspecting...")
	start := time.Now()
	go func() {
		result, err := inspect.Target(context.Background(), target)
		status := "success"
		var text string
		if err != nil {
			status = "error"
			text = "Error: " + err.Error()
		} else {
			text = prettyJSON(result)
		}
		a.log("gui", "inspect", []string{target}, status, err, start)
		a.mw.Synchronize(func() {
			a.inspectBtn.SetEnabled(true)
			a.inspectOut.SetText(text)
		})
	}()
}

// --- Catalog --------------------------------------------------------------

func (a *app) onFamilies() {
	start := time.Now()
	families := catalog.Families()
	a.log("gui", "catalog families", nil, "success", nil, start)
	a.catalogOut.SetText(prettyJSON(families))
}

func (a *app) onProbe() {
	target := strings.TrimSpace(a.probeTarget.Text())
	if target == "" {
		showErr(a.mw, "Catalog probe", fmt.Errorf("enter a target IP address"))
		return
	}
	a.probeBtn.SetEnabled(false)
	a.catalogOut.SetText("Probing...")
	start := time.Now()
	go func() {
		result, err := probe.Collect(context.Background(), target)
		status := "success"
		var text string
		if err != nil {
			status = "error"
			text = "Error: " + err.Error()
		} else {
			text = prettyJSON(result)
		}
		a.log("gui", "catalog probe", []string{target}, status, err, start)
		a.mw.Synchronize(func() {
			a.probeBtn.SetEnabled(true)
			a.catalogOut.SetText(text)
		})
	}()
}

// --- Install / uninstall ---------------------------------------------------
//
// Preview always forces DryRun=true: it calls the identical Workflow code the
// CLI's --dry-run does, so the plan/preflight text shown here is exactly what
// the CLI would print, and nothing can mutate during a preview. Execute is
// only reachable after a successful Preview and a native Yes/No dialog on the
// operator's own machine — together, one explicit confirmation of the full
// shown plan, matching D-0040's gate. Execute then re-runs the same call with
// Yes+NonInteractive set (the CLI's own --yes --non-interactive contract),
// re-verifying evidence immediately before the only mutating call.

func (a *app) resetPending() {
	a.pendingInstall = nil
	a.pendingUninstall = nil
	a.executeBtn.SetEnabled(false)
	a.previewJSON = ""
	if a.planDetailsBtn != nil {
		a.planDetailsBtn.SetEnabled(false)
	}
}

func (a *app) onPreview() {
	if a.mutationBusy {
		return
	}
	if strings.TrimSpace(a.targetField.Text()) == "" {
		a.resetPending()
		a.planOut.SetText("Choose a saved printer or enter " + strings.TrimSuffix(strings.ToLower(a.targetLabel.Text()), ":") + " before previewing.")
		a.targetField.SetFocus()
		return
	}
	a.setMutationBusy(true)
	a.previewBtn.SetEnabled(false)
	a.resetPending()
	a.planOut.SetText("Checking the printer and preparing your preview. This may take a few seconds...")
	a.reviewHint.SetText("Preparing your preview. No changes are being made.")
	isInstall := !a.modeUninstall.Checked()
	configure := a.modeConfigure.Checked()
	useProfile := a.useProfileCheck.Checked()
	dryRunOnly := a.dryRunOnlyCheck.Checked()
	targetOrProfile := strings.TrimSpace(a.targetField.Text())
	forceFamily := ""
	if idx := a.forceFamilyCombo.CurrentIndex(); idx > 0 && idx < len(a.familyIDs) {
		forceFamily = a.familyIDs[idx]
	}
	if useProfile {
		forceFamily = ""
	}
	purgeDriver := !isInstall && a.purgeDriverCheck.Checked()

	start := time.Now()
	go func() {
		var buf bytes.Buffer
		var op string
		var args []string
		var status string
		var errText string

		if isInstall {
			op = "install"
			options := install.InstallOptions{DryRun: true, ForceFamily: forceFamily, UpdateExisting: configure, Compact: true}
			if configure && !useProfile {
				a.finishPreview("configure", nil, start, fmt.Errorf("configure requires a profile file"), "")
				return
			}
			if configure {
				op = "configure"
			}
			if useProfile {
				profile, err := install.LoadProfile(targetOrProfile)
				if err != nil {
					a.finishPreview(op, args, start, err, buf.String())
					return
				}
				options.Profile = &profile
				if err := profile.ResolvePackagePath(targetOrProfile); err != nil {
					a.finishPreview(op, args, start, err, "")
					return
				}
				args = []string{"--profile", targetOrProfile}
			} else {
				options.Target = targetOrProfile
				args = []string{targetOrProfile}
			}
			outcome, _ := a.workflow.RunInstall(context.Background(), a.env, strings.NewReader(""), &buf, false, options)
			a.mw.Synchronize(func() { a.previewJSON = prettyJSON(outcome) })
			for _, reason := range outcome.Uncertain {
				fmt.Fprintln(&buf, "Evidence: "+reason)
			}
			status = outcome.Status
			errText = outcome.Error
			if status == "dry-run" && !dryRunOnly {
				options.ExpectedPlan = outcome.Plan
				a.mw.Synchronize(func() { a.pendingInstall = &options })
			}
		} else {
			op = "uninstall"
			options := install.UninstallOptions{DryRun: true, PurgeDriver: purgeDriver, Compact: true}
			if useProfile {
				profile, err := install.LoadProfile(targetOrProfile)
				if err != nil {
					a.finishPreview(op, args, start, err, buf.String())
					return
				}
				options.Profile = &profile
				options.PrinterName = profile.PrinterName
				args = []string{"--profile", targetOrProfile}
			} else {
				options.PrinterName = targetOrProfile
				args = []string{targetOrProfile}
			}
			outcome, _ := a.workflow.RunUninstall(context.Background(), a.env, strings.NewReader(""), &buf, false, options)
			a.mw.Synchronize(func() { a.previewJSON = prettyJSON(outcome) })
			status = outcome.Status
			errText = outcome.Error
			if status == "dry-run" && !dryRunOnly {
				options.ExpectedPlan = outcome.Plan
				a.mw.Synchronize(func() { a.pendingUninstall = &options })
			}
		}

		var err error
		if errText != "" {
			err = fmt.Errorf("%s", errText)
		}
		a.finishPreview(op, args, start, err, buf.String())
	}()
}

func (a *app) finishPreview(op string, args []string, start time.Time, err error, transcript string) {
	status := "success"
	if err != nil {
		status = "error"
	}
	a.log("gui", op+" preview", args, status, err, start)
	a.mw.Synchronize(func() {
		a.setMutationBusy(false)
		text := lines(transcript)
		if err != nil {
			text = "Unable to continue\r\n" + friendlyOperationError(err.Error()) + "\r\n\r\n" + transcript
			a.reviewHint.SetText("Resolve the issue below, then preview again.")
		} else if a.dryRunOnlyCheck.Checked() {
			a.reviewHint.SetText("Preview only is on. Turn it off and preview again to enable installation.")
		} else if a.pendingInstall != nil || a.pendingUninstall != nil {
			a.reviewHint.SetText("Review the plan below, then use the button at the bottom right to confirm.")
		} else {
			a.reviewHint.SetText("Preview complete. No changes are needed.")
		}
		a.planOut.SetText(text)
		a.planDetailsBtn.SetEnabled(a.previewJSON != "")
		if a.pendingInstall != nil || a.pendingUninstall != nil {
			a.executeBtn.SetEnabled(true)
		}
	})
}

func (a *app) onExecute() {
	if a.mutationBusy || a.dryRunOnlyCheck.Checked() {
		return
	}
	if a.pendingInstall == nil && a.pendingUninstall == nil {
		return
	}
	verb := "add printer"
	if a.pendingUninstall != nil {
		verb = "remove printer"
	}
	if a.pendingInstall != nil && a.pendingInstall.UpdateExisting {
		verb = "update settings"
	}
	if walk.MsgBox(a.mw, "Confirm "+verb, "Review the plan below. Proceed with these changes?\n\n"+a.planOut.Text(), walk.MsgBoxYesNo|walk.MsgBoxDefButton2|walk.MsgBoxIconWarning) != 6 {
		return
	}
	a.setMutationBusy(true)
	a.mutationExecuting = true
	pendingInstall, pendingUninstall := a.pendingInstall, a.pendingUninstall
	a.executeBtn.SetEnabled(false)
	a.previewBtn.SetEnabled(false)
	a.reviewHint.SetText("Applying your confirmed changes. Please keep SpoolSmith open.")
	start := time.Now()
	go func() {
		var buf bytes.Buffer
		var op string
		var status, errText string

		if pendingInstall != nil {
			op = "install"
			options := *pendingInstall
			if options.UpdateExisting {
				op = "configure"
			}
			options.DryRun = false
			options.Yes = true
			options.NonInteractive = true
			outcome, _ := a.workflow.RunInstall(context.Background(), a.env, strings.NewReader(""), &buf, false, options)
			status, errText = outcome.Status, outcome.Error
		} else {
			op = "uninstall"
			options := *pendingUninstall
			options.DryRun = false
			options.Yes = true
			options.NonInteractive = true
			outcome, _ := a.workflow.RunUninstall(context.Background(), a.env, strings.NewReader(""), &buf, false, options)
			status, errText = outcome.Status, outcome.Error
		}

		var err error
		if errText != "" {
			err = fmt.Errorf("%s", errText)
		}
		logStatus := "success"
		if err != nil {
			logStatus = "error"
		}
		var logArgs []string
		if pendingInstall != nil && pendingInstall.ExpectedPlan != nil {
			logArgs = []string{pendingInstall.ExpectedPlan.PrinterName, pendingInstall.ExpectedPlan.IPAddress}
		}
		if pendingUninstall != nil {
			logArgs = []string{pendingUninstall.PrinterName}
		}
		a.log("gui", op+" execute", logArgs, logStatus, err, start)
		a.mw.Synchronize(func() {
			a.mutationExecuting = false
			a.resetPending()
			a.setMutationBusy(false)
			text := lines(buf.String())
			if errText != "" {
				text = "The operation could not finish.\r\n" + friendlyOperationError(errText) + "\r\n\r\n" + text
				a.reviewHint.SetText("Check the result below before trying again.")
			} else if status == "success" || status == "already-absent" {
				a.reviewHint.SetText("Done. Your saved settings are available in Saved printers.")
			}
			a.planOut.SetText(text)
		})
	}()
}

func (a *app) setMutationBusy(busy bool) {
	a.mutationBusy = busy
	for _, control := range []walk.Widget{a.modeInstall, a.modeConfigure, a.modeUninstall, a.useProfileCheck, a.targetField, a.forceFamilyCombo, a.purgeDriverCheck, a.dryRunOnlyCheck, a.previewBtn, a.reviewBrowseBtn} {
		control.SetEnabled(!busy)
	}
	a.updateReviewControls()
}

func (a *app) bindMutationInputs() {
	invalidate := func() { a.invalidateReview() }
	a.targetField.TextChanged().Attach(invalidate)
	a.forceFamilyCombo.CurrentIndexChanged().Attach(invalidate)
	for _, checkbox := range []*walk.CheckBox{a.useProfileCheck, a.purgeDriverCheck, a.dryRunOnlyCheck} {
		checkbox.CheckedChanged().Attach(invalidate)
	}
	for _, radio := range []*walk.RadioButton{a.modeInstall, a.modeConfigure, a.modeUninstall} {
		radio.CheckedChanged().Attach(func() {
			if a.modeConfigure.Checked() {
				a.useProfileCheck.SetChecked(true)
			}
			invalidate()
		})
	}
	a.mw.Closing().Attach(func(canceled *bool, _ walk.CloseReason) {
		if a.mutationExecuting {
			*canceled = true
			walk.MsgBox(a.mw, "Operation in progress", "Wait for the current printer operation to finish before closing.", walk.MsgBoxIconInformation)
		}
	})
}

// --- Log viewer -------------------------------------------------------

func (a *app) onRefreshLog() {
	data, err := os.ReadFile(actionlog.Path())
	if err != nil {
		a.logOut.SetText("(no log entries yet: " + err.Error() + ")")
		return
	}
	entries := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(entries) > 500 {
		entries = entries[len(entries)-500:]
	}
	a.logOut.SetText(strings.Join(entries, "\r\n"))
}

func (a *app) onOpenLogPath() {
	walk.MsgBox(a.mw, "Action log location", actionlog.Path(), walk.MsgBoxIconInformation)
}
