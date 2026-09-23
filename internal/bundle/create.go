package bundle

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spilloid/spoolsmith/internal/evidence"
	"github.com/spilloid/spoolsmith/internal/install"
	"github.com/spilloid/spoolsmith/internal/probe"
)

// Collector gathers live evidence from a printer, matching probe.Collect.
type Collector func(context.Context, string) (probe.Result, error)

// CreateOptions describes one copy of an installed queue into a bundle.
type CreateOptions struct {
	// QueueName is the installed queue to read.
	QueueName string
	// Path is the bundle file to write. It must not already exist.
	Path string
	// Note is operator free text recorded in the manifest.
	Note string
	// SettingsOnly skips the driver entirely. By default Create tries to
	// carry the driver package inside the bundle and falls back to a
	// settings-only bundle (explaining why in DriverNotIncluded) only when it
	// cannot; this is the explicit opt-out.
	SettingsOnly bool
	// CreatedBy and SourceHost are provenance for a human reading the
	// manifest, never a trust decision.
	CreatedBy  string
	SourceHost string
	// Progress, when set, receives human-readable step descriptions. Both
	// front ends report progress; only the destination differs.
	Progress func(string)
}

// CreateResult reports what was written.
type CreateResult struct {
	Manifest Manifest
	// ExportDir is the retained driver-export working directory, empty when no
	// driver was carried.
	ExportDir string
	// DriverNotIncluded is empty when the driver is embedded in the bundle.
	// Otherwise it says, in plain words, why it is not and what the other PC
	// therefore needs.
	DriverNotIncluded string
}

