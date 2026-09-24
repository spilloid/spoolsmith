package install

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"reflect"
	"strconv"
	"strings"

	"github.com/spilloid/spoolsmith/internal/catalog"
	"github.com/spilloid/spoolsmith/internal/evidence"
	"github.com/spilloid/spoolsmith/internal/probe"
)

// ExitCode is the stable process contract exposed by the CLI workflows.
type ExitCode int

const (
	ExitSuccess      ExitCode = 0
	ExitGeneralError ExitCode = 1
	ExitUsageError   ExitCode = 2
	ExitUnresolved   ExitCode = 3
	ExitPreflight    ExitCode = 4
	ExitNotConfirmed ExitCode = 5
)

// InstallOptions contains the policy-relevant inputs for one install attempt.
type InstallOptions struct {
	// ExpectedPlan binds execution to a previously reviewed preview. A changed
	// plan requires another preview; callers cannot authorize an unseen plan.
	ExpectedPlan   *Plan
	Target         string
	Profile        *Profile
	Offline        bool
	UpdateExisting bool
	Compact        bool
	ForceFamily    string
	Yes            bool
	JSON           bool
	NonInteractive bool
	DryRun         bool
	// BundleDriver, when set, stages a driver payload SpoolSmith has already
	// extracted and hash-verified from a bundle, if the driver is missing.
	BundleDriver *BundleDriver
	// ConfirmPlanHash names the exact plan the operator reviewed. It confirms
	// that one plan and nothing else: if the plan computed on this machine
	// differs at all, the operation stops instead of mutating.
	ConfirmPlanHash string
}

// UninstallOptions contains the policy-relevant inputs for one uninstall attempt.
type UninstallOptions struct {
	ExpectedPlan   *Plan
	Compact        bool
	Profile        *Profile
	PrinterName    string
	PurgeDriver    bool
	Yes            bool
	JSON           bool
	NonInteractive bool
	DryRun         bool
}

// Outcome is the machine-readable result of an install or uninstall workflow.
type Outcome struct {
	Operation  string   `json:"operation"`
	Status     string   `json:"status"`
	DryRun     bool     `json:"dry_run"`
	Confirmed  bool     `json:"confirmed"`
	Resolution string   `json:"resolution,omitempty"`
	Uncertain  []string `json:"uncertain,omitempty"`
	Plan       *Plan    `json:"plan,omitempty"`
	// PlanHash names the exact plan above. Reviewing one plan by hand and
	// then passing its hash to --plan-hash is how a reviewed setup is rolled
	// out across a fleet without accepting whatever each machine computes.
	PlanHash    string           `json:"plan_hash,omitempty"`
	Preflight   *PreflightResult `json:"preflight,omitempty"`
	Result      *Result          `json:"result,omitempty"`
	Error       string           `json:"error,omitempty"`
	LocalStatus *LocalStatus     `json:"local_status,omitempty"`
}

// Workflow owns detection, catalog selection, preflight, and confirmation so
// the command package remains a transport adapter. Function fields are public
// to let a future GUI and tests supply the same seams without global state.
type Workflow struct {
	Collect   func(context.Context, string) (probe.Result, error)
	Resolve   func(evidence.Evidence) catalog.ResolutionResult
	Families  func() []catalog.Family
	DriverFor func(string) (catalog.DriverPackage, bool)
}

// NewWorkflow returns the production workflow backed by the probe and catalog packages.
func NewWorkflow() Workflow {
	return Workflow{
		Collect:   probe.Collect,
		Resolve:   catalog.Resolve,
		Families:  catalog.Families,
		DriverFor: catalog.DriverFor,
	}
}

