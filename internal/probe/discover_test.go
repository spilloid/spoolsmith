package probe

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/spilloid/spoolsmith/internal/evidence"
)

func TestDiscoverBoundariesAndCandidates(t *testing.T) {
	for _, tc := range []struct {
		cidr string
		want int
	}{{"192.0.2.0/30", 2}, {"192.0.2.0/31", 2}, {"192.0.2.9/32", 1}, {"192.0.2.15/24", 254}} {
		result, err := discover(context.Background(), tc.cidr, func(_ context.Context, ip string) (Result, error) {
			return Result{Evidence: evidence.Evidence{IP: ip, OpenPorts: []int{9100}}}, nil
		})
		if err != nil || result.Scanned != tc.want || len(result.Candidates) != tc.want {
			t.Fatalf("%s: %#v %v", tc.cidr, result, err)
		}
	}
	for _, cidr := range []string{"192.0.2.0/23", "::/120", "192.0.2.1", "bad"} {
		_, err := discover(context.Background(), cidr, func(context.Context, string) (Result, error) { panic("invalid network was scanned") })
		if err == nil {
			t.Fatalf("accepted %q", cidr)
		}
	}
	for _, tc := range []struct {
		e    evidence.Evidence
		want bool
	}{
		{evidence.Evidence{OpenPorts: []int{80, 443}}, false},
		{evidence.Evidence{OpenPorts: []int{631}}, true},
		{evidence.Evidence{PJLID: "Unknown printer"}, true},
	} {
		if printerCandidate(Result{Evidence: tc.e}) != tc.want {
			t.Fatalf("classification: %#v", tc)
		}
	}
}

func TestDiscoverNumericOrderAndCancellation(t *testing.T) {
	result, err := discover(context.Background(), "192.0.2.0/28", func(_ context.Context, ip string) (Result, error) {
		return Result{Evidence: evidence.Evidence{IP: ip, PJLID: "Printer"}}, nil
	})
	if err != nil || result.Candidates[1].Evidence.IP != "192.0.2.2" || result.Candidates[9].Evidence.IP != "192.0.2.10" {
		t.Fatalf("wrong numeric order: %#v %v", result, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var calls atomic.Int32
	_, err = discover(ctx, "192.0.2.0/24", func(context.Context, string) (Result, error) { calls.Add(1); return Result{}, nil })
	if !errors.Is(err, context.Canceled) || calls.Load() != 0 {
		t.Fatalf("cancellation: %v calls=%d", err, calls.Load())
	}
}
