//go:build !windows

package install

import (
	"context"
	"errors"
)

var errWindowsOnly = errors.New("install: windows-only, not supported on this platform")

type unsupportedEnvironment struct{}

// NewEnvironment returns an environment that fails clearly on non-Windows platforms.
func NewEnvironment() Environment {
	return unsupportedEnvironment{}
}

func (unsupportedEnvironment) IsElevated(context.Context) (bool, error) {
	return false, errWindowsOnly
}

func (unsupportedEnvironment) DriverPresent(context.Context, string) (bool, error) {
	return false, errWindowsOnly
}

func (unsupportedEnvironment) Run(context.Context, string) (string, error) {
	return "", errWindowsOnly
}

func (unsupportedEnvironment) LookupPrinter(context.Context, string) (PrinterConfiguration, error) {
	return PrinterConfiguration{}, errWindowsOnly
}
