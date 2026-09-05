// Package probe collects bounded, read-only printer fingerprint evidence.
package probe

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/spilloid/spoolsmith/internal/evidence"
)

// Result contains collected evidence and the outcome of every sub-probe.
type Result struct {
	Evidence evidence.Evidence `json:"evidence"`
	Probes   []ProbeOutcome    `json:"probes"`
}

// ProbeOutcome describes one non-fatal sub-probe attempt.
type ProbeOutcome struct {
	Name     string        `json:"name"`
	Success  bool          `json:"success"`
	Detail   string        `json:"detail"`
	Duration time.Duration `json:"duration"`
}

// Collect resolves target, runs all independent probes concurrently, and
// assembles every value that was actually observed.
func Collect(ctx context.Context, target string) (Result, error) {
	ip, err := resolveTarget(ctx, target)
	if err != nil {
		return Result{}, err
	}

	result := Result{
		Evidence: evidence.Evidence{IP: ip, Provenance: "captured"},
		Probes:   make([]ProbeOutcome, 6),
	}
	var wg sync.WaitGroup
	run := func(index int, name string, fn func() (string, error)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			started := time.Now()
			detail, err := fn()
			outcome := ProbeOutcome{Name: name, Success: err == nil, Detail: detail, Duration: time.Since(started)}
			if err != nil {
				outcome.Detail = err.Error()
			}
			result.Probes[index] = outcome
		}()
	}

	run(0, "snmp", func() (string, error) {
		descr, objectID, err := probeSNMP(ctx, ip)
		if err != nil {
			return "", err
		}
		result.Evidence.SNMPSysDescr = descr
		result.Evidence.SNMPSysObjectID = objectID
		return fmt.Sprintf("sysDescr=%q, sysObjectID=%q", descr, objectID), nil
	})
	run(1, "http", func() (string, error) {
		title, err := probeHTTP(ctx, ip)
		if err != nil {
			return "", err
		}
		result.Evidence.HTTPTitle = title
		return fmt.Sprintf("title=%q", title), nil
	})
	run(2, "pjl", func() (string, error) {
		id, err := probePJL(ctx, ip)
		if err != nil {
			return "", err
		}
		result.Evidence.PJLID = id
		return fmt.Sprintf("id=%q", id), nil
	})
	run(3, "ports", func() (string, error) {
		ports := probePorts(ctx, ip)
		result.Evidence.OpenPorts = ports
		if len(ports) == 0 {
			return "no tested ports open", nil
		}
		return fmt.Sprintf("open=%v", ports), nil
	})
	run(4, "oui", func() (string, error) {
		vendor, err := probeOUI(ip)
		if err != nil {
			return "", err
		}
		result.Evidence.MACVendor = vendor
		return fmt.Sprintf("vendor=%q", vendor), nil
	})
	run(5, "hostname", func() (string, error) {
		hostname, err := probeHostname(ctx, ip)
		if err != nil {
			return "", err
		}
		result.Evidence.Hostname = hostname
		return fmt.Sprintf("hostname=%q", hostname), nil
	})

	wg.Wait()
	return result, nil
}

func resolveTarget(ctx context.Context, target string) (string, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return "", fmt.Errorf("resolve target: target is empty")
	}
	if parsed := net.ParseIP(target); parsed != nil {
		return parsed.String(), nil
	}
	resolveCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	addresses, err := net.DefaultResolver.LookupIPAddr(resolveCtx, target)
	if err != nil {
		return "", fmt.Errorf("resolve target %q: %w", target, err)
	}
	if len(addresses) == 0 {
		return "", fmt.Errorf("resolve target %q: no addresses found", target)
	}
	return addresses[0].IP.String(), nil
}
