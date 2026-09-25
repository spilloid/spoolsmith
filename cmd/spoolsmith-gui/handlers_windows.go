//go:build windows

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spilloid/spoolsmith/internal/actionlog"
	"github.com/spilloid/spoolsmith/internal/bundle"
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
	thisPCUI
	navUI
	mw       *walk.MainWindow
	workflow install.Workflow
	env      install.Environment
	logger   *actionlog.Logger

	discoverCIDR      *walk.LineEdit
	discoverBtn       *walk.PushButton
	discoverOut       *walk.Label
	discoverTable     *walk.TableView
	discoverModel     *discoveryTableModel
	otherNetwork      *walk.Composite
	otherNetworkBtn   *walk.PushButton
	otherNetworkShown bool
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

	captureTarget  *walk.LineEdit
	captureFile    *walk.LineEdit
	captureName    *walk.LineEdit
	captureDriver  *walk.ComboBox
	refreshDrivers *walk.PushButton
	captureBtn     *walk.PushButton

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
	pendingRepoint   *install.RepointOptions

	setupOpen   bool
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

// collector is the same live probe the CLI hands to the shared copy path.
func (a *app) collector() bundle.Collector {
	if a.workflow.Collect != nil {
		return bundle.Collector(a.workflow.Collect)
	}
	return bundle.Collector(probe.Collect)
}

// hostName records which machine a copied printer came from.
func hostName() string {
	name, err := os.Hostname()
	if err != nil {
		return ""
	}
	return name
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
		showErr(a.mw, "Inspect", fmt.Errorf("enter a printer IP address, a fixture file, a printer file (.ssb) or a printer set (.zip)"))
		return
	}
	a.inspectBtn.SetEnabled(false)
	a.inspectOut.SetText("Inspecting...")
	start := time.Now()
	go func() {
		if strings.EqualFold(filepath.Ext(target), bundle.SetExt) {
			entries, note, err := extractSet(target)
			var text string
			if err != nil {
				text = "Error: " + err.Error()
			} else {
				var b strings.Builder
				fmt.Fprintf(&b, "Printer set: %s\r\n%s\r\n", target, countPrinters(len(entries)))
				if note != "" {
					fmt.Fprintf(&b, "Note: %s\r\n", note)
				}
				for _, entry := range entries {
					fmt.Fprintf(&b, "\r\n%s\r\n  %s", filepath.Base(entry.path), entry.label)
				}
				b.WriteString("\r\n\r\nThis checks each printer file, not the printers. Open the set from Add a printer to review one.")
				text = b.String()
			}
			a.log("gui", "bundle inspect", []string{target}, statusOf(err), err, start)
			a.mw.Synchronize(func() { a.inspectBtn.SetEnabled(true); a.inspectOut.SetText(text) })
			return
		}
		if strings.EqualFold(filepath.Ext(target), ".ssb") {
			opened, err := bundle.Open(target)
			var text string
			if err == nil {
				err = opened.Verify()
				if err == nil {
					text = prettyJSON(opened.Manifest)
				}
				opened.Close()
			}
			if err != nil {
				text = "Error: " + err.Error()
			}
			a.log("gui", "bundle inspect", []string{target}, statusOf(err), err, start)
			a.mw.Synchronize(func() { a.inspectBtn.SetEnabled(true); a.inspectOut.SetText(text) })
			return
		}
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
	a.pendingRepoint = nil
	a.executeBtn.SetEnabled(false)
	a.previewJSON = ""
	if a.planDetailsBtn != nil {
		a.planDetailsBtn.SetEnabled(false)
	}
}

