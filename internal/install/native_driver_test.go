package install

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
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
