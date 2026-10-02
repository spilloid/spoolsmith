package install

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/spilloid/spoolsmith/internal/evidence"
)

type usbTestEnvironment struct {
	*fakeEnvironment
	queues []InstalledQueue
}

func (e *usbTestEnvironment) ListPrinters(context.Context) ([]InstalledQueue, error) {
	return e.queues, nil
}

func usbTestProfile() Profile {
	return Profile{Version: 1, PortType: "usb", SourcePort: "USB001", PrinterName: "Desk Printer", DriverName: "Vendor Driver", Evidence: evidence.Evidence{Provenance: "unconfirmed", ProvenanceNote: "USB copy"}}
}

func TestUSBProfileRequiresSourcePortWithoutIP(t *testing.T) {
	p := usbTestProfile()
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	p.Target = "192.0.2.1"
	if err := p.Validate(); err == nil {
		t.Fatal("USB profile accepted an IP target")
	}
}

func TestUSBInstallFindsQueueAndGuardsPortAtMutation(t *testing.T) {
	env := &usbTestEnvironment{fakeEnvironment: workflowEnvironment(true, true), queues: []InstalledQueue{{PrinterName: "Desk Printer", DriverName: "Generic Driver", PortName: "USB002", PortKnown: true}}}
	p := usbTestProfile()
	var output bytes.Buffer
	workflow := NewWorkflow()
	preview, code := workflow.RunInstall(context.Background(), env, strings.NewReader(""), &output, false, InstallOptions{Profile: &p, DryRun: true})
	if code != ExitSuccess || preview.Plan == nil || !preview.Plan.USB || preview.Plan.PortName != "USB002" || len(env.ran) != 0 {
		t.Fatalf("preview = %+v, code = %d, ran = %v", preview, code, env.ran)
	}
	if !strings.Contains(preview.Plan.Commands[0], "USB002") || !strings.Contains(preview.Plan.Commands[0], "Generic Driver") {
		t.Fatalf("mutation command does not guard reviewed queue: %s", preview.Plan.Commands[0])
	}
	apply, code := workflow.RunInstall(context.Background(), env, strings.NewReader(""), &output, false, InstallOptions{Profile: &p, ExpectedPlan: preview.Plan, Yes: true})
	if code != ExitSuccess || apply.Status != "success" || len(env.ran) != 1 {
		t.Fatalf("apply = %+v, code = %d, ran = %v", apply, code, env.ran)
	}
}

func TestUSBInstallRefusesAmbiguousQueues(t *testing.T) {
	env := &usbTestEnvironment{fakeEnvironment: workflowEnvironment(true, true), queues: []InstalledQueue{{PrinterName: "A", PortName: "USB001", PortKnown: true}, {PrinterName: "B", PortName: "USB002", PortKnown: true}}}
	p := usbTestProfile()
	_, code := NewWorkflow().RunInstall(context.Background(), env, strings.NewReader(""), &bytes.Buffer{}, false, InstallOptions{Profile: &p, Yes: true})
	if code != ExitUnresolved || len(env.ran) != 0 {
		t.Fatalf("code = %d, ran = %v", code, env.ran)
	}
}

func TestUSBInstallDoesNotMapSoleUnrelatedQueue(t *testing.T) {
	env := &usbTestEnvironment{fakeEnvironment: workflowEnvironment(true, true), queues: []InstalledQueue{{PrinterName: "Other Printer", DriverName: "Other Driver", PortName: "USB002", Monitor: "USB Monitor", PortKnown: true}}}
	p := usbTestProfile()
	outcome, code := NewWorkflow().RunInstall(context.Background(), env, strings.NewReader(""), &bytes.Buffer{}, false, InstallOptions{Profile: &p, Yes: true})
	if code != ExitUnresolved || outcome.Plan != nil || len(env.ran) != 0 || !strings.Contains(outcome.Error, "--usb-queue") {
		t.Fatalf("unrelated USB queue = %+v, code = %d, ran = %v", outcome, code, env.ran)
	}
}

func TestUSBInstallMapsExplicitExistingQueue(t *testing.T) {
	env := &usbTestEnvironment{fakeEnvironment: workflowEnvironment(true, true), queues: []InstalledQueue{
		{PrinterName: "Other Printer", DriverName: "Other Driver", PortName: "USB002", Monitor: "USB Monitor", PortKnown: true},
		{PrinterName: "Connected Device", DriverName: "Generic Driver", PortName: "USB003", Monitor: "USB Monitor", PortKnown: true},
	}}
	p := usbTestProfile()
	var output bytes.Buffer
	preview, code := NewWorkflow().RunInstall(context.Background(), env, strings.NewReader(""), &output, false, InstallOptions{Profile: &p, USBQueue: "Connected Device", DryRun: true})
	if code != ExitSuccess || preview.Plan == nil || preview.Plan.PrinterName != "Connected Device" || preview.Plan.PortName != "USB003" || preview.Plan.DriverName != p.DriverName || preview.Plan.USBOffline || len(env.ran) != 0 {
		t.Fatalf("explicit USB preview = %+v, code = %d, ran = %v", preview, code, env.ran)
	}
	if !strings.Contains(preview.Plan.Commands[0], "USB003") || !strings.Contains(preview.Plan.Commands[0], "Generic Driver") || !strings.Contains(preview.Plan.Commands[0], "Get-PrinterPort") || !strings.Contains(preview.Plan.Commands[0], "USB port changed since preview") {
		t.Fatalf("explicit USB plan lacks reviewed port/current driver guard: %s", preview.Plan.Commands[0])
	}
	apply, code := NewWorkflow().RunInstall(context.Background(), env, strings.NewReader(""), &output, false, InstallOptions{Profile: &p, USBQueue: "Connected Device", ExpectedPlan: preview.Plan, Yes: true})
	if code != ExitSuccess || apply.Status != "success" || len(env.ran) != 1 {
		t.Fatalf("explicit USB apply = %+v, code = %d, ran = %v", apply, code, env.ran)
	}
}

