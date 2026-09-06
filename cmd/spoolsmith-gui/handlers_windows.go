//go:build windows

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/tailscale/walk"
	"github.com/spilloid/spoolsmith/internal/actionlog"
	"github.com/spilloid/spoolsmith/internal/catalog"
	"github.com/spilloid/spoolsmith/internal/inspect"
	"github.com/spilloid/spoolsmith/internal/install"
	"github.com/spilloid/spoolsmith/internal/probe"
)

// app owns every widget handle and the same Workflow/Environment the CLI
// uses, so the GUI is a second transport over identical, already-authorized
// (D-0039/D-0040/D-0041) core logic rather than a separate mutation path.
type app struct {
	mw       *walk.MainWindow
	workflow install.Workflow
	env      install.Environment
	logger   *actionlog.Logger

	discoverCIDR *walk.LineEdit
	discoverBtn  *walk.PushButton
	discoverOut  *walk.TextEdit

	inspectTarget *walk.LineEdit
	inspectBtn    *walk.PushButton
	inspectOut    *walk.TextEdit

	familiesBtn *walk.PushButton
	probeTarget *walk.LineEdit
	probeBtn    *walk.PushButton
	catalogOut  *walk.TextEdit

	profileDir    *walk.LineEdit
	profileList   *walk.ListBox
	profileFiles  []string
	profileOut    *walk.TextEdit
	refreshBtn    *walk.PushButton
	viewBtn       *walk.PushButton
	loadEditBtn   *walk.PushButton
	saveEditBtn   *walk.PushButton
	captureTarget *walk.LineEdit
	captureFile   *walk.LineEdit
	captureName   *walk.LineEdit
	captureDriver *walk.LineEdit
	captureBtn    *walk.PushButton
	editName      *walk.LineEdit
	editDriver    *walk.LineEdit
	editTarget    *walk.LineEdit

	modeInstall      *walk.RadioButton
	modeUninstall    *walk.RadioButton
	useProfileCheck  *walk.CheckBox
	targetField      *walk.LineEdit
	profileField     *walk.LineEdit
	forceFamilyCombo *walk.ComboBox
	familyIDs        []string
	familyLabels     []string
	purgeDriverCheck *walk.CheckBox
	dryRunOnlyCheck  *walk.CheckBox
	previewBtn       *walk.PushButton
	executeBtn       *walk.PushButton
	planOut          *walk.TextEdit

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
	return string(data)
}

func showErr(owner walk.Form, title string, err error) {
	walk.MsgBox(owner, title, err.Error(), walk.MsgBoxIconError)
}

// --- Discover ---------------------------------------------------------

func (a *app) onDiscover() {
	cidr := strings.TrimSpace(a.discoverCIDR.Text())
	if cidr == "" {
		showErr(a.mw, "Discover", fmt.Errorf("enter an IPv4 CIDR, such as 192.168.1.0/24"))
		return
	}
	a.discoverBtn.SetEnabled(false)
	a.discoverOut.SetText("Scanning for printer candidates; open ports do not establish driver compatibility...")
	start := time.Now()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
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
			a.discoverOut.SetText(prettyJSON(response))
		})
	}()
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

// --- Profiles ---------------------------------------------------------

func (a *app) profilePath(name string) string {
	return filepath.Join(strings.TrimSpace(a.profileDir.Text()), name)
}

func (a *app) onRefreshProfiles() {
	dir := strings.TrimSpace(a.profileDir.Text())
	if dir == "" {
		dir = "profiles"
	}
	var files []string
	entries, err := os.ReadDir(dir)
	if err == nil {
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
				continue
			}
			files = append(files, entry.Name())
		}
	}
	sort.Strings(files)
	a.profileFiles = files
	_ = a.profileList.SetModel(files)
}

func (a *app) selectedProfile() (string, bool) {
	index := a.profileList.CurrentIndex()
	if index < 0 || index >= len(a.profileFiles) {
		return "", false
	}
	return a.profilePath(a.profileFiles[index]), true
}

