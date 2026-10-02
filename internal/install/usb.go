package install

import (
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
)

// runUSBInstall stages a driver without a device when necessary, or maps it
// to a USB queue Windows has enumerated. The source port is never reused.
func (w Workflow) runUSBInstall(ctx context.Context, env Environment, input io.Reader, interactive io.Writer, inputIsTerminal bool, options InstallOptions) (Outcome, ExitCode) {
	outcome := Outcome{Operation: "install", Status: "error", DryRun: options.DryRun, Resolution: "operator-usb-profile"}
	var err error
	if options.ForceFamily != "" || options.Target != "" || options.Profile == nil {
		return failOutcome(outcome, errors.New("install: USB printer files cannot use an IP target or family override"), ExitUsageError)
	}
	if options.Profile.DriverPackage != nil {
		return failOutcome(outcome, errors.New("install: USB printer files require an installed driver or embedded driver payload"), ExitUsageError)
	}
	if options.USBQueue != "" {
		if options.Offline {
			return failOutcome(outcome, errors.New("install: --usb-queue cannot be combined with --offline; choose one USB queue to map or stage only the driver"), ExitUsageError)
		}
		if strings.TrimSpace(options.USBQueue) == "" {
			return failOutcome(outcome, errors.New("install: --usb-queue requires a Windows USB printer queue name"), ExitUsageError)
		}
		if err := validatePlanValue("USB queue name", options.USBQueue); err != nil {
			return failOutcome(outcome, err, ExitUsageError)
		}
	}
	var queue InstalledQueue
	offline := options.Offline
	if !offline {
		queues, err := ListUSBPrinters(ctx, env)
		if err != nil {
			return failOutcome(outcome, err, ExitPreflight)
		}
		var matches []InstalledQueue
		for _, candidate := range queues {
			wanted := options.USBQueue
			if wanted == "" {
				wanted = options.Profile.PrinterName
			}
			if strings.EqualFold(candidate.PrinterName, wanted) {
				matches = append(matches, candidate)
			}
		}
		if len(matches) == 0 && len(queues) == 0 && options.USBQueue == "" {
			offline = true
		} else {
			if len(matches) != 1 {
				var names []string
				for _, candidate := range queues {
					names = append(names, fmt.Sprintf("%s (%s)", candidate.PrinterName, candidate.PortName))
				}
				if len(matches) > 1 {
					return failOutcome(outcome, fmt.Errorf("install: several Windows USB queues have the chosen name %q; review this PC's printer queues before mapping", options.Profile.PrinterName), ExitUnresolved)
				}
				if options.USBQueue != "" {
					return failOutcome(outcome, fmt.Errorf("install: chosen USB queue %q was not found on a current Windows USB port; available USB queues: %s", options.USBQueue, strings.Join(names, ", ")), ExitUnresolved)
				}
				return failOutcome(outcome, fmt.Errorf("install: no Windows USB queue matches saved printer %q; available USB queues: %s. Choose the intended queue with --usb-queue, or use --offline to stage only the driver", options.Profile.PrinterName, strings.Join(names, ", ")), ExitUnresolved)
			}
			queue = matches[0]
		}
	}
	if err := validatePlanValue("Windows driver name", options.Profile.DriverName); err != nil {
		return failOutcome(outcome, err, ExitUsageError)
	}
	selection := options.Profile.selectedResolution()
	plan := Plan{USB: true, USBOffline: offline, SourcePrinterName: options.Profile.PrinterName, PrinterName: options.Profile.PrinterName, DriverName: options.Profile.DriverName, ForcedOverride: true, Driver: *selection.Driver, Family: *selection.Family}
	if !offline {
		for _, pair := range [][2]string{{"USB queue name", queue.PrinterName}, {"USB port", queue.PortName}, {"current driver name", queue.DriverName}} {
			if err := validatePlanValue(pair[0], pair[1]); err != nil {
				return failOutcome(outcome, err, ExitUsageError)
			}
		}
		plan.PrinterName, plan.PortName, plan.UpdateExisting = queue.PrinterName, queue.PortName, true
		name, port, oldDriver, driver := powerShellString(queue.PrinterName), powerShellString(queue.PortName), powerShellString(queue.DriverName), powerShellString(plan.DriverName)
		command := "$q = @(Get-Printer -ErrorAction Stop | Where-Object { $_.Name -eq " + name + " }); if ($q.Count -ne 1 -or $q[0].PortName -ne " + port + " -or $q[0].DriverName -ne " + oldDriver + ") { throw 'USB printer changed since preview; review again' }; " +
			"$p = @(Get-PrinterPort -ErrorAction Stop | Where-Object { $_.Name -eq " + port + " }); if ($p.Count -ne 1 -or (([string]$p[0].PortMonitor).Trim() -ne '' -and ([string]$p[0].PortMonitor).Trim() -ne 'USB Monitor') -or ([string]$p[0].PrinterHostAddress).Trim() -ne '' -or [int]$p[0].PortNumber -ne 0 -or [int]$p[0].Protocol -ne 0) { throw 'USB port changed since preview; review again' }; " +
			"if ($q[0].DriverName -ne " + driver + ") { Set-Printer -InputObject $q[0] -DriverName " + driver + " -ErrorAction Stop; 'Updated USB printer driver' } else { 'USB printer already uses this driver' }"
		plan.Commands = []string{powerShellCommand(command)}
	}
	if options.BundleDriver != nil {
		payload := *options.BundleDriver
		if err := payload.Validate(); err != nil {
			return failOutcome(outcome, err, ExitUsageError)
		}
		if payload.WindowsDriverName != plan.DriverName {
			return failOutcome(outcome, errors.New("install: USB bundle driver differs from saved printer driver"), ExitUsageError)
		}
		plan.BundleDriver = &payload
		plan.PublisherTrust = bundlePublisherTrust()
		plan.Driver.Strategy = "bundle-payload-if-missing"
		plan.Driver.Source = bundleDriverSource(payload)
		stage, err := bundleDriverCommand(payload)
		if err != nil {
			return failOutcome(outcome, err, ExitUsageError)
		}
		plan.Commands = append([]string{stage}, plan.Commands...)
	}
	outcome.Plan = &plan
	if hash, err := FingerprintPlan(plan); err == nil {
		outcome.PlanHash = hash
	}
	if offline {
		outcome.Resolution = "offline-operator-usb-profile"
		fmt.Fprintln(interactive, "No USB queue will be changed. The copied driver will be prepared now; connect the printer later and apply this file again to map its Windows USB queue.")
	} else {
		fmt.Fprintf(interactive, "Windows USB queue found: %s on %s. SpoolSmith cannot confirm the physical device model; confirm this is the intended printer before applying.\n", queue.PrinterName, queue.PortName)
	}
	writeInstallPlan(interactive, plan, options.Compact)
	if options.ExpectedPlan != nil && !reflect.DeepEqual(*options.ExpectedPlan, plan) {
		return failOutcome(outcome, errors.New("install: USB printer plan changed since preview; review again"), ExitNotConfirmed)
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
		matched, err := planHashMatches(plan, options.ConfirmPlanHash)
		if err != nil {
			return failOutcome(outcome, err, ExitGeneralError)
		}
		if !matched {
			return failOutcome(outcome, errors.New("install: USB printer plan does not match reviewed fingerprint"), ExitNotConfirmed)
		}
		confirmed = true
	}
	if !confirmed && inputIsTerminal && !options.NonInteractive && !options.JSON {
		confirmed, err = Confirm(bufferedReader(input), interactive)
		if err != nil {
			return failOutcome(outcome, err, ExitGeneralError)
		}
	}
	if !confirmed {
		outcome.Status = "not-confirmed"
		outcome.Error = "install: confirmation required; no commands were run"
		return outcome, ExitNotConfirmed
	}
	outcome.Confirmed = true
	result, err := Install(ctx, env, plan, true)
	outcome.Result = &result
	if err != nil {
		return failOutcome(outcome, err, ExitGeneralError)
	}
	outcome.Status = "success"
	if offline {
		fmt.Fprintf(interactive, "USB driver prepared: %s. No printer queue was installed or changed. Connect the printer and apply this file again to map its Windows USB queue.\n", plan.DriverName)
	} else {
		fmt.Fprintf(interactive, "USB printer driver configured: %s on %s. Check a test page.\n", plan.PrinterName, plan.PortName)
	}
	return outcome, ExitSuccess
}

// ListUSBPrinters returns only current Windows queues on standard USB printer
// ports. Callers can present these choices, while runUSBInstall verifies the
// selected queue again before building a plan.
func ListUSBPrinters(ctx context.Context, env Environment) ([]InstalledQueue, error) {
	queues, err := ListPrinters(ctx, env)
	if err != nil {
		return nil, err
	}
	var usb []InstalledQueue
	for _, candidate := range queues {
		if candidate.IsUSB() {
			usb = append(usb, candidate)
		}
	}
	return usb, nil
}