// Create reads one already-working queue off this machine and writes a bundle
// another machine can apply.
//
// This is the whole copy operation, shared by the CLI's `copy` command and the
// desktop app's Copy to a file, so the two cannot drift. That matters more than
// the saved duplication: this path decides what evidence is captured, whether a
// queue may be reproduced at all, and whether driver files leave the machine.
// Two implementations of that would be two security reviews.
//
// The driver is preferred, not optional: a bundle that carries its own driver
// sets up a PC that has never seen this printer. When the driver cannot be
// carried -- not running as administrator, or the export itself fails -- the
// copy still succeeds as a settings-only bundle and DriverNotIncluded says
// why. Cancellation is still a hard error.
//
// Front ends keep only what is genuinely theirs -- asking for the file name,
// and showing progress.
func Create(ctx context.Context, env install.Environment, collect Collector, opts CreateOptions) (CreateResult, error) {
	if err := ctx.Err(); err != nil {
		return CreateResult{}, err
	}
	report := opts.Progress
	if report == nil {
		report = func(string) {}
	}
	if strings.TrimSpace(opts.QueueName) == "" {
		return CreateResult{}, fmt.Errorf("copy: choose the printer to copy")
	}
	if strings.TrimSpace(opts.Path) == "" {
		return CreateResult{}, fmt.Errorf("copy: choose where to save the file")
	}

	// Checked here, before any network probe or driver export, so a collision
	// fails cheaply instead of after paying for both. Write still uses
	// exclusive creation to protect against a race after this check.
	if _, err := os.Stat(opts.Path); err == nil {
		return CreateResult{}, fmt.Errorf("copy: %s already exists; retry with a different, unused bundle filename", opts.Path)
	} else if !os.IsNotExist(err) {
		return CreateResult{}, fmt.Errorf("copy: check %s: %w", opts.Path, err)
	}

	// Exporting a driver reads the protected driver store through pnputil,
	// which requires Administrator. Checking first means a PC that cannot
	// export never pays for an opaque pnputil failure; it simply falls back
	// to a settings-only copy.
	tryDriver := !opts.SettingsOnly
	cannotExport := ""
	if tryDriver {
		elevated, err := env.IsElevated(ctx)
		if ctxErr := ctx.Err(); ctxErr != nil {
			return CreateResult{}, ctxErr
		}
		switch {
		case err != nil:
			cannotExport = fmt.Sprintf("administrator rights could not be checked (%v), so the driver was not copied", err)
			tryDriver = false
		case !elevated:
			cannotExport = "the driver needs administrator rights to copy"
			tryDriver = false
		}
	}

	report("Reading the printer's settings from Windows...")
	cloned, err := install.CloneQueue(ctx, env, opts.QueueName)
	if err != nil {
		return CreateResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return CreateResult{}, err
	}

	// The printer's own evidence is captured here, not copied from the queue,
	// so applying the bundle elsewhere still checks it is talking to the same
	// device rather than trusting the file.
	report(fmt.Sprintf("Checking the printer at %s...", cloned.HostAddress))
	probed, confirmed, err := collectIdentity(ctx, collect, cloned.HostAddress, report)
	if err != nil {
		return CreateResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return CreateResult{}, err
	}

	profile := install.Profile{
		Version:     1,
		Target:      probed.Evidence.IP,
		PrinterName: cloned.PrinterName,
		DriverName:  cloned.DriverName,
		Evidence:    probed.Evidence,
	}
	if !confirmed {
		// The printer wouldn't answer -- still worth writing a bundle from
		// what Windows already knows about this queue, rather than sending
		// the operator away with nothing. Applying it later already requires
		// this exact fallback (RunInstall's offline path), so the two halves
		// of a degraded copy line up on the same guarantee: nothing here was
		// ever confirmed against the real device.
		profile.Evidence.Provenance = "unconfirmed"
		profile.Evidence.ProvenanceNote = fmt.Sprintf("the printer at %s did not answer during copy; identity was never confirmed", cloned.HostAddress)
	}
	if err := profile.Validate(); err != nil {
		return CreateResult{}, err
	}

	manifest := Manifest{
		Version:    Version,
		CreatedBy:  opts.CreatedBy,
		SourceHost: opts.SourceHost,
		Note:       opts.Note,
		Profile:    profile,
	}

	result := CreateResult{}
	needsDriver := func(why string) string {
		return fmt.Sprintf("%s; the other PC must already have %q installed", why, cloned.DriverName)
	}
	notIncluded := func(why string) {
		result.DriverNotIncluded = needsDriver(why)
		report("Driver not included: " + result.DriverNotIncluded)
	}
	switch {
	case opts.SettingsOnly:
		result.DriverNotIncluded = needsDriver("settings only, as requested")
	case !tryDriver:
		notIncluded(cannotExport)
	}

	payloadRoot := ""
	if tryDriver {
		root, driver, exportErr := exportDriver(ctx, env, cloned.DriverName, report)
		if err := ctx.Err(); err != nil {
			if root != "" {
				os.RemoveAll(root)
			}
			return CreateResult{}, err
		}
		if exportErr != nil {
			// A settings-only bundle is still worth having: the driver is
			// preferred, never a reason to send the operator away empty.
			notIncluded(fmt.Sprintf("the driver could not be copied (%v)", exportErr))
		} else {
			manifest.Driver = driver
			payloadRoot = root
		}
	}

	report("Writing the file...")
	if err := ctx.Err(); err != nil {
		if payloadRoot != "" {
			os.RemoveAll(payloadRoot)
		}
		return CreateResult{}, err
	}
	if err := Write(opts.Path, manifest, payloadRoot); err != nil {
		if payloadRoot == "" {
			return CreateResult{}, err
		}
		// The export succeeded but could not be packaged (for example, a
		// driver package above the bundle size limit). Fall back to the
		// settings rather than failing the copy.
		os.RemoveAll(payloadRoot)
		payloadRoot = ""
		manifest.Driver = nil
		notIncluded(fmt.Sprintf("the driver could not be packaged (%v)", err))
		if err := Write(opts.Path, manifest, ""); err != nil {
			return CreateResult{}, err
		}
	}
	result.ExportDir = payloadRoot

	// Read back what was just written rather than trusting the write.
	report("Checking the file can be read back...")
	opened, err := Open(opts.Path)
	if err != nil {
		return result, fmt.Errorf("wrote %s but could not read it back: %w", opts.Path, err)
	}
	defer opened.Close()
	if err := opened.Verify(); err != nil {
		return result, err
	}
	result.Manifest = opened.Manifest
	return result, nil
}