// RunInstall performs one complete install workflow. DryRun takes precedence
// over Yes and never reaches confirmation or a mutating Environment.Run call.
func (w Workflow) RunInstall(ctx context.Context, env Environment, input io.Reader, interactive io.Writer, inputIsTerminal bool, options InstallOptions) (Outcome, ExitCode) {
	outcome := Outcome{Operation: "install", Status: "error", DryRun: options.DryRun}
	if err := ctx.Err(); err != nil {
		return failOutcome(outcome, err, ExitGeneralError)
	}
	if err := w.validate(); err != nil {
		return failOutcome(outcome, err, ExitGeneralError)
	}
	if options.Offline && options.Profile == nil {
		return failOutcome(outcome, errors.New("install: --offline requires an administrator-prevalidated --profile"), ExitUsageError)
	}
	if options.Profile != nil {
		if options.ForceFamily != "" || options.Target != "" {
			return failOutcome(outcome, errors.New("install: --profile cannot be combined with a target or --force-family"), ExitUsageError)
		}
		if err := options.Profile.Validate(); err != nil {
			return failOutcome(outcome, err, ExitUsageError)
		}
		options.Target = options.Profile.Target
	}

	var forcedFamily *catalog.Family
	var forcedDriver *catalog.DriverPackage
	if options.ForceFamily != "" {
		family, driver, err := w.forcedSelection(options.ForceFamily)
		if err != nil {
			return failOutcome(outcome, err, ExitUsageError)
		}
		forcedFamily, forcedDriver = &family, &driver
	}

	var probeResult probe.Result
	var err error
	// autoOffline distinguishes a fallback this run chose for itself from an
	// --offline the operator typed. Both end up running the same offline path
	// below; only the notice and the recorded resolution tell them apart.
	autoOffline := false
	if options.Offline {
		probeResult.Evidence.IP = options.Target
	} else {
		probeResult, err = w.Collect(ctx, options.Target)
		if ctx.Err() != nil {
			return failOutcome(outcome, ctx.Err(), ExitGeneralError)
		}
		if errors.Is(err, context.Canceled) {
			return failOutcome(outcome, err, ExitGeneralError)
		}
		if err != nil && options.Profile != nil {
			// A Profile means an operator already reviewed and approved this
			// exact printer once -- that trust doesn't expire because the
			// printer won't answer right this second (still booting after a
			// move, a cable not yet seated, DHCP settling on a new subnet).
			// Falling back to the offline path this run already supports beats
			// sending the operator away empty-handed to go learn about
			// --offline and start over.
			fmt.Fprintf(interactive, "Could not contact the printer at %s (%v). Setting up offline from the saved profile instead; reachability and printing will need to be checked once it answers.\n", options.Target, err)
			options.Offline = true
			autoOffline = true
			err = nil
			probeResult = probe.Result{}
			probeResult.Evidence.IP = options.Target
		}
	}
	if err != nil {
		return failOutcome(outcome, fmt.Errorf("install: collect evidence: %w", err), ExitGeneralError)
	}

	reader := bufferedReader(input)
	resolution := catalog.ResolutionResult{}
	forced := forcedFamily != nil
	if options.Offline {
		resolution = options.Profile.selectedResolution()
		reason := "offline provisioning: live identity was not checked; reachability and printing are unverified"
		if autoOffline {
			reason = "offline fallback: the printer could not be contacted, so live identity was not checked; reachability and printing are unverified"
		}
		resolution.Uncertain = append(resolution.Uncertain, reason)
		forced = true
	} else if options.Profile != nil {
		if !options.Profile.hasCapturedIdentity() {
			// This profile was itself captured offline (see bundle.Create) and
			// carries nothing to ever compare against a live probe. There is no
			// "retry" that could change that outcome, so go straight to the
			// same offline fallback rather than pretending a check happened.
			fmt.Fprintln(interactive, "This profile has no confirmed printer identity (captured offline). Setting up offline; reachability and printing will need to be checked once it answers.")
			options.Offline = true
			autoOffline = true
			resolution = options.Profile.selectedResolution()
			resolution.Uncertain = append(resolution.Uncertain, "offline fallback: this profile has no captured identity to confirm; reachability and printing are unverified")
		} else {
			resolution, err = options.Profile.resolution(probeResult.Evidence)
			if errors.Is(err, errIdentityUnavailable) {
				fmt.Fprintln(interactive, "Identity probes were unavailable; retrying once for a sleeping printer.")
				retryResult, retryErr := w.Collect(ctx, options.Target)
				if ctx.Err() != nil {
					return failOutcome(outcome, ctx.Err(), ExitGeneralError)
				}
				if errors.Is(retryErr, context.Canceled) {
					return failOutcome(outcome, retryErr, ExitGeneralError)
				}
				if retryErr == nil {
					probeResult = retryResult
					resolution, err = options.Profile.resolution(probeResult.Evidence)
				}
			}
			if errors.Is(err, errIdentityUnavailable) {
				fmt.Fprintln(interactive, "Still no identity answer after retrying. Setting up offline from the saved profile instead; reachability and printing will need to be checked once it answers.")
				options.Offline = true
				autoOffline = true
				resolution = options.Profile.selectedResolution()
				resolution.Uncertain = append(resolution.Uncertain, "offline fallback: the printer never confirmed its identity, so live identity was not checked; reachability and printing are unverified")
			} else if err != nil {
				return failOutcome(outcome, err, ExitUnresolved)
			}
		}
		forced = true
	} else if forced {
		resolution = forcedResolution(*forcedFamily, *forcedDriver, probeResult.Evidence.IP)
	} else {
		resolution = w.Resolve(probeResult.Evidence)
		if resolution.Family == nil || resolution.Driver == nil {
			outcome.Uncertain = append([]string(nil), resolution.Uncertain...)
			mayPrompt := inputIsTerminal && !options.NonInteractive && !options.Yes && !options.JSON
			if !mayPrompt {
				return failOutcome(outcome, errors.New("install: printer family could not be resolved and no forced override was given"), ExitUnresolved)
			}
			family, selected, selectErr := SelectFamily(reader, interactive, resolution.Uncertain, w.Families())
			if selectErr != nil {
				return failOutcome(outcome, fmt.Errorf("install: select family: %w", selectErr), ExitGeneralError)
			}
			if !selected {
				outcome.Status = "not-confirmed"
				outcome.Error = "install: family selection aborted; no commands were run"
				return outcome, ExitNotConfirmed
			}
			driver, ok := w.DriverFor(family.ID)
			if !ok {
				return failOutcome(outcome, fmt.Errorf("install: family %q has no driver package", family.ID), ExitGeneralError)
			}
			resolution = forcedResolution(family, driver, probeResult.Evidence.IP)
			forced = true
		}
	}

	if forced {
		outcome.Resolution = "forced-override"
	} else {
		outcome.Resolution = "automatic"
	}
	if options.Profile != nil {
		outcome.Resolution = "operator-profile"
		outcome.Uncertain = append([]string(nil), resolution.Uncertain...)
		switch {
		case autoOffline:
			outcome.Resolution = "offline-fallback-operator-profile"
		case options.Offline:
			outcome.Resolution = "offline-operator-profile"
		default:
			fmt.Fprintln(interactive, "Profile: driver compatibility was selected by the operator; live model evidence matches the capture.")
		}
	}
	for _, reason := range resolution.Uncertain {
		fmt.Fprintf(interactive, "  Note: %s\n", reason)
	}
	plan, err := BuildPlan(probeResult.Evidence.IP, resolution)
	if err != nil {
		return failOutcome(outcome, err, ExitGeneralError)
	}
	plan.ForcedOverride = forced
	plan.Offline = options.Offline
	plan.UpdateExisting = options.UpdateExisting
	plan.Commands = installCommands(plan)
	if options.Profile != nil && options.Profile.DriverPackage != nil {
		selection := *options.Profile.DriverPackage
		plan.DriverPackage = &selection
		record, recordErr := selection.record(plan.DriverName)
		if recordErr != nil {
			return failOutcome(outcome, recordErr, ExitUsageError)
		}
		plan.Driver.Source = record.SourceURL
		plan.Driver.SHA256 = record.SHA256
		plan.Driver.Version = record.Version
		plan.Driver.Strategy = "verified-local-archive-if-missing"
		command, packageErr := packageCommand(selection, plan.DriverName)
		if packageErr != nil {
			return failOutcome(outcome, packageErr, ExitUsageError)
		}
		plan.Commands = append([]string{command}, plan.Commands...)
	}
	if options.BundleDriver != nil {
		if plan.DriverPackage != nil {
			return failOutcome(outcome, errors.New("install: a bundle driver payload and a vendor package recipe cannot both stage the same driver"), ExitUsageError)
		}
		payload := *options.BundleDriver
		if err := payload.Validate(); err != nil {
			return failOutcome(outcome, err, ExitUsageError)
		}
		if payload.WindowsDriverName != plan.DriverName {
			return failOutcome(outcome, fmt.Errorf("install: bundle payload provides driver %q but the profile maps %q", payload.WindowsDriverName, plan.DriverName), ExitUsageError)
		}
		plan.BundleDriver = &payload
		plan.PublisherTrust = bundlePublisherTrust()
		plan.Driver.Strategy = "bundle-payload-if-missing"
		plan.Driver.Source = bundleDriverSource(payload)
		command, bundleErr := bundleDriverCommand(payload)
		if bundleErr != nil {
			return failOutcome(outcome, bundleErr, ExitUsageError)
		}
		plan.Commands = append([]string{command}, plan.Commands...)
	}
	outcome.Plan = &plan
	if fingerprint, hashErr := FingerprintPlan(plan); hashErr == nil {
		outcome.PlanHash = fingerprint
	}
	writeInstallPlan(interactive, plan, options.Compact)
	if options.ExpectedPlan != nil && !reflect.DeepEqual(*options.ExpectedPlan, plan) {
		return failOutcome(outcome, errors.New("install: plan changed since preview; review a new preview before proceeding"), ExitNotConfirmed)
	}

	preflight, err := Preflight(ctx, env, plan)
	outcome.Preflight = &preflight
	if err != nil {
		return failOutcome(outcome, err, ExitPreflight)
	}
	if options.DryRun {
		outcome.Status = "dry-run"
		fmt.Fprintln(interactive, "Preview complete. No changes made.")
		return outcome, ExitSuccess
	}

	confirmed := options.Yes
	if options.ConfirmPlanHash != "" {
		matched, hashErr := planHashMatches(plan, options.ConfirmPlanHash)
		if hashErr != nil {
			return failOutcome(outcome, hashErr, ExitGeneralError)
		}
		if !matched {
			actual, _ := FingerprintPlan(plan)
			outcome.Status = "not-confirmed"
			outcome.Error = fmt.Sprintf("install: this machine's plan is %s, not the reviewed plan %s; review the difference before applying it here", actual, options.ConfirmPlanHash)
			return outcome, ExitNotConfirmed
		}
		fmt.Fprintf(interactive, "Plan matches the reviewed fingerprint %s.\n", options.ConfirmPlanHash)
		confirmed = true
	}
	if !confirmed {
		if options.NonInteractive || options.JSON || !inputIsTerminal {
			outcome.Status = "not-confirmed"
			outcome.Error = "install: confirmation required; no commands were run"
			return outcome, ExitNotConfirmed
		}
		confirmed, err = Confirm(reader, interactive)
		if err != nil {
			return failOutcome(outcome, fmt.Errorf("install: read confirmation: %w", err), ExitGeneralError)
		}
	}
	if !confirmed {
		outcome.Status = "not-confirmed"
		outcome.Error = "install: confirmation declined; no commands were run"
		return outcome, ExitNotConfirmed
	}
	outcome.Confirmed = true

	result, err := Install(ctx, env, plan, true)
	outcome.Result = &result
	if err != nil {
		writeCommandResults(interactive, result.Ran)
		if len(result.Ran) == 0 || errors.Is(err, ErrNotElevated) || errors.Is(err, ErrDriverNotPresent) {
			return failOutcome(outcome, err, ExitPreflight)
		}
		return failOutcome(outcome, err, ExitGeneralError)
	}
	if options.Offline {
		status, checkErr := CheckStatus(ctx, env, *options.Profile)
		outcome.LocalStatus = &status
		if checkErr != nil {
			return failOutcome(outcome, fmt.Errorf("install: local verification: %w", checkErr), ExitGeneralError)
		}
		if !status.Compliant {
			return failOutcome(outcome, fmt.Errorf("install: local verification failed: %s", strings.Join(status.Mismatches, "; ")), ExitGeneralError)
		}
		fmt.Fprintln(interactive, "Configured and verified locally. Printer reachability and printing have not been tested.")
	}
	outcome.Status = "success"
	fmt.Fprintf(interactive, "Printer configured: %s (%s). Reapplying the same profile is safe.\n", plan.PrinterName, plan.IPAddress)
	for _, ran := range result.Ran {
		if strings.TrimSpace(ran.Output) != "" {
			fmt.Fprintln(interactive, strings.TrimSpace(ran.Output))
		}
	}
	return outcome, ExitSuccess
}

