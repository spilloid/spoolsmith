//go:build windows

package bundle

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spilloid/spoolsmith/internal/install"
	"github.com/spilloid/spoolsmith/internal/probe"
)

// This gate explicitly authorizes temporary queue creation/removal. No print
// jobs are sent and no installed driver is removed. Ordinary tests skip it.
func TestHardwareAdminWSDImportAndUSBPreparation(t *testing.T) {
	if os.Getenv("SPOOLSMITH_TEST_ADMIN_MUTATIONS") != "1" {
		t.Skip("set SPOOLSMITH_TEST_ADMIN_MUTATIONS=1 in an elevated session")
	}
	queue := os.Getenv("SPOOLSMITH_TEST_WSD_QUEUE")
	if queue == "" {
		t.Fatal("SPOOLSMITH_TEST_WSD_QUEUE is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	env := install.NewEnvironment()
	if elevated, err := env.IsElevated(ctx); err != nil || !elevated {
		t.Fatalf("an elevated Windows session is required: elevated=%t, err=%v", elevated, err)
	}
	source, err := install.LookupPrinter(ctx, env, queue)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		current, err := install.LookupPrinter(context.Background(), env, queue)
		if err != nil || current != source {
			t.Errorf("source queue changed: before=%+v, after=%+v, err=%v", source, current, err)
		}
	})
	path := filepath.Join(t.TempDir(), "wsd.ssb")
	created, err := Create(ctx, env, probe.Collect, CreateOptions{QueueName: queue, Path: path})
	if err != nil {
		t.Fatal(err)
	}
	// Reopen the bundle and use the same verified payload API as Apply.
	opened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	payload, _, err := opened.PrepareDriver()
	if err != nil {
		t.Fatal(err)
	}
	profile := opened.Manifest.Profile
	if profile.PortType != "ipp" && payload == nil {
		t.Fatalf("OEM driver export is required for this release check: %s", created.DriverNotIncluded)
	}
	profile.PrinterName = fmt.Sprintf("SpoolSmith v1.4 validation %d", time.Now().UnixNano())
	workflow := install.NewWorkflow()
	var transcript bytes.Buffer
	options := install.InstallOptions{Profile: &profile, BundleDriver: payload, Yes: true}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		out, code := workflow.RunUninstall(cleanupCtx, env, strings.NewReader(""), &transcript, false, install.UninstallOptions{PrinterName: profile.PrinterName, Yes: true})
		if code != install.ExitSuccess {
			t.Errorf("temporary queue cleanup: %+v; %s", out, transcript.String())
		}
	})
	for attempt := 0; attempt < 2; attempt++ {
		out, code := workflow.RunInstall(ctx, env, strings.NewReader(""), &transcript, false, options)
		if code != install.ExitSuccess {
			t.Fatalf("hardware apply %d: %+v; %s", attempt+1, out, transcript.String())
		}
	}
	status, err := install.CheckStatus(ctx, env, profile)
	if err != nil || !status.Compliant {
		t.Fatalf("hardware queue status: %+v, %v", status, err)
	}
	t.Logf("Created and reapplied %s using driver %s and port %s", profile.PrinterName, profile.DriverName, status.Actual.PortName)
	if payload == nil {
		t.Log("USB preparation requires an exported OEM payload; source selected IPP, so this subcheck is unavailable")
		return
	}
	usb := profile
	usb.PortType, usb.SourcePort, usb.Target, usb.IPPURL = "usb", "USB001", "", ""
	usb.PrinterName += " USB offline"
	usb.Evidence.Provenance, usb.Evidence.ProvenanceNote = "unconfirmed", "Release validation: prepare driver without a connected USB printer"
	out, code := workflow.RunInstall(ctx, env, strings.NewReader(""), &transcript, false, install.InstallOptions{Profile: &usb, BundleDriver: payload, Offline: true, Yes: true})
	if code != install.ExitSuccess || out.Plan == nil || !out.Plan.USBOffline {
		t.Fatalf("USB offline preparation: %+v; %s", out, transcript.String())
	}
	if _, err := install.LookupPrinter(ctx, env, usb.PrinterName); !errors.Is(err, install.ErrPrinterNotFound) {
		t.Fatalf("USB offline preparation must create no queue: %v", err)
	}
	t.Log("USB offline preparation succeeded without creating a USB queue; existing OEM driver was reused")
}