func (a *app) onPreview() {
	if a.mutationBusy || a.sheetDone {
		return
	}
	op := a.currentOperation()
	a.needsElevation = false
	a.sheetOutcome = nil
	if err := op.Validate(); err != nil {
		a.resetPending()
		a.setHint(err.Error())
		a.renderSheet()
		return
	}
	a.setMutationBusy(true)
	a.resetPending()
	if op.Offline || op.Kind == opRemove || op.Kind == opRepoint {
		a.setHint("Checking this PC and preparing the steps. Nothing changes yet...")
	} else {
		a.setHint("Checking the printer and preparing the steps. Nothing changes yet...")
	}
	a.planOut.SetText("")
	a.renderSheet()
	dryRunOnly := a.dryRunOnlyCheck.Checked()

	start := time.Now()
	go func() {
		var buf bytes.Buffer
		outcome, args, err := a.previewOperation(op, &buf)
		if err != nil {
			a.finishPreview(op, args, start, err, nil, buf.String())
			return
		}
		outcome.Operation = string(op.Kind)
		var outcomeErr error
		if outcome.Status == "dry-run" && !dryRunOnly {
			outcomeErr = a.stagePending(op, outcome)
		}
		if outcome.Error != "" {
			outcomeErr = fmt.Errorf("%s", outcome.Error)
		}
		a.finishPreview(op, args, start, outcomeErr, &outcome, buf.String())
	}()
}

func loadPrinterFile(path string) (install.Profile, *install.BundleDriver, error) {
	opened, err := bundle.Open(path)
	if err != nil {
		return install.Profile{}, nil, err
	}
	defer opened.Close()
	profile := opened.Manifest.Profile
	if err := profile.ResolvePackagePath(path); err != nil {
		return install.Profile{}, nil, err
	}
	driver, _, err := opened.PrepareDriver()
	if err != nil {
		return install.Profile{}, nil, err
	}
	return profile, driver, nil
}

// previewOperation runs exactly the operation described, always as a dry run.
//
// Preview calls the identical Workflow code the CLI's --dry-run does, so the
// plan shown here is what the CLI would print, and nothing can mutate during a
// preview.
func (a *app) previewOperation(op operation, buf *bytes.Buffer) (install.Outcome, []string, error) {
	ctx := context.Background()
	switch op.Kind {
	case opRemove:
		options := install.UninstallOptions{DryRun: true, PurgeDriver: op.PurgeDriver, Compact: true}
		args := []string{op.PrinterName}
		if op.ProfilePath != "" {
			profile, err := bundle.LoadProfile(op.ProfilePath)
			if err != nil {
				return install.Outcome{}, args, err
			}
			options.Profile = &profile
			options.PrinterName = profile.PrinterName
			args = []string{"--profile", op.ProfilePath}
		} else {
			options.PrinterName = op.PrinterName
		}
		outcome, _ := a.workflow.RunUninstall(ctx, a.env, strings.NewReader(""), buf, false, options)
		return outcome, args, nil

	case opRepoint:
		options := install.RepointOptions{PrinterName: op.PrinterName, NewAddress: op.NewAddress, DryRun: true, Compact: true}
		outcome, _ := a.workflow.RunRepoint(ctx, a.env, strings.NewReader(""), buf, false, options)
		return outcome, []string{op.PrinterName, op.NewAddress}, nil

	default:
		options := install.InstallOptions{
			DryRun:         true,
			ForceFamily:    op.ForceFamily,
			UpdateExisting: op.Kind == opConfigure || (op.Kind == opApply && op.UpdateExisting),
			Offline:        op.Offline,
			Compact:        true,
		}
		var args []string
		switch {
		case op.Kind == opApply || op.ProfilePath != "":
			// The same file shape either way now (a bundle, .ssb): apply and
			// an install/configure chosen from a saved printer file load and
			// stage identically, embedded driver payload included.
			path := op.BundlePath
			if op.ProfilePath != "" {
				path = op.ProfilePath
			}
			profile, driver, err := loadPrinterFile(path)
			if err != nil {
				return install.Outcome{}, []string{path}, err
			}
			options.Profile = &profile
			options.BundleDriver = driver
			args = []string{path}
			if op.Kind != opApply {
				args = []string{"--profile", path}
			}
		default:
			options.Target = op.Target
			args = []string{op.Target}
		}
		outcome, _ := a.workflow.RunInstall(ctx, a.env, strings.NewReader(""), buf, false, options)
		return outcome, args, nil
	}
}