// RunUninstall performs lookup, preflight, confirmation, and exact removal.
// DryRun takes precedence over Yes and never calls the mutating Environment.Run method.
func (w Workflow) RunUninstall(ctx context.Context, env Environment, input io.Reader, interactive io.Writer, inputIsTerminal bool, options UninstallOptions) (Outcome, ExitCode) {
	outcome := Outcome{Operation: "uninstall", Status: "error", DryRun: options.DryRun}
	reader := bufferedReader(input)
	if options.Profile != nil {
		if err := options.Profile.Validate(); err != nil {
			return failOutcome(outcome, err, ExitUsageError)
		}
		if options.PrinterName != options.Profile.PrinterName {
			return failOutcome(outcome, errors.New("remove: profile queue name does not match request"), ExitUsageError)
		}
	}

	configuration, err := LookupPrinter(ctx, env, options.PrinterName)
	if errors.Is(err, ErrPrinterNotFound) {
		outcome.Status = "already-absent"
		fmt.Fprintf(interactive, "Printer already absent: %s. No changes needed.\n", options.PrinterName)
		return outcome, ExitSuccess
	}
	if err != nil {
		return failOutcome(outcome, err, ExitGeneralError)
	}
	if options.Profile != nil && (!strings.EqualFold(configuration.PortName, managedPortPrefix+net.ParseIP(options.Profile.Target).String()) || !strings.EqualFold(configuration.DriverName, options.Profile.DriverName)) {
		return failOutcome(outcome, fmt.Errorf("remove: installed queue differs from profile (port %q, driver %q); review removal explicitly by queue name", configuration.PortName, configuration.DriverName), ExitUnresolved)
	}
	plan, err := BuildUninstallPlan(configuration.PrinterName, configuration.PortName, configuration.DriverName, options.PurgeDriver)
	if err != nil {
		return failOutcome(outcome, err, ExitGeneralError)
	}
	outcome.Plan = &plan
	writeUninstallPlan(interactive, plan, options.PurgeDriver, options.Compact)
	if options.ExpectedPlan != nil && !reflect.DeepEqual(*options.ExpectedPlan, plan) {
		return failOutcome(outcome, errors.New("remove: plan changed since preview; review a new preview before proceeding"), ExitNotConfirmed)
	}

	// Preflight runs after the plan is shown, matching RunInstall. Looking up the
	// queue and building the removal plan only read Windows inventory, so what a
	// removal would do stays reviewable without administrator rights — which is
	// how an operator inspects a removal before granting them. The gate itself is
	// unchanged: nothing has mutated yet, and Uninstall re-checks elevation at the
	// mutation boundary.
	preflight, err := PreflightUninstall(ctx, env)
	outcome.Preflight = &preflight
	if err != nil {
		return failOutcome(outcome, err, ExitPreflight)
	}

	if options.DryRun {
		outcome.Status = "dry-run"
		fmt.Fprintln(interactive, "Preview complete. No changes made.")
		return outcome, ExitSuccess
	}

	confirmed := options.Yes
	if !confirmed {
		if options.NonInteractive || options.JSON || !inputIsTerminal {
			outcome.Status = "not-confirmed"
			outcome.Error = "install: confirmation required; no commands were run"
			return outcome, ExitNotConfirmed
		}
		confirmed, err = Confirm(reader, interactive)
		if err != nil {
			return failOutcome(outcome, fmt.Errorf("install: read confirmation: %w", err), ExitGeneralError)
		}
	}
	if !confirmed {
		outcome.Status = "not-confirmed"
		outcome.Error = "install: confirmation declined; no commands were run"
		return outcome, ExitNotConfirmed
	}
	outcome.Confirmed = true

	result, err := Uninstall(ctx, env, configuration.PrinterName, configuration.PortName, configuration.DriverName, options.PurgeDriver)
	outcome.Result = &result
	if err != nil {
		writeCommandResults(interactive, result.Ran)
		if len(result.Ran) == 0 || errors.Is(err, ErrNotElevated) {
			return failOutcome(outcome, err, ExitPreflight)
		}
		return failOutcome(outcome, err, ExitGeneralError)
	}
	outcome.Status = "success"
	fmt.Fprintf(interactive, "Printer removed: %s.\n", configuration.PrinterName)
	for _, ran := range result.Ran {
		if strings.TrimSpace(ran.Output) != "" {
			fmt.Fprintln(interactive, strings.TrimSpace(ran.Output))
		}
	}
	return outcome, ExitSuccess
}

