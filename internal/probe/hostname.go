package probe

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"
)

func probeHostname(ctx context.Context, ip string) (string, error) {
	probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	names, err := net.DefaultResolver.LookupAddr(probeCtx, ip)
	if err != nil {
		return "", fmt.Errorf("reverse DNS: %w", err)
	}
	if len(names) == 0 {
		return "", fmt.Errorf("reverse DNS: no names found")
	}
	return strings.TrimSuffix(names[0], "."), nil
}