// exportDriver copies the named driver's package out of the Windows driver
// store into a fresh working directory and describes it for the manifest. On
// failure the working directory is removed and root is empty.
func exportDriver(ctx context.Context, env install.Environment, driverName string, report func(string)) (root string, driver *DriverPayload, err error) {
	exportRoot, err := os.MkdirTemp("", "spoolsmith-export-")
	if err != nil {
		return "", nil, err
	}
	defer func() {
		if err != nil {
			os.RemoveAll(exportRoot)
			root = ""
		}
	}()
	report(fmt.Sprintf("Copying driver %q out of Windows. This can take a minute...", driverName))
	if err := ctx.Err(); err != nil {
		return "", nil, err
	}
	export, err := install.ExportDriver(ctx, env, driverName, exportRoot)
	if err != nil {
		return "", nil, err
	}
	infRelative, err := install.FindExportedINF(exportRoot, export.OriginalName)
	if err != nil {
		return "", nil, err
	}
	// Retained rather than deleted on success, matching how the existing
	// driver-staging path treats its own working directory.
	return exportRoot, &DriverPayload{
		WindowsDriverName: driverName,
		INF:               infRelative,
		ExportedFrom:      export.PublishedName,
	}, nil
}

// collectIdentity probes the printer for the identity evidence a bundle needs,
// retrying once when the first answer carries no identity at all.
//
// A printer's first contact after idling routinely answers thinner than the
// same printer seconds later -- this repo recorded exactly that against real
// hardware, where the first probe returned one open port and the next returned
// four. Failing the whole copy on that first thin answer would send an
// operator away from a machine that was about to work.
//
// The retry only repeats the probe; it never lowers the bar for what counts as
// identity. When the printer still hasn't answered after that retry --
// unreachable, powered off for a physical move, still asleep -- this reports
// that back instead of failing the whole copy: Create writes the bundle from
// what Windows already knows about the queue and marks its evidence
// unconfirmed, matching the same offline fallback RunInstall already uses when
// applying one. Only a canceled context is still a hard error here.
func collectIdentity(ctx context.Context, collect Collector, address string, report func(string)) (result probe.Result, confirmed bool, err error) {
	result, err = collect(ctx, address)
	if err == nil && hasIdentity(result) {
		return result, true, nil
	}
	report(fmt.Sprintf("The printer at %s did not answer clearly. Waking it and trying once more...", address))
	select {
	case <-ctx.Done():
		return probe.Result{}, false, ctx.Err()
	case <-time.After(2 * time.Second):
	}
	retry, retryErr := collect(ctx, address)
	if retryErr == nil && hasIdentity(retry) {
		return retry, true, nil
	}
	report(fmt.Sprintf("The printer at %s still could not be reached or confirmed. Capturing this queue's settings offline; the file will record that its identity was never confirmed.", address))
	return probe.Result{Evidence: evidence.Evidence{IP: address}}, false, nil
}

// hasIdentity reports whether a probe found any of the three identity sources
// a profile requires.
func hasIdentity(result probe.Result) bool {
	return strings.TrimSpace(result.Evidence.HTTPTitle) != "" ||
		strings.TrimSpace(result.Evidence.PJLID) != "" ||
		strings.TrimSpace(result.Evidence.SNMPSysDescr) != ""
}

// PrepareDriver verifies and extracts this bundle's driver payload, and
// describes it in the terms the install workflow needs.
//
// Extraction happens before any plan is shown so the plan can state what is
// actually on disk, and so a corrupt payload fails here rather than halfway
// through a privileged staging step. Nothing is staged into Windows until the
// plan is confirmed.
//
// Shared by both front ends for the same reason Create is: this decides what
// bytes a privileged step will later hand to pnputil.
func (b *Bundle) PrepareDriver() (*install.BundleDriver, string, error) {
	if b.Manifest.Driver == nil {
		return nil, "", nil
	}
	stageDir := b.StageDir()
	if err := b.EnsureExtracted(stageDir); err != nil {
		return nil, stageDir, err
	}
	return &install.BundleDriver{
		WindowsDriverName: b.Manifest.Driver.WindowsDriverName,
		PublishedName:     b.Manifest.Driver.ExportedFrom,
		SourceHost:        b.Manifest.SourceHost,
		INF:               b.Manifest.Driver.INF,
		StageDirName:      b.StageDirName(),
		FileCount:         len(b.Manifest.Driver.Files),
		TotalBytes:        b.Manifest.TotalPayloadBytes(),
		PayloadDigest:     b.PayloadDigest(),
	}, stageDir, nil
}