// SelectFamily prints an unresolved result and a single-choice catalog picker.
func SelectFamily(input io.Reader, interactive io.Writer, uncertain []string, families []catalog.Family) (catalog.Family, bool, error) {
	fmt.Fprintln(interactive, "Uncertain")
	for _, reason := range uncertain {
		fmt.Fprintf(interactive, "  - %s\n", reason)
	}
	for index, family := range families {
		fmt.Fprintf(interactive, "  %d. %s (%s); aliases: %s\n", index+1, family.ID, family.Manufacturer, strings.Join(family.Aliases, ", "))
	}
	fmt.Fprint(interactive, "Select a family by number, or 'a' to abort: ")
	answer, err := readAnswer(input)
	if err != nil {
		return catalog.Family{}, false, err
	}
	if strings.EqualFold(answer, "a") {
		return catalog.Family{}, false, nil
	}
	choice, err := strconv.Atoi(answer)
	if err != nil || choice < 1 || choice > len(families) {
		return catalog.Family{}, false, nil
	}
	return families[choice-1], true, nil
}

// Confirm requests the workflow's one explicit mutation confirmation.
func Confirm(input io.Reader, interactive io.Writer) (bool, error) {
	fmt.Fprint(interactive, "Proceed? [y/N]: ")
	answer, err := readAnswer(input)
	if err != nil {
		return false, err
	}
	return strings.EqualFold(answer, "y") || strings.EqualFold(answer, "yes"), nil
}

