package usage

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func samplerOptions(random func() float64) QuotaCostSamplerOptions {
	return QuotaCostSamplerOptions{MinCostUSD: 20, MaxCostUSD: 40, Random: random, RetryBase: 5 * time.Millisecond, RetryMax: 20 * time.Millisecond, ProbeTimeout: time.Second}
}

func codexCost(authIndex, requestID string, cost float64) QuotaAuditUsage {
	return QuotaAuditUsage{Provider: "codex", AuthID: authIndex + "-id", AuthIndex: authIndex, RequestID: requestID, CostUSD: &cost}
}

func waitProbe(t *testing.T, calls <-chan QuotaProbeRequest, want string) QuotaProbeRequest {
	t.Helper()
	select {
	case request := <-calls:
		if request.AuthIndex != want {
			t.Fatalf("probe auth index = %q, want %q", request.AuthIndex, want)
		}
		return request
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for probe %q", want)
		return QuotaProbeRequest{}
	}
}

func TestQuotaCostSamplerAccumulatesPerAuthIndex(t *testing.T) {
	calls := make(chan QuotaProbeRequest, 4)
	sampler := NewQuotaCostSampler(func(_ context.Context, request QuotaProbeRequest) error { calls <- request; return nil }, samplerOptions(func() float64 { return 0 }))
	defer sampler.Close()
	sampler.Observe(context.Background(), codexCost("a", "a-1", 10))
	sampler.Observe(context.Background(), codexCost("b", "b-1", 20))
	waitProbe(t, calls, "b")
	select {
	case request := <-calls:
		t.Fatalf("unexpected independent-account probe for %q", request.AuthIndex)
	case <-time.After(30 * time.Millisecond):
	}
}

func TestQuotaCostSamplerFallsBackToAuthIDWithoutIndex(t *testing.T) {
	calls := make(chan QuotaProbeRequest, 1)
	sampler := NewQuotaCostSampler(func(_ context.Context, request QuotaProbeRequest) error { calls <- request; return nil }, samplerOptions(func() float64 { return 0 }))
	defer sampler.Close()
	cost := 20.0
	sampler.Observe(context.Background(), QuotaAuditUsage{Provider: "codex", AuthID: "auth-id", RequestID: "request-1", CostUSD: &cost})
	select {
	case request := <-calls:
		if request.AuthID != "auth-id" || request.AuthIndex != "" {
			t.Fatalf("fallback probe identity = %#v", request)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for fallback probe")
	}
}

func TestQuotaCostSamplerUsesNewRandomTargetAfterRound(t *testing.T) {
	var mu sync.Mutex
	values := []float64{0, 1}
	random := func() float64 {
		mu.Lock()
		defer mu.Unlock()
		value := values[0]
		if len(values) > 1 {
			values = values[1:]
		}
		return value
	}
	calls := make(chan QuotaProbeRequest, 4)
	sampler := NewQuotaCostSampler(func(_ context.Context, request QuotaProbeRequest) error { calls <- request; return nil }, samplerOptions(random))
	defer sampler.Close()
	sampler.Observe(context.Background(), codexCost("a", "a-1", 20))
	waitProbe(t, calls, "a")
	sampler.Observe(context.Background(), codexCost("a", "a-2", 39))
	select {
	case <-calls:
		t.Fatal("new target fired before threshold")
	case <-time.After(30 * time.Millisecond):
	}
	sampler.Observe(context.Background(), codexCost("a", "a-3", 1))
	waitProbe(t, calls, "a")
}

func TestQuotaCostSamplerDeduplicatesRequestID(t *testing.T) {
	calls := make(chan QuotaProbeRequest, 2)
	sampler := NewQuotaCostSampler(func(_ context.Context, request QuotaProbeRequest) error { calls <- request; return nil }, samplerOptions(func() float64 { return 0 }))
	defer sampler.Close()
	sampler.Observe(context.Background(), codexCost("a", "same", 20))
	waitProbe(t, calls, "a")
	sampler.Observe(context.Background(), codexCost("a", "same", 20))
	select {
	case <-calls:
		t.Fatal("duplicate request ID retriggered probe")
	case <-time.After(30 * time.Millisecond):
	}
}

func TestQuotaCostSamplerIgnoresUnpricedUsage(t *testing.T) {
	calls := make(chan QuotaProbeRequest, 1)
	sampler := NewQuotaCostSampler(func(_ context.Context, request QuotaProbeRequest) error { calls <- request; return nil }, samplerOptions(func() float64 { return 0 }))
	defer sampler.Close()
	sampler.Observe(context.Background(), QuotaAuditUsage{Provider: "codex", AuthIndex: "a", RequestID: "unpriced"})
	select {
	case <-calls:
		t.Fatal("unpriced usage triggered probe")
	case <-time.After(30 * time.Millisecond):
	}
}

func TestQuotaCostSamplerAllowsOnlyOneProbeInFlight(t *testing.T) {
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	var count atomic.Int32
	sampler := NewQuotaCostSampler(func(_ context.Context, request QuotaProbeRequest) error {
		count.Add(1)
		started <- struct{}{}
		<-release
		return nil
	}, samplerOptions(func() float64 { return 0 }))
	defer sampler.Close()
	sampler.Observe(context.Background(), codexCost("a", "a-1", 20))
	<-started
	for i := 0; i < 10; i++ {
		sampler.Observe(context.Background(), codexCost("a", fmt.Sprintf("a-extra-%d", i), 20))
	}
	time.Sleep(20 * time.Millisecond)
	if got := count.Load(); got != 1 {
		t.Fatalf("in-flight probes = %d, want 1", got)
	}
	close(release)
}

func TestQuotaCostSamplerRetriesFailureAndKeepsPendingCost(t *testing.T) {
	calls := make(chan QuotaProbeRequest, 4)
	var count atomic.Int32
	sampler := NewQuotaCostSampler(func(_ context.Context, request QuotaProbeRequest) error {
		calls <- request
		if count.Add(1) == 1 {
			return errors.New("temporary")
		}
		return nil
	}, samplerOptions(func() float64 { return 0 }))
	defer sampler.Close()
	sampler.Observe(context.Background(), codexCost("a", "a-1", 20))
	waitProbe(t, calls, "a")
	waitProbe(t, calls, "a")
	if got := count.Load(); got != 2 {
		t.Fatalf("probe attempts = %d, want 2", got)
	}
}

func TestQuotaCostSamplerPreservesCarryAfterSuccess(t *testing.T) {
	calls := make(chan QuotaProbeRequest, 4)
	sampler := NewQuotaCostSampler(func(_ context.Context, request QuotaProbeRequest) error { calls <- request; return nil }, samplerOptions(func() float64 { return 0 }))
	defer sampler.Close()
	sampler.Observe(context.Background(), codexCost("a", "a-1", 35))
	waitProbe(t, calls, "a")
	sampler.Observe(context.Background(), codexCost("a", "a-2", 5))
	waitProbe(t, calls, "a")
}