// stagePending records the reviewed plan so Execute can re-run the identical
// operation, bound to the plan the operator actually saw.
func (a *app) stagePending(op operation, outcome install.Outcome) error {
	switch op.Kind {
	case opRemove:
		options := install.UninstallOptions{PurgeDriver: op.PurgeDriver, Compact: true, PrinterName: op.PrinterName, ExpectedPlan: outcome.Plan}
		if op.ProfilePath != "" {
			profile, err := bundle.LoadProfile(op.ProfilePath)
			if err != nil {
				return err
			}
			options.Profile = &profile
			options.PrinterName = profile.PrinterName
		}
		a.mw.Synchronize(func() { a.pendingUninstall = &options })
	case opRepoint:
		options := install.RepointOptions{PrinterName: op.PrinterName, NewAddress: op.NewAddress, Compact: true, ExpectedPlan: outcome.Plan}
		a.mw.Synchronize(func() { a.pendingRepoint = &options })
	default:
		options := install.InstallOptions{ForceFamily: op.ForceFamily, UpdateExisting: op.Kind == opConfigure || (op.Kind == opApply && op.UpdateExisting), Offline: op.Offline, Compact: true, ExpectedPlan: outcome.Plan}
		switch {
		case op.Kind == opApply || op.ProfilePath != "":
			path := op.BundlePath
			if op.ProfilePath != "" {
				path = op.ProfilePath
			}
			profile, driver, err := loadPrinterFile(path)
			if err != nil {
				return err
			}
			options.Profile = &profile
			options.BundleDriver = driver
		default:
			options.Target = op.Target
		}
		a.mw.Synchronize(func() { a.pendingInstall = &options })
	}
	return nil
}

func (a *app) finishPreview(op operation, args []string, start time.Time, err error, outcome *install.Outcome, transcript string) {
	a.log("gui", string(op.Kind)+" preview", args, statusOf(err), err, start)
	a.mw.Synchronize(func() {
		a.setMutationBusy(false)
		a.sheetOutcome = outcome
		if outcome != nil {
			a.previewJSON = prettyJSON(*outcome)
		}
		text := lines(transcript)
		switch {
		case outcome != nil && needsAdministrator(*outcome) && !isElevated():
			// The plan is complete; only this process's rights are missing.
			// Show it, and let the shield button hand it to an elevated copy
			// that checks it again and asks for the usual confirmation.
			a.needsElevation = true
			a.setHint("Windows will ask for administrator permission. SpoolSmith then checks these steps again and asks you to confirm them.")
		case err != nil:
			text = "Unable to continue\r\n" + friendlyOperationError(err.Error()) + "\r\n\r\n" + text
			a.setHint(friendlyOperationError(err.Error()))
		case a.dryRunOnlyCheck.Checked():
			a.setHint("Preview only is on. Turn it off under More options to apply these steps.")
		case a.hasPending():
			a.setHint("These are the steps. You confirm them once before anything changes.")
		default:
			a.setHint("Nothing needs to change.")
		}
		a.planOut.SetText(text)
		a.planDetailsBtn.SetEnabled(a.previewJSON != "")
		a.renderSheet()
		if a.executeBtn.Enabled() {
			a.executeBtn.SetFocus()
		}
	})
}

func (a *app) hasPending() bool {
	return a.pendingInstall != nil || a.pendingUninstall != nil || a.pendingRepoint != nil
}

