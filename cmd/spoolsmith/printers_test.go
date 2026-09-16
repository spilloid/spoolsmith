package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/spilloid/spoolsmith/internal/install"
)

// inventoryFakeEnvironment adds the listing capability to the CLI's fake.
type inventoryFakeEnvironment struct {
	*cliFakeEnvironment
	queues []install.InstalledQueue
	err    error
}

func (f *inventoryFakeEnvironment) ListPrinters(context.Context) ([]install.InstalledQueue, error) {
	return f.queues, f.err
}

func inventoryApplication(t *testing.T, queues []install.InstalledQueue) application {
	t.Helper()
	app := testApplication()
	app.environment = &inventoryFakeEnvironment{
		cliFakeEnvironment: app.environment.(*cliFakeEnvironment),
		queues:             queues,
	}
	return app
}

func sampleQueues() []install.InstalledQueue {
	return []install.InstalledQueue{
		{PrinterName: "Office", DriverName: "Brother HL-L2315D series", PortName: "SpoolSmith-192.0.2.10", HostAddress: "192.0.2.10", PortNumber: 9100, Protocol: 1, PortKnown: true},
		{PrinterName: "Microsoft Print to PDF", DriverName: "Microsoft Print To PDF", PortName: "PORTPROMPT:", PortKnown: true},
	}
}

func TestPrintersListsQueuesAndWhyOneCannotBeCopied(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run(t.Context(), []string{"printers"}, strings.NewReader(""), &stdout, &stderr, inventoryApplication(t, sampleQueues()))
	if code != 0 {
		t.Fatalf("printers exited %d: %s", code, stderr.String())
	}
	human := stderr.String()
	if !strings.Contains(human, "Office") || !strings.Contains(human, "192.0.2.10") {
		t.Fatalf("listing does not show the copyable queue: %s", human)
	}
	// The blocked queue must still be listed, with the reason stated rather
	// than silently dropped -- an operator needs to know it exists.
	if !strings.Contains(human, "Microsoft Print to PDF") || !strings.Contains(human, "not a standard TCP/IP port") {
		t.Fatalf("listing hides why a queue cannot be copied: %s", human)
	}

	var decoded []install.InstalledQueue
	if err := json.Unmarshal(stdout.Bytes(), &decoded); err != nil {
		t.Fatalf("printers did not emit a JSON array: %v (%s)", err, stdout.String())
	}
	if len(decoded) != 2 {
		t.Fatalf("printers emitted %d queues, want 2", len(decoded))
	}
}

func TestPrintersCopyableOnlyFiltersTheListing(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run(t.Context(), []string{"printers", "--copyable"}, strings.NewReader(""), &stdout, &stderr, inventoryApplication(t, sampleQueues()))
	if code != 0 {
		t.Fatalf("printers --copyable exited %d: %s", code, stderr.String())
	}
	var decoded []install.InstalledQueue
	if err := json.Unmarshal(stdout.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded) != 1 || decoded[0].PrinterName != "Office" {
		t.Fatalf("printers --copyable = %#v, want only the copyable queue", decoded)
	}
}

// An unattended caller that named no queue has not decided which printer it
// meant. It must be told, not guessed for.
func TestCopyWithoutAQueueNameRefusesWhenNotInteractive(t *testing.T) {
	app := inventoryApplication(t, sampleQueues())
	app.inputTerminal = false
	var stdout, stderr bytes.Buffer
	code := run(t.Context(), []string{"copy"}, strings.NewReader(""), &stdout, &stderr, app)
	if code == 0 {
		t.Fatal("copy chose a queue for a non-interactive caller")
	}
	if !strings.Contains(stderr.String(), "spoolsmith printers") {
		t.Fatalf("refusal does not point at the listing command: %s", stderr.String())
	}
}

func TestDefaultBundleName(t *testing.T) {
	tests := map[string]string{
		"Office":                 "Office.ssb",
		"Accounting HL-L2315D":   "Accounting-HL-L2315D.ssb",
		`Front Desk / Reception`: "Front-Desk---Reception.ssb",
		"":                       "printer.ssb",
		"///":                    "printer.ssb",
	}
	for queue, want := range tests {
		if got := defaultBundleName(queue); got != want {
			t.Fatalf("defaultBundleName(%q) = %q, want %q", queue, got, want)
		}
	}
	// Differently punctuated names can derive the same file name. The bundle
	// writer refuses to overwrite, so that collides loudly rather than
	// silently replacing a bundle the operator still needed.
	if defaultBundleName("Front Desk") != defaultBundleName("Front/Desk/") {
		t.Skip("derivation changed; re-check that collisions still fail closed in bundle.Write")
	}
}
