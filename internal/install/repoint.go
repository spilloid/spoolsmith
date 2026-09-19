package install

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"reflect"
	"strings"

	"github.com/spilloid/spoolsmith/internal/catalog"
)

// RepointOptions contains the policy-relevant inputs for one repoint attempt.
type RepointOptions struct {
	PrinterName    string
	NewAddress     string
	Yes            bool
	JSON           bool
	NonInteractive bool
	DryRun         bool
	Compact        bool
	ExpectedPlan   *Plan
}

// BuildRepointPlan points an existing queue at a different printer address,
// keeping its name and its driver.
//
// A printer that moved, or a subnet that was renumbered, does not need a new
// queue — everything users already selected, defaulted to, and printed from
// stays valid, and only the endpoint is wrong. Tearing the queue down and
// rebuilding it would change the thing users interact with in order to fix the
// thing they never see.
//
// The commands come from installCommands with UpdateExisting set, which is
// exactly the reviewed "queue exists but its port differs" path: it adds the
// new port if absent, refuses a name collision with a conflicting endpoint,
// and moves the queue onto it. The old port is deliberately left in place —
// another queue may still print through it, and removing a port that is still
// in use is not a side effect a repoint should have.
func BuildRepointPlan(current PrinterConfiguration, newAddress string) (Plan, error) {
	if err := validatePlanValue("printer name", current.PrinterName); err != nil {
		return Plan{}, err
	}
	if err := validatePlanValue("Windows driver name", current.DriverName); err != nil {
		return Plan{}, err
	}
	if strings.TrimSpace(current.DriverName) == "" {
		return Plan{}, errors.New("repoint: Windows reports no driver for this queue")
	}
	if err := validatePlanValue("IP address", newAddress); err != nil {
		return Plan{}, err
	}
	parsed := net.ParseIP(strings.TrimSpace(newAddress))
	if parsed == nil {
		return Plan{}, fmt.Errorf("repoint: %q is not a literal IP address", newAddress)
	}
	ip := parsed.String()
	portName := "SpoolSmith-" + ip
	if strings.EqualFold(current.PortName, portName) {
		return Plan{}, fmt.Errorf("repoint: %q already prints to %s through port %q; nothing to change", current.PrinterName, ip, current.PortName)
	}
	family := catalog.Family{ID: "operator-repoint", Manufacturer: "Operator selected"}
	driver := catalog.DriverPackage{
		FamilyID:          family.ID,
		Name:              current.DriverName,
		WindowsDriverName: current.DriverName,
		Source:            "Driver already registered for this queue",
		Strategy:          "existing-windows-driver",
	}
	plan := Plan{
		PreviousPortName: current.PortName,
		IPAddress:        ip,
		PrinterName:      current.PrinterName,
		PortName:         portName,
		DriverName:       current.DriverName,
		Family:           family,
		Driver:           driver,
		UpdateExisting:   true,
	}
	plan.Commands = installCommands(plan)
	return plan, nil
}

// RunRepoint shows the change and applies it after one explicit confirmation,
// on the same gate as every other mutating path.
func (w Workflow) RunRepoint(ctx context.Context, env Environment, input io.Reader, interactive io.Writer, inputIsTerminal bool, options RepointOptions) (Outcome, ExitCode) {
	outcome := Outcome{Operation: "repoint", Status: "error", DryRun: options.DryRun}
	reader := bufferedReader(input)

	current, err := LookupPrinter(ctx, env, options.PrinterName)
	if errors.Is(err, ErrPrinterNotFound) {
		return failOutcome(outcome, fmt.Errorf("repoint: this PC has no queue named %q; run `spoolsmith printers` to list them", options.PrinterName), ExitUsageError)
	}
	if err != nil {
		return failOutcome(outcome, err, ExitGeneralError)
	}

	plan, err := BuildRepointPlan(current, options.NewAddress)
	if err != nil {
		return failOutcome(outcome, err, ExitUsageError)
	}
	if options.ExpectedPlan != nil && !reflect.DeepEqual(*options.ExpectedPlan, plan) {
		return failOutcome(outcome, errors.New("repoint: plan changed since preview; review a new preview before proceeding"), ExitNotConfirmed)
	}
	outcome.Plan = &plan
	if fingerprint, hashErr := FingerprintPlan(plan); hashErr == nil {
		outcome.PlanHash = fingerprint
	}

	fmt.Fprintln(interactive, "Repoint plan")
	fmt.Fprintf(interactive, "  Queue: %s (kept)\n  Driver: %s (kept)\n", plan.PrinterName, plan.DriverName)
	fmt.Fprintf(interactive, "  From port: %s\n  To port:   %s -> %s RAW TCP 9100\n", current.PortName, plan.PortName, plan.IPAddress)
	fmt.Fprintf(interactive, "  The old port %q is left in place; other queues may still use it.\n", current.PortName)
	if !options.Compact {
		for _, command := range plan.Commands {
			fmt.Fprintf(interactive, "  $ %s\n", command)
		}
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
	if !confirmed {
		if options.NonInteractive || options.JSON || !inputIsTerminal {
			outcome.Status = "not-confirmed"
			outcome.Error = "repoint: confirmation required; no commands were run"
			return outcome, ExitNotConfirmed
		}
		confirmed, err = Confirm(reader, interactive)
		if err != nil {
			return failOutcome(outcome, fmt.Errorf("repoint: read confirmation: %w", err), ExitGeneralError)
		}
	}
	if !confirmed {
		outcome.Status = "not-confirmed"
		outcome.Error = "repoint: confirmation declined; no commands were run"
		return outcome, ExitNotConfirmed
	}
	outcome.Confirmed = true

	result := Result{Plan: plan}
	for _, command := range plan.Commands {
		output, runErr := env.Run(ctx, command)
		result.Ran = append(result.Ran, CommandResult{Command: command, Output: output, Err: runErr})
		if runErr != nil {
			outcome.Result = &result
			writeCommandResults(interactive, result.Ran)
			return failOutcome(outcome, fmt.Errorf("repoint: run %q: %w", command, runErr), ExitGeneralError)
		}
	}
	outcome.Result = &result
	outcome.Status = "success"
	// This confirms the queue's configured address changed, not that printing
	// actually succeeds -- repoint never sends a test print or contacts the
	// new address to verify it.
	fmt.Fprintf(interactive, "Queue %q address updated to %s.\n", plan.PrinterName, plan.IPAddress)
	for _, ran := range result.Ran {
		if strings.TrimSpace(ran.Output) != "" {
			fmt.Fprintln(interactive, strings.TrimSpace(ran.Output))
		}
	}
	return outcome, ExitSuccess
}