func (a *app) onViewProfile() {
	path, ok := a.selectedProfile()
	if !ok {
		showErr(a.mw, "Profile", fmt.Errorf("select a profile in the list first"))
		return
	}
	profile, err := install.LoadProfile(path)
	if err != nil {
		a.profileOut.SetText("Error: " + err.Error())
		return
	}
	a.profileOut.SetText(prettyJSON(profile))
}

func (a *app) onCaptureProfile() {
	target := strings.TrimSpace(a.captureTarget.Text())
	file := strings.TrimSpace(a.captureFile.Text())
	name := strings.TrimSpace(a.captureName.Text())
	driver := strings.TrimSpace(a.captureDriver.Text())
	if target == "" || file == "" || name == "" || driver == "" {
		showErr(a.mw, "Capture profile", fmt.Errorf("target, file, queue name, and driver name are all required"))
		return
	}
	a.captureBtn.SetEnabled(false)
	start := time.Now()
	go func() {
		result, err := probe.Collect(context.Background(), target)
		var saveErr error
		if err == nil {
			p := install.Profile{Version: 1, Target: result.Evidence.IP, Evidence: result.Evidence, PrinterName: name, DriverName: driver}
			saveErr = install.SaveProfile(file, p)
		}
		finalErr := err
		if finalErr == nil {
			finalErr = saveErr
		}
		status := "success"
		if finalErr != nil {
			status = "error"
		}
		a.log("gui", "profile capture", []string{target, file}, status, finalErr, start)
		a.mw.Synchronize(func() {
			a.captureBtn.SetEnabled(true)
			if finalErr != nil {
				showErr(a.mw, "Capture profile", finalErr)
				return
			}
			walk.MsgBox(a.mw, "Capture profile", "Saved printer profile. The driver name is operator-selected; installation checks that it is registered locally.", walk.MsgBoxIconInformation)
			a.onRefreshProfiles()
		})
	}()
}

func (a *app) onLoadEdit() {
	path, ok := a.selectedProfile()
	if !ok {
		showErr(a.mw, "Profile edit", fmt.Errorf("select a profile in the list first"))
		return
	}
	profile, err := install.LoadProfile(path)
	if err != nil {
		showErr(a.mw, "Profile edit", err)
		return
	}
	_ = a.editName.SetText(profile.PrinterName)
	_ = a.editDriver.SetText(profile.DriverName)
	_ = a.editTarget.SetText(profile.Target)
}

func (a *app) onSaveEdit() {
	path, ok := a.selectedProfile()
	if !ok {
		showErr(a.mw, "Profile edit", fmt.Errorf("select a profile in the list first"))
		return
	}
	profile, err := install.LoadProfile(path)
	if err != nil {
		showErr(a.mw, "Profile edit", err)
		return
	}
	if name := strings.TrimSpace(a.editName.Text()); name != "" {
		profile.PrinterName = name
	}
	if driver := strings.TrimSpace(a.editDriver.Text()); driver != "" {
		profile.DriverName = driver
	}
	if target := strings.TrimSpace(a.editTarget.Text()); target != "" {
		profile.Target = target
	}
	start := time.Now()
	backup, err := install.EditProfile(path, profile)
	status := "success"
	if err != nil {
		status = "error"
	}
	a.log("gui", "profile edit", []string{path}, status, err, start)
	if err != nil {
		showErr(a.mw, "Profile edit", err)
		return
	}
	walk.MsgBox(a.mw, "Profile edit", fmt.Sprintf("Profile updated; previous version saved to %s.\nChanging the queue name creates a separate queue; remove the old queue by name if needed.", backup), walk.MsgBoxIconInformation)
	a.onRefreshProfiles()
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
}

