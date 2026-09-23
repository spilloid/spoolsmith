package install

import (
	"context"
	"errors"
	"github.com/spilloid/spoolsmith/internal/probe"
	"io"
	"strings"
	"testing"
)

type offlineEnvironment struct {
	*fakeEnvironment
	actual  LocalConfiguration
	readErr error
}

func (e *offlineEnvironment) LocalConfiguration(context.Context, string) (LocalConfiguration, error) {
	return e.actual, e.readErr
}
func matchingLocal(p Profile) LocalConfiguration {
	return LocalConfiguration{QueuePresent: true, PrinterName: p.PrinterName, DriverName: p.DriverName, DriverPresent: true, PortName: "RAW9100-" + p.Target, PortPresent: true, Address: p.Target, Protocol: 1, PortNumber: 9100}
}
func TestOfflineProvisioningNeverProbesAndVerifies(t *testing.T) {
	p := sampleProfile()
	for _, update := range []bool{false, true} {
		e := &offlineEnvironment{fakeEnvironment: workflowEnvironment(true, true), actual: matchingLocal(p)}
		w := NewWorkflow()
		w.Collect = func(context.Context, string) (probe.Result, error) {
			t.Fatal("offline invoked network probe")
			return probe.Result{}, nil
		}
		for i := 0; i < 2; i++ {
			out, code := w.RunInstall(context.Background(), e, panicReader{}, io.Discard, false, InstallOptions{Profile: &p, Offline: true, Yes: true, UpdateExisting: update})
			if code != 0 || out.LocalStatus == nil || !out.LocalStatus.Compliant || !out.Plan.Offline || out.Resolution != "offline-operator-profile" {
				t.Fatalf("code=%d outcome=%+v", code, out)
			}
		}
	}
}
func TestOfflineFailureBoundaries(t *testing.T) {
	for _, mode := range []string{"no profile", "invalid profile", "mixed target", "forced family", "dry run", "unconfirmed", "missing driver", "not elevated", "conflict", "verification mismatch", "inventory error"} {
		t.Run(mode, func(t *testing.T) {
			p := sampleProfile()
			e := &offlineEnvironment{fakeEnvironment: workflowEnvironment(true, true), actual: matchingLocal(p)}
			opts := InstallOptions{Profile: &p, Offline: true, Yes: true, NonInteractive: true}
			want := ExitSuccess
			mutations := false
			switch mode {
			case "no profile":
				opts.Profile = nil
				want = ExitUsageError
			case "invalid profile":
				p.DriverName = ""
				want = ExitUsageError
			case "mixed target":
				opts.Target = p.Target
				want = ExitUsageError
			case "forced family":
				opts.ForceFamily = "brother-hl-l2xxx"
				want = ExitUsageError
			case "dry run":
				opts.DryRun = true
			case "unconfirmed":
				opts.Yes = false
				want = ExitNotConfirmed
			case "missing driver":
				e.driverPresent = false
				want = ExitPreflight
			case "not elevated":
				e.elevated = false
				want = ExitPreflight
			case "conflict":
				e.runErrors = map[int]error{0: errors.New("conflicting port")}
				want = ExitGeneralError
				mutations = true
			case "verification mismatch":
				e.actual.PortNumber = 515
				want = ExitGeneralError
				mutations = true
			case "inventory error":
				e.readErr = errors.New("spooler unavailable")
				want = ExitGeneralError
				mutations = true
			}
			w := NewWorkflow()
			w.Collect = func(context.Context, string) (probe.Result, error) {
				t.Fatal("offline probed")
				return probe.Result{}, nil
			}
			out, code := w.RunInstall(context.Background(), e, panicReader{}, io.Discard, false, opts)
			if code != want || (!mutations && len(e.ran) > 0) {
				t.Fatalf("code=%d want=%d ran=%d out=%+v", code, want, len(e.ran), out)
			}
			if mutations && out.Result == nil {
				t.Fatal("lost mutation diagnostics")
			}
		})
	}
}
func TestLocalStatusRejectsEachMismatch(t *testing.T) {
	p := sampleProfile()
	changes := []func(*LocalConfiguration){func(c *LocalConfiguration) { c.QueuePresent = false }, func(c *LocalConfiguration) { c.PrinterName = "other" }, func(c *LocalConfiguration) { c.DriverPresent = false }, func(c *LocalConfiguration) { c.DriverName = "other" }, func(c *LocalConfiguration) { c.PortName = "other" }, func(c *LocalConfiguration) { c.PortPresent = false }, func(c *LocalConfiguration) { c.Address = "192.0.2.11" }, func(c *LocalConfiguration) { c.Protocol = 2 }, func(c *LocalConfiguration) { c.PortNumber = 515 }}
	for _, change := range changes {
		e := &offlineEnvironment{fakeEnvironment: workflowEnvironment(true, true), actual: matchingLocal(p)}
		change(&e.actual)
		status, err := CheckStatus(context.Background(), e, p)
		if err != nil || status.Compliant || len(status.Mismatches) == 0 || len(e.ran) > 0 {
			t.Fatalf("%+v %v", status, err)
		}
	}
}

