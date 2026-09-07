package install

import (
	"context"
	"errors"
)

// DriverNames reads exact registered names through the shared OS adapter.
func DriverNames(ctx context.Context, env Environment) ([]string, error) {
	listing, ok := env.(interface {
		DriverNames(context.Context) ([]string, error)
	})
	if !ok {
		return nil, errors.New("registered driver listing is unavailable on this platform")
	}
	return listing.DriverNames(ctx)
}