func (a *app) onPreview() {
	a.previewBtn.SetEnabled(false)
	a.resetPending()
	a.planOut.SetText("Building plan...")
	isInstall := a.modeInstall.Checked()
	useProfile := a.useProfileCheck.Checked()
	dryRunOnly := a.dryRunOnlyCheck.Checked()
	targetOrProfile := strings.TrimSpace(a.targetField.Text())
	forceFamily := ""
	if idx := a.forceFamilyCombo.CurrentIndex(); idx > 0 && idx < len(a.familyIDs) {
		forceFamily = a.familyIDs[idx]
	}
	purgeDriver := a.purgeDriverCheck.Checked()

	start := time.Now()
	go func() {
		var buf bytes.Buffer
		var op string
		var args []string
		var status string
		var errText string

		if isInstall {
			op = "install"
			options := install.InstallOptions{DryRun: true, ForceFamily: forceFamily}
			if useProfile {
				profile, err := install.LoadProfile(targetOrProfile)
				if err != nil {
					a.finishPreview(op, args, start, err, buf.String())
					return
				}
				options.Profile = &profile
				args = []string{"--profile", targetOrProfile}
			} else {
				options.Target = targetOrProfile
				args = []string{targetOrProfile}
			}
			outcome, _ := a.workflow.RunInstall(context.Background(), a.env, strings.NewReader(""), &buf, false, options)
			status = outcome.Status
			errText = outcome.Error
			if status == "dry-run" && !dryRunOnly {
				a.mw.Synchronize(func() { a.pendingInstall = &options })
			}
		} else {
			op = "uninstall"
			options := install.UninstallOptions{DryRun: true, PurgeDriver: purgeDriver}
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
			status = outcome.Status
			errText = outcome.Error
			if status == "dry-run" && !dryRunOnly {
				a.mw.Synchronize(func() { a.pendingUninstall = &options })
			}
		}

		var err error
		if errText != "" {
			err = fmt.Errorf("%s", errText)
		}
		a.finishPreview(op, args, start, err, buf.String()+"\n[status: "+status+"]")
	}()
}

func (a *app) finishPreview(op string, args []string, start time.Time, err error, transcript string) {
	status := "success"
	if err != nil {
		status = "error"
	}
	a.log("gui", op+" preview", args, status, err, start)
	a.mw.Synchronize(func() {
		a.previewBtn.SetEnabled(true)
		text := transcript
		if err != nil && strings.TrimSpace(text) == "" {
			text = "Error: " + err.Error()
		}
		a.planOut.SetText(text)
		if a.pendingInstall != nil || a.pendingUninstall != nil {
			a.executeBtn.SetEnabled(true)
		}
	})
}

func (a *app) onExecute() {
	if a.pendingInstall == nil && a.pendingUninstall == nil {
		return
	}
	verb := "install"
	if a.pendingUninstall != nil {
		verb = "uninstall"
	}
	if walk.MsgBox(a.mw, "Confirm "+verb, "This will run the "+verb+" plan shown above and make real changes to this machine. Proceed?", walk.MsgBoxYesNo|walk.MsgBoxIconWarning) != 6 {
		return
	}
	a.executeBtn.SetEnabled(false)
	a.previewBtn.SetEnabled(false)
	start := time.Now()
	go func() {
		var buf bytes.Buffer
		var op string
		var status, errText string

		if a.pendingInstall != nil {
			op = "install"
			options := *a.pendingInstall
			options.DryRun = false
			options.Yes = true
			options.NonInteractive = true
			outcome, _ := a.workflow.RunInstall(context.Background(), a.env, strings.NewReader(""), &buf, false, options)
			status, errText = outcome.Status, outcome.Error
		} else {
			op = "uninstall"
			options := *a.pendingUninstall
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
		a.log("gui", op+" execute", nil, logStatus, err, start)
		a.mw.Synchronize(func() {
			a.resetPending()
			a.previewBtn.SetEnabled(true)
			a.planOut.SetText(buf.String() + "\n[status: " + status + "]")
		})
	}()
}

// --- Log viewer -------------------------------------------------------

func (a *app) onRefreshLog() {
	data, err := os.ReadFile(actionlog.Path())
	if err != nil {
		a.logOut.SetText("(no log entries yet: " + err.Error() + ")")
		return
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) > 500 {
		lines = lines[len(lines)-500:]
	}
	a.logOut.SetText(strings.Join(lines, "\n"))
}

func (a *app) onOpenLogPath() {
	walk.MsgBox(a.mw, "Action log location", actionlog.Path(), walk.MsgBoxIconInformation)
}