func (w Workflow) validate() error {
	if w.Collect == nil || w.Resolve == nil || w.Families == nil || w.DriverFor == nil {
		return errors.New("install: workflow dependencies are incomplete")
	}
	return nil
}

func (w Workflow) forcedSelection(id string) (catalog.Family, catalog.DriverPackage, error) {
	for _, family := range w.Families() {
		if family.ID != id {
			continue
		}
		driver, ok := w.DriverFor(id)
		if !ok {
			return catalog.Family{}, catalog.DriverPackage{}, fmt.Errorf("install: forced family %q has no driver package", id)
		}
		return family, driver, nil
	}
	return catalog.Family{}, catalog.DriverPackage{}, fmt.Errorf("install: unknown forced family %q", id)
}

func forcedResolution(family catalog.Family, driver catalog.DriverPackage, ip string) catalog.ResolutionResult {
	return catalog.ResolutionResult{
		NormalizedModel: fmt.Sprintf("SpoolSmith %s printer at %s", family.Manufacturer, ip),
		Family:          &family,
		Driver:          &driver,
		Confidence:      0,
		Uncertain:       []string{"printer family was manually selected"},
	}
}

func failOutcome(outcome Outcome, err error, code ExitCode) (Outcome, ExitCode) {
	outcome.Status = "error"
	outcome.Error = err.Error()
	return outcome, code
}

