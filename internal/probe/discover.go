package probe

import (
	"context"
	"fmt"
	"net/netip"
	"sort"
	"sync"

	"github.com/spilloid/spoolsmith/internal/catalog"
)

// Discovery retains candidates, including unsupported printers, without treating
// an open port as proof of identity or compatibility.
type Discovery struct {
	Network    string   `json:"network"`
	Scanned    int      `json:"scanned"`
	Candidates []Result `json:"candidates"`
}

// Discover probes an explicitly selected IPv4 subnet of at most 256 addresses.
func Discover(ctx context.Context, cidr string) (Discovery, error) {
	return discover(ctx, cidr, Collect)
}

func discover(ctx context.Context, cidr string, collect func(context.Context, string) (Result, error)) (Discovery, error) {
	prefix, err := netip.ParsePrefix(cidr)
	if err != nil || !prefix.Addr().Is4() || prefix.Bits() < 24 {
		return Discovery{}, fmt.Errorf("discover: supply an IPv4 CIDR from /24 through /32 (at most 256 addresses)")
	}
	prefix = prefix.Masked()
	result := Discovery{Network: prefix.String(), Candidates: []Result{}}
	var targets []string
	for addr := prefix.Addr(); prefix.Contains(addr); addr = addr.Next() {
		targets = append(targets, addr.String())
	}
	if prefix.Bits() <= 30 {
		targets = targets[1 : len(targets)-1]
	}
	jobs := make(chan string)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for target := range jobs {
				if ctx.Err() != nil {
					continue
				}
				observation, err := collect(ctx, target)
				mu.Lock()
				result.Scanned++
				if err != nil && firstErr == nil {
					firstErr = err
				}
				if err == nil && printerCandidate(observation) {
					result.Candidates = append(result.Candidates, observation)
				}
				mu.Unlock()
			}
		}()
	}
send:
	for _, target := range targets {
		select {
		case <-ctx.Done():
			break send
		case jobs <- target:
		}
	}
	close(jobs)
	wg.Wait()
	sort.Slice(result.Candidates, func(i, j int) bool {
		a, _ := netip.ParseAddr(result.Candidates[i].Evidence.IP)
		b, _ := netip.ParseAddr(result.Candidates[j].Evidence.IP)
		return a.Compare(b) < 0
	})
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	return result, firstErr
}

func printerCandidate(r Result) bool {
	if catalog.Resolve(r.Evidence).Family != nil {
		return true
	}
	if r.Evidence.PJLID != "" {
		return true
	}
	for _, port := range r.Evidence.OpenPorts {
		if port == 631 || port == 9100 || port == 515 {
			return true
		}
	}
	return false
}