func TestHardwareAdminIPPImport(t *testing.T) {
	if os.Getenv("SPOOLSMITH_TEST_ADMIN_MUTATIONS") != "1" {
		t.Skip("set SPOOLSMITH_TEST_ADMIN_MUTATIONS=1 in an elevated session")
	}
	queue := os.Getenv("SPOOLSMITH_TEST_WSD_QUEUE")
	if queue == "" {
		t.Fatal("SPOOLSMITH_TEST_WSD_QUEUE is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	env := install.NewEnvironment()
	if elevated, err := env.IsElevated(ctx); err != nil || !elevated {
		t.Fatalf("an elevated Windows session is required: elevated=%t, err=%v", elevated, err)
	}
	cloned, err := install.CloneQueue(ctx, env, queue)
	if err != nil {
		t.Fatal(err)
	}
	endpoint, ippModel, err := install.DiscoverIPPEndpointForDevice(ctx, cloned.HostAddress, cloned.SourceWSDDeviceID)
	if err != nil {
		t.Fatal(err)
	}
	created, err := Create(ctx, env, probe.Collect, CreateOptions{QueueName: queue, Path: filepath.Join(t.TempDir(), "ipp.ssb"), SettingsOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	profile := created.Manifest.Profile
	profile.PrinterName = fmt.Sprintf("SpoolSmith v1.4 IPP validation %d", time.Now().UnixNano())
	profile.PortType, profile.IPPURL, profile.DriverName = "ipp", endpoint, "Microsoft IPP Class Driver"
	// The model is what makes the v1.5 native-driver step run; without it the plan skips the step.
	profile.Evidence.IPPModel = ippModel
	// Directed discovery can reject an already registered WSD device even when
	// the requested IPP queue name is unique. Opt-in source preparation happens
	// only after capture; restore the source after the temporary IPP cleanup.
	if os.Getenv("SPOOLSMITH_TEST_PREPARE_IPP_SOURCE") == "1" {
		backupDir, err := filepath.Abs(filepath.Join("..", "..", "dist", "validation"))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(backupDir, 0o755); err != nil {
			t.Fatal(err)
		}
		backup := filepath.Join(backupDir, fmt.Sprintf("wsd-source-%d.xml", time.Now().UnixNano()))
		quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "''") + "'" }
		output, err := env.Run(ctx, "$ErrorActionPreference = 'Stop'; $p = Get-Printer -Name "+quote(queue)+"; $c = Get-PrintConfiguration -PrinterName $p.Name; if (@(Get-PrintJob -PrinterName $p.Name).Count -ne 0) { throw 'Source queue has pending print jobs' }; if ($p.Shared) { throw 'Shared source queues cannot be temporarily removed' }; $d = Get-CimInstance Win32_Printer | Where-Object Name -eq $p.Name; $location = Get-PnpDeviceProperty -InstanceId "+quote("SWD\\DAFWSDPROVIDER\\"+strings.ToUpper(cloned.SourceWSDDeviceID))+" -KeyName DEVPKEY_Device_LocationInfo; [PSCustomObject]@{Printer=$p; Ticket=$c.PrintTicketXML; Default=[bool]$d.Default; DeviceURL=[string]$location.Data} | Export-Clixml -LiteralPath "+quote(backup))
		if err != nil {
			t.Fatalf("snapshot WSD source: %v; %s", err, output)
		}
		t.Cleanup(func() {
			restoreCtx, restoreCancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer restoreCancel()
			output, err := env.Run(restoreCtx, "$ErrorActionPreference = 'Stop'; $s = Import-Clixml -LiteralPath "+quote(backup)+"; $p = $s.Printer; $port = @(Get-PrinterPort | Where-Object Name -eq $p.PortName); if (-not (Get-Printer | Where-Object Name -eq $p.Name)) { if ($port.Count -eq 1) { Add-Printer -Name $p.Name -DriverName $p.DriverName -PortName $p.PortName -PrintProcessor $p.PrintProcessor -Datatype $p.Datatype } else { Add-Printer -Name $p.Name -DeviceURL $s.DeviceURL } }; Set-Printer -Name $p.Name -DriverName $p.DriverName -Comment $p.Comment -Location $p.Location; Set-PrintConfiguration -PrinterName $p.Name -PrintTicketXml $s.Ticket; if ($s.Default) { $d = Get-CimInstance Win32_Printer | Where-Object Name -eq $p.Name; Invoke-CimMethod -InputObject $d -MethodName SetDefaultPrinter | Out-Null }; $restored = Get-Printer -Name $p.Name; $port = @(Get-PrinterPort | Where-Object Name -eq $restored.PortName); if ($restored.DriverName -ne $p.DriverName -or $port.Count -ne 1 -or $port[0].PortMonitor -ne 'WSD Port Monitor') { throw 'Restored source driver or WSD port differs' }; 'Restored original WSD queue with a verified WSD port'")
			if err != nil {
				t.Errorf("restore WSD source: %v; %s; backup=%s", err, output, backup)
			} else {
				t.Log(strings.TrimSpace(output))
			}
		})
		output, err = env.Run(ctx, "$ErrorActionPreference = 'Stop'; Remove-Printer -Name "+quote(queue)+"; 'Temporarily removed source WSD queue; retained its driver'")
		if err != nil {
			t.Fatalf("prepare IPP source: %v; %s", err, output)
		}
		t.Log(strings.TrimSpace(output))
	}
	workflow := install.NewWorkflow()
	var transcript bytes.Buffer
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		out, code := workflow.RunUninstall(cleanupCtx, env, strings.NewReader(""), &transcript, false, install.UninstallOptions{PrinterName: profile.PrinterName, Yes: true})
		if code != install.ExitSuccess {
			t.Errorf("temporary IPP queue cleanup: %+v; %s", out, transcript.String())
		}
	})
	for attempt := 0; attempt < 2; attempt++ {
		out, code := workflow.RunInstall(ctx, env, strings.NewReader(""), &transcript, false, install.InstallOptions{Profile: &profile, Yes: true})
		if code != install.ExitSuccess {
			t.Fatalf("IPP apply %d: %+v; %s", attempt+1, out, transcript.String())
		}
		// Record what the v1.5 native-driver step actually did on this printer.
		// A skip is acceptable (Windows may have no driver for the model); an
		// unrestored switch is not, and already fails the install.
		if out.Plan != nil && out.Result != nil {
			driver, derr := env.Run(ctx, "(Get-Printer -Name '"+strings.ReplaceAll(profile.PrinterName, "'", "''")+"').DriverName")
			t.Logf("IPP apply %d native driver: wanted=%t model=%q outcome=%q; queue driver now %q (err=%v)",
				attempt+1, out.Plan.NativeDriver, out.Plan.NativeDriverModel, out.Result.NativeDriver, strings.TrimSpace(driver), derr)
		}
	}
	status, err := install.CheckStatus(ctx, env, profile)
	if err != nil || !status.Compliant {
		t.Fatalf("IPP hardware queue status: %+v, %v", status, err)
	}
	t.Logf("Created and reapplied IPP fallback at %s; Windows port=%s, monitor=%s", endpoint, status.Actual.PortName, status.Actual.PortMonitor)
}