func bufferedReader(input io.Reader) *bufio.Reader {
	if reader, ok := input.(*bufio.Reader); ok {
		return reader
	}
	return bufio.NewReader(input)
}

func readAnswer(input io.Reader) (string, error) {
	reader := bufferedReader(input)
	answer, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	return strings.TrimSpace(answer), nil
}

func writeInstallPlan(writer io.Writer, plan Plan, compact bool) {
	fmt.Fprintln(writer, "Install plan")
	if plan.Offline {
		fmt.Fprintln(writer, "  OFFLINE: live identity is not checked. Verify local queue, driver and RAW TCP 9100 endpoint after applying; printing requires network connectivity.")
	}
	if plan.DriverPackage != nil {
		record, _ := plan.DriverPackage.record(plan.DriverName)
		fmt.Fprintf(writer, "  Driver package: %s\n  Local archive: %s\n  Expected SHA-256: %s\n  Vendor source: %s\n", plan.DriverPackage.ID, plan.DriverPackage.Archive, record.SHA256, record.SourceURL)
		fmt.Fprintln(writer, "  Reuse registered driver; if missing, verify signatures/hash, extract, stage INF and register. Staging files are retained in the Windows temp directory.")
	}
	if plan.BundleDriver != nil {
		fmt.Fprintf(writer, "  Bundle driver payload: %s\n  Files: %d (%d bytes), staged only if the driver is missing\n  Payload digest: %s\n", plan.BundleDriver.WindowsDriverName, plan.BundleDriver.FileCount, plan.BundleDriver.TotalBytes, plan.BundleDriver.PayloadDigest)
		fmt.Fprintln(writer, "  Payload bytes were verified against the bundle manifest. Windows enforces driver signing when pnputil stages the INF. This is not a vendor-package hash check.")
	}
	if plan.PublisherTrust != nil {
		fmt.Fprintf(writer, "  Driver publisher trust: if the driver is staged, the signer of each catalog its INF names is added to %s unless that exact certificate (by thumbprint) is already there.\n", plan.PublisherTrust.Store)
		fmt.Fprintln(writer, "  Only a signer from a catalog Windows validates is eligible, never certificate material from the bundle. Nothing is added to Root; an unsigned or untrusted-root catalog is left for Windows to accept or refuse.")
	}
	if plan.ForcedOverride {
		fmt.Fprintln(writer, "  Warning: manually selected mapping; not an automatic high-confidence driver match.")
	}
	fmt.Fprintf(writer, "  Driver source: %s\n", shownValue(plan.Driver.Source))
	if compact {
		fmt.Fprintf(writer, "  Queue:  %s\n  Target: %s (RAW TCP 9100)\n  Port:   %s\n  Driver: %s\n", plan.PrinterName, plan.IPAddress, plan.PortName, plan.DriverName)
		if plan.UpdateExisting {
			fmt.Fprintln(writer, "  Create or update this queue; reuse a matching port. Existing port conflicts stop the operation.")
		} else {
			fmt.Fprintln(writer, "  Create missing queue/port; matching settings are unchanged. Conflicts stop the operation.")
		}
		fmt.Fprintln(writer, "  Full commands and metadata: repeat with --dry-run --json.")
		return
	}
	fmt.Fprintf(writer, "  IP address: %s\n", plan.IPAddress)
	fmt.Fprintf(writer, "  Printer name: %s\n", plan.PrinterName)
	fmt.Fprintf(writer, "  Port name: %s\n", plan.PortName)
	fmt.Fprintf(writer, "  Update existing queue: %t (conflicting ports are never overwritten)\n", plan.UpdateExisting)
	fmt.Fprintf(writer, "  Family: %s (%s)\n", plan.Family.ID, plan.Family.Manufacturer)
	if plan.ForcedOverride {
		fmt.Fprintln(writer, "  ⚠ Family manually selected — not an automatic high-confidence match")
	}
	fmt.Fprintf(writer, "  Driver label: %s\n", shownValue(plan.Driver.Name))
	fmt.Fprintf(writer, "  Windows driver name: %s\n", shownValue(plan.DriverName))
	fmt.Fprintf(writer, "  Driver version: %s\n", shownValue(plan.Driver.Version))
	fmt.Fprintf(writer, "  Driver source: %s\n", shownValue(plan.Driver.Source))
	fmt.Fprintf(writer, "  Driver SHA-256: %s\n", shownValue(plan.Driver.SHA256))
	fmt.Fprintf(writer, "  Driver strategy: %s\n", shownValue(plan.Driver.Strategy))
	fmt.Fprintln(writer, "  Commands:")
	for _, command := range plan.Commands {
		fmt.Fprintf(writer, "    %s\n", command)
	}
}