func (a *app) onExecute() {
	if a.sheetDone {
		a.closeSheet()
		return
	}
	if a.mutationBusy || a.dryRunOnlyCheck.Checked() {
		return
	}
	op := a.currentOperation()
	if !isElevated() {
		if a.needsElevation || a.hasPending() {
			a.relaunchElevated(op)
		}
		return
	}
	if !a.hasPending() {
		return
	}
	steps := checklistText(planChecklist(*a.sheetOutcome), "\n")
	if walk.MsgBox(a.mw, "Confirm: "+op.Title(), op.Summary()+"\n\n"+steps+"\n\nThe full plan is under Details. Proceed with these changes?\n\n"+a.planOut.Text(), walk.MsgBoxYesNo|walk.MsgBoxDefButton2|walk.MsgBoxIconWarning) != 6 {
		return
	}
	a.setMutationBusy(true)
	a.mutationExecuting = true
	pendingInstall, pendingUninstall, pendingRepoint := a.pendingInstall, a.pendingUninstall, a.pendingRepoint
	a.executeBtn.SetEnabled(false)
	a.setHint("Applying your confirmed changes. Please keep SpoolSmith open.")
	start := time.Now()
	go func() {
		var buf bytes.Buffer
		var outcome install.Outcome
		ctx := context.Background()

		switch {
		case pendingRepoint != nil:
			options := *pendingRepoint
			options.Yes = true
			options.NonInteractive = true
			outcome, _ = a.workflow.RunRepoint(ctx, a.env, strings.NewReader(""), &buf, false, options)
		case pendingUninstall != nil:
			options := *pendingUninstall
			options.DryRun = false
			options.Yes = true
			options.NonInteractive = true
			outcome, _ = a.workflow.RunUninstall(ctx, a.env, strings.NewReader(""), &buf, false, options)
		default:
			options := *pendingInstall
			options.DryRun = false
			options.Yes = true
			options.NonInteractive = true
			outcome, _ = a.workflow.RunInstall(ctx, a.env, strings.NewReader(""), &buf, false, options)
		}
		outcome.Operation = string(op.Kind)
		status, errText := outcome.Status, outcome.Error

		var err error
		if errText != "" {
			err = fmt.Errorf("%s", errText)
		}
		a.log("gui", string(op.Kind)+" execute", []string{op.PrinterName}, statusOf(err), err, start)
		a.mw.Synchronize(func() {
			a.mutationExecuting = false
			a.resetPending()
			// Keep the shared result for ticket notes, including partial
			// failures, without keeping permission to run the plan again.
			a.sheetOutcome = &outcome
			a.previewJSON = prettyJSON(outcome)
			a.planDetailsBtn.SetEnabled(true)
			a.sheetDone = true
			a.setMutationBusy(false)
			text := lines(buf.String())
			if errText != "" {
				text = "The operation could not finish.\r\n" + friendlyOperationError(errText) + "\r\n\r\n" + text
				a.setHint("It didn't finish. The steps below show where it stopped and why.")
			} else if status == "success" || status == "already-absent" {
				a.setHint("Done.")
				// The inventory is stale the moment a change lands, and This
				// PC is where the operator goes next.
				a.onRefreshQueues()
			}
			a.planOut.SetText(text)
			a.copyNotesBtn.SetText("Copy notes for the ticket")
			setShown(a.copyNotesBtn, true)
			a.renderSheet()
		})
	}()
}

func (a *app) setMutationBusy(busy bool) {
	a.mutationBusy = busy
	for _, control := range []walk.Widget{a.forceFamilyCombo, a.purgeDriverCheck, a.dryRunOnlyCheck, a.offlineCheck, a.updateCheck, a.previewBtn} {
		if control != nil {
			control.SetEnabled(!busy)
		}
	}
	a.updateReviewControls()
	a.updateQueueActions()
}

func (a *app) bindMutationInputs() {
	invalidate := func() { a.invalidateReview() }
	a.forceFamilyCombo.CurrentIndexChanged().Attach(invalidate)
	for _, checkbox := range []*walk.CheckBox{a.purgeDriverCheck, a.dryRunOnlyCheck, a.offlineCheck, a.updateCheck} {
		checkbox.CheckedChanged().Attach(invalidate)
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
		if os.IsNotExist(err) {
			a.logOut.SetText("(no log entries yet)")
		} else {
			a.logOut.SetText("Couldn't read the log file at " + actionlog.Path() + ":\r\n" + err.Error())
		}
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
