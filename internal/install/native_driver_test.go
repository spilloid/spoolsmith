package install

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

func nativeTestPlan(t *testing.T) Plan {
	t.Helper()
	p, err := windowsIPPProfile("192.0.2.10", "Office", "ipp://192.0.2.10/ipp/print", "Example Printer 100")
	if err != nil {
		t.Fatal(err)
	}
	plan := Plan{IPP: true, IPPURL: p.IPPURL, IPAddress: p.Target, PrinterName: p.PrinterName, DriverName: p.DriverName}
	plan.NativeDriver, plan.NativeDriverModel = true, p.Evidence.IPPModel
	plan.Commands = installCommands(plan)
	return plan
}

func TestIPPPlanEndsWithBestEffortNativeDriverStep(t *testing.T) {
	plan := nativeTestPlan(t)
	if len(plan.Commands) != 2 {
		t.Fatalf("want create + native-driver commands, got %d", len(plan.Commands))
	}
	create, native := plan.Commands[0], plan.Commands[1]
	if strings.Contains(create, "Add-PrinterDriver") || strings.Contains(create, "Microsoft.Update.Session") {
		t.Fatal("queue creation must not depend on the native driver step")
	}
	for _, want := range []string{"Example Printer 100", "Add-PrinterDriver", "Microsoft.Update.Session", "Set-Printer", "SPOOLSMITH-NATIVE-DRIVER:"} {
		if !strings.Contains(native, want) {
			t.Errorf("native step missing %q", want)
		}
	}
	if strings.Contains(native, "Remove-Printer") || strings.Contains(native, "Remove-PrinterDriver") {
		t.Error("native step must never remove queues or drivers")
	}
	if strings.Contains(native, "PrinterHostAddress") && strings.Contains(native, "Add-PrinterPort") {
		t.Error("native step must not create or repoint ports")
	}
	noNative := plan
	noNative.NativeDriver = false
	if got := installCommands(noNative); len(got) != 1 {
		t.Fatalf("plan without native driver has %d commands", len(got))
	}
}

type nativeFailEnv struct {
	ran []string
	err map[int]error
	out map[int]string
}

func (e *nativeFailEnv) IsElevated(context.Context) (bool, error)            { return true, nil }
func (e *nativeFailEnv) DriverPresent(context.Context, string) (bool, error) { return true, nil }
func (e *nativeFailEnv) Run(_ context.Context, c string) (string, error) {
	i := len(e.ran)
	e.ran = append(e.ran, c)
	return e.out[i], e.err[i]
}

func TestNativeDriverFailureNeverFailsInstall(t *testing.T) {
	plan := nativeTestPlan(t)
	env := &nativeFailEnv{err: map[int]error{1: errors.New("powershell crashed")}}
	result, err := Install(context.Background(), env, plan, true)
	if err != nil {
		t.Fatalf("native-driver failure failed the install: %v", err)
	}
	if len(env.ran) != 2 || !strings.HasPrefix(result.NativeDriver, "skipped") {
		t.Fatalf("ran=%d native=%q", len(env.ran), result.NativeDriver)
	}
}

func TestNativeDriverOutcomeComesFromMarker(t *testing.T) {
	plan := nativeTestPlan(t)
	env := &nativeFailEnv{out: map[int]string{1: "noise\r\nSPOOLSMITH-NATIVE-DRIVER: applied Example Printer 100\r\n"}}
	result, err := Install(context.Background(), env, plan, true)
	if err != nil || result.NativeDriver != "applied Example Printer 100" {
		t.Fatalf("native=%q err=%v", result.NativeDriver, err)
	}
	env = &nativeFailEnv{out: map[int]string{1: "no marker at all"}}
	if result, _ = Install(context.Background(), env, plan, true); !strings.HasPrefix(result.NativeDriver, "skipped") {
		t.Fatalf("missing marker must read as skipped, got %q", result.NativeDriver)
	}
	// The queue-creation command still fails closed.
	env = &nativeFailEnv{err: map[int]error{0: errors.New("denied")}}
	if _, err = Install(context.Background(), env, plan, true); err == nil || len(env.ran) != 1 {
		t.Fatalf("creation failure must stop the plan: err=%v ran=%d", err, len(env.ran))
	}
}

