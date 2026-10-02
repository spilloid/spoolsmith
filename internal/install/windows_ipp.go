package install

import (
	"context"
	"errors"
	"strings"

	"github.com/spilloid/spoolsmith/internal/evidence"
)

// WindowsDriverChoice is the desktop's explicit Windows-managed IPP choice.
const WindowsDriverChoice = "Windows automatic (IPP)"

// CaptureWindowsIPPProfile reads the printer's advertised endpoint and model.
// It neither installs a driver nor creates a queue. Windows selects its inbox
// class driver only when the resulting plan is confirmed and applied.
func CaptureWindowsIPPProfile(ctx context.Context, target, name string) (Profile, error) {
	endpoint, model, err := DiscoverIPPEndpoint(ctx, target)
	if err != nil {
		return Profile{}, err
	}
	return windowsIPPProfile(target, name, endpoint, model)
}

func windowsIPPProfile(target, name, endpoint, model string) (Profile, error) {
	if strings.TrimSpace(model) == "" {
		return Profile{}, errors.New("IPP printer did not report its model; Windows automatic setup cannot capture its identity")
	}
	p := Profile{Version: 1, Target: target, PrinterName: name,
		PortType: "ipp", IPPURL: endpoint, DriverName: "Microsoft IPP Class Driver",
		Evidence: evidence.Evidence{IP: target, Provenance: "captured", IPPModel: model,
			ProvenanceNote: "Model and endpoint read with IPP Get-Printer-Attributes; Windows selects its inbox class driver"}}
	return p, p.Validate()
}
