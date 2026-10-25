package install

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// installInventoryEnvironment is the read-only inventory a RAW plan's guards
// consult. It is asserted rather than added to Environment so the existing
// fakes stay valid; an environment without it skips the prediction and the
// guards in the generated commands still apply at mutation time.
type installInventoryEnvironment interface {
	printerLookupEnvironment
	portLookupEnvironment
}

// predictRAWConflicts mirrors, read-only, the throw conditions in
// installCommands: a same-named queue with another driver or port (unless the
// plan updates existing queues), and a port with the managed name that is not
// RAW 9100 at the plan's address. Comparisons are case-insensitive string
// comparisons, exactly as PowerShell's -ne makes them.
func predictRAWConflicts(ctx context.Context, env Environment, plan Plan) error {
	inventory, ok := env.(installInventoryEnvironment)
	if !ok {
		return nil
	}
	if !plan.UpdateExisting {
		existing, err := inventory.LookupPrinter(ctx, plan.PrinterName)
		switch {
		case errors.Is(err, ErrPrinterNotFound):
		case err != nil:
			return fmt.Errorf("install: read existing printers before applying: %w", err)
		case !strings.EqualFold(existing.DriverName, plan.DriverName) || !strings.EqualFold(existing.PortName, plan.PortName):
			return fmt.Errorf("install: this plan would stop before changing anything: an existing printer named %q uses driver %q on port %q, but this file maps driver %q on port %q; review an update with configure --profile or apply --update, or remove that printer first", existing.PrinterName, existing.DriverName, existing.PortName, plan.DriverName, plan.PortName)
		}
	}
	port, err := inventory.LookupPort(ctx, plan.PortName)
	switch {
	case errors.Is(err, ErrPortNotFound):
	case err != nil:
		return fmt.Errorf("install: read existing printer ports before applying: %w", err)
	case !strings.EqualFold(port.HostAddress, plan.IPAddress) || port.PortNumber != 9100 || port.Protocol != 1:
		return fmt.Errorf("install: this plan would stop before changing anything: an existing port named %q points to %q port %d (%s), not RAW 9100 at %s; choose a different target or repair the port", port.PortName, port.HostAddress, port.PortNumber, protocolName(port.Protocol), plan.IPAddress)
	}
	return nil
}