// TestDefaultDegradesToOfflineWhenPrinterUnreachable is the fix for the actual
// operator complaint this file's other tests were written against: a Profile
// means the printer was already reviewed and approved once, so a printer that
// won't answer today -- still booting after a physical move, a cable not yet
// seated -- should not throw that trust away. install (and apply, which
// shares this exact code path for a bundle's Profile) now falls back to the
// same offline path --offline already offers, and says so plainly, rather
// than failing outright.
func TestDefaultDegradesToOfflineWhenPrinterUnreachable(t *testing.T) {
	p := sampleProfile()
	e := &offlineEnvironment{fakeEnvironment: workflowEnvironment(true, true), actual: matchingLocal(p)}
	w := NewWorkflow()
	calls := 0
	w.Collect = func(context.Context, string) (probe.Result, error) {
		calls++
		return probe.Result{}, errors.New("unreachable")
	}
	out, code := w.RunInstall(context.Background(), e, panicReader{}, io.Discard, false, InstallOptions{Profile: &p, Yes: true})
	if code != ExitSuccess || calls != 1 || len(e.ran) == 0 {
		t.Fatalf("%+v code=%d calls=%d", out, code, calls)
	}
	if out.Resolution != "offline-fallback-operator-profile" || out.LocalStatus == nil || !out.LocalStatus.Compliant {
		t.Fatalf("did not record an offline fallback: %+v", out)
	}
	if !containsSubstring(out.Uncertain, "could not be contacted") {
		t.Fatalf("no operator-visible reason for the fallback: %+v", out.Uncertain)
	}
}

// TestNoFallbackWithoutAProfile is the boundary on the fix above: with no
// Profile there is nothing already-approved to fall back to -- automatic
// catalog resolution needs live evidence, so an unreachable printer still
// fails outright here, exactly as it always has.
func TestNoFallbackWithoutAProfile(t *testing.T) {
	w := NewWorkflow()
	calls := 0
	w.Collect = func(context.Context, string) (probe.Result, error) {
		calls++
		return probe.Result{}, errors.New("unreachable")
	}
	e := workflowEnvironment(true, true)
	out, code := w.RunInstall(context.Background(), e, panicReader{}, io.Discard, false, InstallOptions{Target: "192.0.2.10", Yes: true})
	if code == 0 || calls != 1 || len(e.ran) > 0 || !strings.Contains(out.Error, "unreachable") {
		t.Fatalf("%+v code=%d calls=%d", out, code, calls)
	}
}

func containsSubstring(values []string, substr string) bool {
	for _, v := range values {
		if strings.Contains(v, substr) {
			return true
		}
	}
	return false
}

// Cancellation must never be converted into permission to provision offline.
func TestOfflineFallbackPreservesCancellation(t *testing.T) {
	for _, mode := range []string{"before probe", "first probe", "identity retry", "returned cancellation"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "before probe" {
				cancel()
			}
			p := sampleProfile()
			e := &offlineEnvironment{fakeEnvironment: workflowEnvironment(true, true), actual: matchingLocal(p)}
			w := NewWorkflow()
			calls := 0
			w.Collect = func(context.Context, string) (probe.Result, error) {
				calls++
				if mode == "identity retry" && calls == 1 {
					return probe.Result{}, nil
				}
				if mode != "returned cancellation" {
					cancel()
				}
				return probe.Result{}, context.Canceled
			}
			out, code := w.RunInstall(ctx, e, panicReader{}, io.Discard, false, InstallOptions{Profile: &p, Yes: true})
			if code == ExitSuccess || len(e.ran) != 0 || !strings.Contains(out.Error, "canceled") {
				t.Fatalf("cancellation became setup: code=%d ran=%d out=%+v", code, len(e.ran), out)
			}
			if mode == "before probe" && calls != 0 {
				t.Fatal("probed after cancellation")
			}
		})
	}
}