func writeUninstallPlan(writer io.Writer, plan Plan, purgeDriver bool, compact bool) {
	fmt.Fprintln(writer, "Uninstall plan")
	if compact {
		fmt.Fprintf(writer, "  Queue: %s\n  Port: %s\n  Driver: %s\n", plan.PrinterName, plan.PortName, plan.DriverName)
		fmt.Fprintln(writer, "  Remove this queue; remove its unused SpoolSmith port. Shared/external ports are retained.")
		fmt.Fprintf(writer, "  Remove driver if unused: %t\n  Full commands: repeat with --dry-run --json.\n", purgeDriver)
		return
	}
	fmt.Fprintf(writer, "  Printer name: %s\n", plan.PrinterName)
	fmt.Fprintf(writer, "  Port name: %s\n", plan.PortName)
	fmt.Fprintf(writer, "  Driver name: %s\n", shownValue(plan.DriverName))
	fmt.Fprintf(writer, "  Purge driver: %t\n", purgeDriver)
	fmt.Fprintln(writer, "  Commands:")
	for _, command := range plan.Commands {
		fmt.Fprintf(writer, "    %s\n", command)
	}
}

func writeCommandResults(writer io.Writer, results []CommandResult) {
	if len(results) == 0 {
		return
	}
	fmt.Fprintln(writer, "Attempted commands before failure:")
	for _, result := range results {
		fmt.Fprintf(writer, "  Command: %s\n", result.Command)
		fmt.Fprintf(writer, "  Output: %s\n", shownValue(strings.TrimSpace(result.Output)))
		fmt.Fprintf(writer, "  Errored: %t\n", result.Err != nil)
	}
}

func shownValue(value string) string {
	if strings.TrimSpace(value) == "" {
		return "(not specified)"
	}
	return value
}

// planHashMatches reports whether plan is exactly the reviewed plan.
func planHashMatches(plan Plan, expected string) (bool, error) {
	actual, err := FingerprintPlan(plan)
	if err != nil {
		return false, err
	}
	return strings.EqualFold(actual, strings.TrimSpace(expected)), nil
}

func bundleDriverSource(payload BundleDriver) string {
	if strings.TrimSpace(payload.SourceHost) != "" {
		return fmt.Sprintf("Driver files exported from the Windows driver store on %s", payload.SourceHost)
	}
	return "Driver files exported from a Windows driver store"
}
