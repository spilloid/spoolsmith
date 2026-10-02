//go:build windows

package install

import "testing"

func TestWSDWinspoolEntryPoints(t *testing.T) {
	for name, proc := range map[string]interface{ Find() error }{"OpenPrinterW": wsdOpenPrinter, "XcvDataW": wsdXcvData, "ClosePrinter": wsdClosePrinter} {
		if err := proc.Find(); err != nil {
			t.Fatalf("%s is unavailable: %v", name, err)
		}
	}
}