func TestIPPStatusAcceptsNativeDriverOnlyOnVerifiedEndpoint(t *testing.T) {
	p, err := windowsIPPProfile("192.0.2.10", "Office", "ipp://192.0.2.10/ipp/print", "Example Printer 100")
	if err != nil {
		t.Fatal(err)
	}
	actual := LocalConfiguration{QueuePresent: true, PrinterName: "Office", DriverName: "Example Printer 100", DriverPresent: true,
		PortPresent: true, PortMonitor: "WSD Port Monitor", IPPVerified: true, PortName: "WSD-x", Address: p.IPPURL}
	env := statusEnv{actual}
	status, err := CheckStatus(context.Background(), env, p)
	if err != nil || !status.Compliant {
		t.Fatalf("native driver on verified endpoint: %+v err=%v", status, err)
	}
	actual.IPPVerified = false
	status, _ = CheckStatus(context.Background(), statusEnv{actual}, p)
	if status.Compliant {
		t.Fatal("different driver on an unverified endpoint must not be compliant")
	}
}

type statusEnv struct{ actual LocalConfiguration }

func (statusEnv) IsElevated(context.Context) (bool, error)            { return true, nil }
func (statusEnv) DriverPresent(context.Context, string) (bool, error) { return true, nil }
func (statusEnv) Run(context.Context, string) (string, error)         { return "", nil }
func (s statusEnv) LocalConfiguration(context.Context, string) (LocalConfiguration, error) {
	return s.actual, nil
}

func TestWorkflowIPPPlanRequestsNativeDriverUnlessOffline(t *testing.T) {
	p, err := windowsIPPProfile("192.0.2.10", "Office", "ipp://192.0.2.10/ipp/print", "Example Printer 100")
	if err != nil {
		t.Fatal(err)
	}
	for _, offline := range []bool{false, true} {
		w := testWorkflow(true)
		w.DiscoverIPP = func(context.Context, string) (string, string, error) { return p.IPPURL, p.Evidence.IPPModel, nil }
		env := &offlineEnvironment{fakeEnvironment: workflowEnvironment(true, false)}
		out, code := w.RunInstall(context.Background(), env, strings.NewReader(""), io.Discard, false, InstallOptions{Profile: &p, DryRun: true, Offline: offline})
		if code != ExitSuccess || out.Plan == nil {
			t.Fatalf("offline=%v: %+v code=%d", offline, out, code)
		}
		if out.Plan.NativeDriver == offline || out.Plan.NativeDriverModel != "Example Printer 100" {
			t.Fatalf("offline=%v: native=%v model=%q", offline, out.Plan.NativeDriver, out.Plan.NativeDriverModel)
		}
		if want := map[bool]int{false: 2, true: 1}[offline]; len(out.Plan.Commands) != want {
			t.Fatalf("offline=%v: %d commands", offline, len(out.Plan.Commands))
		}
		if len(env.fakeEnvironment.ran) != 0 {
			t.Fatal("dry run mutated Windows")
		}
	}
}

func TestWorkflowOfflineIPPProfileSkipsLiveIPPDiscovery(t *testing.T) {
	p, err := windowsIPPProfile("192.0.2.10", "Office", "ipp://192.0.2.10/ipp/print", "Example Printer 100")
	if err != nil {
		t.Fatal(err)
	}
	w := testWorkflow(true)
	called := false
	w.DiscoverIPP = func(context.Context, string) (string, string, error) {
		called = true
		return "", "", errors.New("printer unreachable")
	}
	env := &offlineEnvironment{fakeEnvironment: workflowEnvironment(true, false)}
	out, code := w.RunInstall(context.Background(), env, strings.NewReader(""), io.Discard, false, InstallOptions{Profile: &p, DryRun: true, Offline: true})
	if code != ExitSuccess || out.Plan == nil || out.Plan.NativeDriver {
		t.Fatalf("offline IPP profile must plan without a live probe: %+v code=%d", out, code)
	}
	if called {
		t.Fatal("--offline must not contact the printer for IPP discovery")
	}
}

func TestNormalizationIsASCIIOnlyAndRejectsUnusableModels(t *testing.T) {
	for model, want := range map[string]string{"Example Printer-100": "exampleprinter100", "打印机": "", "İPrinter 100": "printer100", "  ": ""} {
		if got := normalizeDriverName(model); got != want {
			t.Errorf("normalize(%q)=%q want %q", model, got, want)
		}
	}
	if sameDriverModel("anything", "打印机") || sameDriverModel("", "") {
		t.Fatal("an unusable model matched a driver")
	}
	if !sameDriverModel("Printer 100", "İPrinter 100") {
		t.Fatal("Go and PowerShell normalization must agree on non-ASCII input")
	}
}