func TestUSBInstallRejectsNamedNonUSBQueue(t *testing.T) {
	env := &usbTestEnvironment{fakeEnvironment: workflowEnvironment(true, true), queues: []InstalledQueue{{PrinterName: "Connected Device", DriverName: "Generic Driver", PortName: "USB003", Monitor: "Standard TCP/IP Port", PortKnown: true}}}
	p := usbTestProfile()
	outcome, code := NewWorkflow().RunInstall(context.Background(), env, strings.NewReader(""), &bytes.Buffer{}, false, InstallOptions{Profile: &p, USBQueue: "Connected Device", Yes: true})
	if code != ExitUnresolved || outcome.Plan != nil || len(env.ran) != 0 || !strings.Contains(outcome.Error, "not found on a current Windows USB port") {
		t.Fatalf("non-USB queue = %+v, code = %d, ran = %v", outcome, code, env.ran)
	}
}

func TestUSBPortMonitorAuthority(t *testing.T) {
	for _, tt := range []struct {
		monitor string
		want    bool
	}{
		{monitor: "USB Monitor", want: true},
		{monitor: "", want: true}, // Older inventory fixtures have no monitor field.
		{monitor: "Standard TCP/IP Port", want: false},
		{monitor: "Local Port", want: false},
	} {
		port := PortConfiguration{PortName: "USB001", Monitor: tt.monitor}
		if got := isUSBConfiguration(port); got != tt.want {
			t.Errorf("USB001 with monitor %q: isUSBConfiguration = %v, want %v", tt.monitor, got, tt.want)
		}
	}
}

func TestUSBFilePreparesDriverWithoutConnectedQueue(t *testing.T) {
	env := &usbTestEnvironment{fakeEnvironment: workflowEnvironment(true, false)}
	p := usbTestProfile()
	payload := BundleDriver{WindowsDriverName: p.DriverName, INF: "driver.inf", StageDirName: "SpoolSmith-bundle-usb", PayloadDigest: "digest"}
	workflow := NewWorkflow()
	var output bytes.Buffer
	preview, code := workflow.RunInstall(context.Background(), env, strings.NewReader(""), &output, false, InstallOptions{Profile: &p, BundleDriver: &payload, DryRun: true})
	if code != ExitSuccess || preview.Plan == nil || !preview.Plan.USBOffline || len(preview.Plan.Commands) != 1 || len(env.ran) != 0 {
		t.Fatalf("offline USB preview = %+v, code = %d, ran = %v", preview, code, env.ran)
	}
	if !strings.Contains(preview.Plan.Commands[0], "pnputil.exe /add-driver") {
		t.Fatal("offline plan did not stage the driver")
	}
	apply, code := workflow.RunInstall(context.Background(), env, strings.NewReader(""), &output, false, InstallOptions{Profile: &p, BundleDriver: &payload, ExpectedPlan: preview.Plan, Yes: true})
	if code != ExitSuccess || apply.Status != "success" || len(env.ran) != 1 || strings.Contains(env.ran[0], "Set-Printer") {
		t.Fatalf("offline USB apply = %+v, code = %d, ran = %v", apply, code, env.ran)
	}
}

func TestUSBOfflineOptionStagesOnlyEvenWithQueue(t *testing.T) {
	env := &usbTestEnvironment{fakeEnvironment: workflowEnvironment(true, true), queues: []InstalledQueue{{PrinterName: "Desk Printer", DriverName: "Generic Driver", PortName: "USB002", PortKnown: true}}}
	p := usbTestProfile()
	outcome, code := NewWorkflow().RunInstall(context.Background(), env, strings.NewReader(""), &bytes.Buffer{}, false, InstallOptions{Profile: &p, Offline: true, DryRun: true})
	if code != ExitSuccess || outcome.Plan == nil || !outcome.Plan.USBOffline || len(outcome.Plan.Commands) != 0 {
		t.Fatalf("explicit USB offline preview = %+v, code = %d", outcome, code)
	}
}

func TestUSBProfileCannotEnterIPStatusOrRemoval(t *testing.T) {
	p := usbTestProfile()
	env := &usbTestEnvironment{fakeEnvironment: workflowEnvironment(true, true)}
	if _, err := CheckStatus(context.Background(), env, p); err == nil || !strings.Contains(err.Error(), "USB") {
		t.Fatalf("CheckStatus() = %v", err)
	}
	_, code := NewWorkflow().RunUninstall(context.Background(), env, strings.NewReader(""), &bytes.Buffer{}, false, UninstallOptions{Profile: &p, PrinterName: p.PrinterName, DryRun: true})
	if code != ExitUsageError {
		t.Fatalf("USB removal profile code = %d", code)
	}
}