func TestWorkflowSkipsNativeDriverForUnmatchableModel(t *testing.T) {
	p, err := windowsIPPProfile("192.0.2.10", "Office", "ipp://192.0.2.10/ipp/print", "打印机")
	if err != nil {
		t.Fatal(err)
	}
	w := testWorkflow(true)
	w.DiscoverIPP = func(context.Context, string) (string, string, error) { return p.IPPURL, p.Evidence.IPPModel, nil }
	out, code := w.RunInstall(context.Background(), workflowEnvironment(true, false), strings.NewReader(""), io.Discard, false, InstallOptions{Profile: &p, DryRun: true})
	if code != ExitSuccess || out.Plan == nil || out.Plan.NativeDriver || len(out.Plan.Commands) != 1 {
		t.Fatalf("plan=%+v code=%d", out.Plan, code)
	}
}

type deadlineEnv struct {
	nativeFailEnv
	deadlines []bool
}

func (e *deadlineEnv) Run(ctx context.Context, c string) (string, error) {
	_, ok := ctx.Deadline()
	e.deadlines = append(e.deadlines, ok)
	return e.nativeFailEnv.Run(ctx, c)
}

func TestNativeDriverStepIsTimeBoundedAndAttentionIsAnError(t *testing.T) {
	plan := nativeTestPlan(t)
	env := &deadlineEnv{}
	if _, err := Install(context.Background(), env, plan, true); err != nil {
		t.Fatal(err)
	}
	if len(env.deadlines) != 2 || env.deadlines[0] || !env.deadlines[1] {
		t.Fatalf("only the native-driver step may carry a deadline: %v", env.deadlines)
	}
	env = &deadlineEnv{nativeFailEnv: nativeFailEnv{out: map[int]string{1: "SPOOLSMITH-NATIVE-DRIVER: attention x; could not restore Microsoft IPP Class Driver: denied; review the queue"}}}
	result, err := Install(context.Background(), env, plan, true)
	if err == nil || !strings.HasPrefix(result.NativeDriver, "attention") {
		t.Fatalf("a failed restore must not read as success: err=%v native=%q", err, result.NativeDriver)
	}
}

// lookupFailEnv is nativeFailEnv that can also read the queue back.
type lookupFailEnv struct {
	nativeFailEnv
	driver    string
	lookupErr error
}

func (e *lookupFailEnv) LookupPrinter(_ context.Context, name string) (PrinterConfiguration, error) {
	return PrinterConfiguration{PrinterName: name, DriverName: e.driver}, e.lookupErr
}

// TestNativeDriverErrorReportsTheQueuesActualDriver: a step that errors or
// runs out of time may have stopped between switching the driver and
// verifying it, so the outcome reads the queue instead of claiming the class
// driver is still in place.
func TestNativeDriverErrorReportsTheQueuesActualDriver(t *testing.T) {
	plan := nativeTestPlan(t)
	for _, tc := range []struct {
		name string
		env  Environment
		want string
	}{
		{"class driver still in place", &lookupFailEnv{driver: "Microsoft IPP Class Driver"}, "keeping Microsoft IPP Class Driver"},
		{"switched before it stopped", &lookupFailEnv{driver: "Example Printer 100"}, "the queue now uses Example Printer 100; check the queue"},
		{"queue cannot be read", &lookupFailEnv{lookupErr: errors.New("spooler down")}, "current driver unknown; check the queue"},
		{"no way to read it", &nativeFailEnv{}, "current driver unknown; check the queue"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var base *nativeFailEnv
			switch env := tc.env.(type) {
			case *lookupFailEnv:
				base = &env.nativeFailEnv
			case *nativeFailEnv:
				base = env
			}
			base.err = map[int]error{1: errors.New("exit status 1")}
			base.out = map[int]string{1: "\r\n  Windows Update service is not running\r\nmore detail\r\n"}
			result, err := Install(context.Background(), tc.env, plan, true)
			if err != nil {
				t.Fatalf("a native-driver error failed the install: %v", err)
			}
			want := "skipped exit status 1: Windows Update service is not running; " + tc.want
			if result.NativeDriver != want {
				t.Fatalf("native = %q, want %q", result.NativeDriver, want)
			}
		})
	}
}

// cancelOnNativeEnv cancels the operator's context while the native step runs.
type cancelOnNativeEnv struct {
	nativeFailEnv
	cancel context.CancelFunc
}

func (e *cancelOnNativeEnv) Run(ctx context.Context, c string) (string, error) {
	if len(e.ran) == 1 {
		e.ran = append(e.ran, c)
		e.cancel()
		<-ctx.Done()
		return "", ctx.Err()
	}
	return e.nativeFailEnv.Run(ctx, c)
}

// TestNativeDriverStepCanceledIsNotASuccess: the step's own time limit is a
// quiet skip, but an operator cancel must never be reported as a finished
// install.
func TestNativeDriverStepCanceledIsNotASuccess(t *testing.T) {
	plan := nativeTestPlan(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	env := &cancelOnNativeEnv{cancel: cancel}
	result, err := Install(ctx, env, plan, true)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Install() after cancel = %v, want context.Canceled", err)
	}
	if !strings.Contains(err.Error(), "check the queue") || !strings.Contains(result.NativeDriver, "current driver unknown") {
		t.Fatalf("err = %v; native = %q", err, result.NativeDriver)
	}
}

// TestNativeDriverTimeoutIsStillAQuietSkip: running out of the step's own
// time is not an operator cancel.
func TestNativeDriverTimeoutIsStillAQuietSkip(t *testing.T) {
	plan := nativeTestPlan(t)
	env := &lookupFailEnv{driver: "Microsoft IPP Class Driver", nativeFailEnv: nativeFailEnv{err: map[int]error{1: context.DeadlineExceeded}}}
	result, err := Install(context.Background(), env, plan, true)
	if err != nil || result.NativeDriver != "skipped context deadline exceeded; keeping Microsoft IPP Class Driver" {
		t.Fatalf("err = %v; native = %q", err, result.NativeDriver)
	}
}

// TestFailedCommandNamesItsPositionAndOutputNotItsText: the generated command
// can run to kilobytes; the error says which one failed and what it printed,
// and the command stays in Result.Ran.
func TestFailedCommandNamesItsPositionAndOutputNotItsText(t *testing.T) {
	plan := nativeTestPlan(t)
	denied := errors.New("exit status 1")
	env := &nativeFailEnv{err: map[int]error{0: denied}, out: map[int]string{0: "\n Add-Printer : Access is denied.\r\n    + CategoryInfo : x\n"}}
	result, err := Install(context.Background(), env, plan, true)
	if err == nil || err.Error() != "install: command 1 of 2 failed: Add-Printer : Access is denied.: exit status 1" || !errors.Is(err, denied) {
		t.Fatalf("err = %v", err)
	}
	if len(result.Ran) != 1 || result.Ran[0].Command != plan.Commands[0] {
		t.Fatalf("ran = %+v", result.Ran)
	}
	if err := commandFailure("install", 0, 1, "  \r\n", denied); err.Error() != "install: command 1 of 1 failed: exit status 1" {
		t.Fatalf("no output: %v", err)
	}
	if got := firstLine(strings.Repeat("x", maxFirstLine+50)); len(got) != maxFirstLine+3 || !strings.HasSuffix(got, "...") {
		t.Fatalf("long line not capped: %d", len(got))
	}
}

// TestIPPPlanTextStatesTheNativeStepsLimitAndFailure: the reviewed plan says
// how long the step may take and the one way it can fail the setup.
func TestIPPPlanTextStatesTheNativeStepsLimitAndFailure(t *testing.T) {
	plan := nativeTestPlan(t)
	var text strings.Builder
	writeInstallPlan(&text, plan, true)
	want := "  This step can take up to 5 minutes. If a switch fails and the class driver cannot be put back, the setup is reported as failed.\n"
	if !strings.Contains(text.String(), want) {
		t.Fatalf("plan text missing %q:\n%s", want, text.String())
	}
	plan.NativeDriver = false
	text.Reset()
	writeInstallPlan(&text, plan, true)
	if strings.Contains(text.String(), "This step can take") {
		t.Fatalf("plan without the native step mentions it:\n%s", text.String())
	}
	saved := nativeDriverTimeout
	defer func() { nativeDriverTimeout = saved }()
	for limit, want := range map[time.Duration]string{time.Minute: "1 minute", 90 * time.Second: "1m30s"} {
		nativeDriverTimeout = limit
		if got := nativeDriverTimeLimit(); got != want {
			t.Errorf("nativeDriverTimeLimit(%v) = %q, want %q", limit, got, want)
		}
	}
}
