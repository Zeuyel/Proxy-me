package usage

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type cchRoundTripper func(*http.Request) (*http.Response, error)

func (f cchRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func cchResponse(req *http.Request, status int, body string, headers http.Header) *http.Response {
	if headers == nil {
		headers = make(http.Header)
	}
	return &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Header:     headers,
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}
}

const cchSchema = `{"$id":"https://cch-plus.com/pricing/cchp.pricing-table/v1","type":"object","properties":{"version":{"type":"string"},"models":{"type":"array"}}}`

const cchModels = `{"version":"cchp-2026-08-20-abc123","models":[{"slug":"openai/GPT-5","model_name":"GPT-5","pricing":[{"provider":"other","charges":{"prompt":{"unit":"per_M_tokens","price":"9"},"completion":{"unit":"per_M_tokens","price":"90"}}},{"provider":"official","charges":{"prompt":{"unit":"per_M_tokens","price":"1.25"},"completion":{"unit":"per_M_tokens","price":"10"},"reasoning":{"unit":"per_M_tokens","price":"20"},"cache_read":{"unit":"per_M_tokens","price":"0.25"}}}]},{"slug":"bad","pricing":[{"provider":"official","charges":{"prompt":{"unit":"per_M_tokens","price":"-1"},"completion":{"unit":"per_M_tokens","price":"2"}}}]},{"slug":"missing-output","pricing":[{"provider":"official","charges":{"prompt":{"unit":"per_M_tokens","price":"1"}}}]}]}`

func newCCHTestSync(t *testing.T, store *QuotaAuditStore, models string, modelStatus int) *CCHPriceSync {
	t.Helper()
	client := &http.Client{Transport: cchRoundTripper(func(req *http.Request) (*http.Response, error) {
		switch req.URL.String() {
		case CCHPlusSchemaURL:
			return cchResponse(req, http.StatusOK, cchSchema, nil), nil
		case CCHPlusModelsURL:
			return cchResponse(req, modelStatus, models, http.Header{"ETag": []string{"\"v1\""}}), nil
		default:
			return nil, errors.New("unexpected URL: " + req.URL.String())
		}
	})}
	return NewCCHPriceSyncWithClient(store, client)
}

func TestCCHPriceSyncSuccessAndValidation(t *testing.T) {
	store := NewQuotaAuditStore()
	syncer := newCCHTestSync(t, store, cchModels, http.StatusOK)
	result, err := syncer.Sync(context.Background())
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if result.Updated != 1 || result.Failed != 2 || result.Version != "cchp-2026-08-20-abc123" || result.Fingerprint == "" {
		t.Fatalf("unexpected result: %+v", result)
	}
	price, ok := store.Export().PriceSnapshots["gpt-5"]
	if !ok || price.InputPerMillionUSD == nil || *price.InputPerMillionUSD != 1.25 || price.Source != cchPlusSource || price.Unit != priceSnapshotUnit || !price.Immutable {
		t.Fatalf("unexpected price snapshot: %+v", price)
	}
}

func TestCCHPriceSyncTimeoutAndNon2xxKeepOldPrice(t *testing.T) {
	store := NewQuotaAuditStore()
	old := 9.0
	store.SetSyncedPriceSnapshot("gpt-5", PriceSnapshot{InputPerMillionUSD: &old, OutputPerMillionUSD: &old, Version: "1"})
	timeoutClient := &http.Client{Transport: cchRoundTripper(func(req *http.Request) (*http.Response, error) {
		return nil, context.DeadlineExceeded
	})}
	result, err := NewCCHPriceSyncWithClient(store, timeoutClient).Sync(context.Background())
	if err == nil || result.Failed == 0 {
		t.Fatalf("expected timeout failure: %+v, %v", result, err)
	}
	if current, ok, _ := store.priceSnapshot("gpt-5"); !ok || current.InputPerMillionUSD == nil || *current.InputPerMillionUSD != old {
		t.Fatal("timeout changed old price")
	}

	result, err = newCCHTestSync(t, store, cchModels, http.StatusBadGateway).Sync(context.Background())
	if err == nil || result.Failed == 0 {
		t.Fatalf("expected status failure: %+v, %v", result, err)
	}
	if current, ok, _ := store.priceSnapshot("gpt-5"); !ok || current.InputPerMillionUSD == nil || *current.InputPerMillionUSD != old {
		t.Fatal("non-2xx changed old price")
	}
}

func TestCCHPriceSyncRejectsRedirectAndMalformedJSON(t *testing.T) {
	redirectClient := &http.Client{Transport: cchRoundTripper(func(req *http.Request) (*http.Response, error) {
		return cchResponse(req, http.StatusFound, "", http.Header{"Location": []string{"https://evil.example/pricing/v1/models.json"}}), nil
	})}
	result, err := NewCCHPriceSyncWithClient(NewQuotaAuditStore(), redirectClient).Sync(context.Background())
	if err == nil || result.Failed == 0 || !strings.Contains(err.Error(), "redirect") {
		t.Fatalf("expected redirect failure: %+v, %v", result, err)
	}

	malformed := newCCHTestSync(t, NewQuotaAuditStore(), `{"version":"1",`, http.StatusOK)
	result, err = malformed.Sync(context.Background())
	if err == nil || result.Failed == 0 || !strings.Contains(err.Error(), "invalid pricing json") {
		t.Fatalf("expected json failure: %+v, %v", result, err)
	}
}

func TestCCHPriceSyncUnchangedAndManualOverride(t *testing.T) {
	store := NewQuotaAuditStore()
	syncer := newCCHTestSync(t, store, `{"version":"1","models":[{"id":"gpt-5","input":1,"output":2}]}`, http.StatusOK)
	first, err := syncer.Sync(context.Background())
	if err != nil || first.Updated != 1 {
		t.Fatalf("first sync: %+v, %v", first, err)
	}
	second, err := syncer.Sync(context.Background())
	if err != nil || second.Updated != 0 || second.Unchanged != 1 || second.Fingerprint != first.Fingerprint {
		t.Fatalf("second sync: %+v, %v", second, err)
	}

	manual := 99.0
	store.SetManualPriceSnapshot("gpt-5", PriceSnapshot{InputPerMillionUSD: &manual, OutputPerMillionUSD: &manual, Version: "manual"})
	third, err := newCCHTestSync(t, store, `{"version":"1","models":[{"id":"gpt-5","input":3,"output":4}]}`, http.StatusOK).Sync(context.Background())
	if err != nil || third.Updated != 0 || third.Unchanged != 1 {
		t.Fatalf("manual sync: %+v, %v", third, err)
	}
	if current, _, manualOwned := store.priceSnapshot("gpt-5"); !manualOwned || current.InputPerMillionUSD == nil || *current.InputPerMillionUSD != manual {
		t.Fatal("remote sync replaced manual price")
	}
}

func TestCCHPriceSyncFailureAndUsageSnapshotFallback(t *testing.T) {
	store := NewQuotaAuditStore()
	syncer := newCCHTestSync(t, store, `{"version":"1","models":[{"id":"gpt-5","input":1,"output":2}]}`, http.StatusOK)
	if _, err := syncer.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC()
	store.CaptureUsage(Record{Provider: "codex", Model: "gpt-5", RequestedAt: at, Detail: Detail{InputTokens: 1_000_000, OutputTokens: 1_000_000}})
	oldUsage := store.Export().Usage[0]
	if oldUsage.PriceSnapshot == nil || oldUsage.PriceSnapshot.InputPerMillionUSD == nil || *oldUsage.PriceSnapshot.InputPerMillionUSD != 1 {
		t.Fatalf("missing old snapshot: %+v", oldUsage)
	}

	failed := newCCHTestSync(t, store, `{"version":"1",`, http.StatusOK)
	if _, err := failed.Sync(context.Background()); err == nil {
		t.Fatal("expected refresh failure")
	}
	current, _, _ := store.priceSnapshot("gpt-5")
	if current.InputPerMillionUSD == nil || *current.InputPerMillionUSD != 1 {
		t.Fatal("failed refresh changed old price")
	}
	newPrice := 5.0
	store.SetSyncedPriceSnapshot("gpt-5", PriceSnapshot{InputPerMillionUSD: &newPrice, OutputPerMillionUSD: &newPrice, Version: "1"})
	if oldUsage.PriceSnapshot == nil || *oldUsage.PriceSnapshot.InputPerMillionUSD != 1 {
		t.Fatal("historical usage snapshot was mutated")
	}
}

func TestCCHPriceModelNormalizationPrefersModelNameAndStripsProviderPrefix(t *testing.T) {
	version, prices, failed, err := parseCPTPrices([]byte(`{"version":"sha256:abc","models":[{"model_name":"GPT-5.3-Codex","slug":"openai/ignored","pricing":[{"provider":"official","charges":{"prompt":{"unit":"per_M_tokens","price":"1"},"completion":{"unit":"per_M_tokens","price":"2"}}}]},{"slug":"openai/codex-mini","pricing":[{"provider":"official","charges":{"prompt":{"unit":"per_M_tokens","price":"1"},"completion":{"unit":"per_M_tokens","price":"2"}}}]}]}`), "v1")
	if err != nil || failed != 0 || version != "sha256:abc" {
		t.Fatalf("unexpected parse result: version=%q failed=%d err=%v", version, failed, err)
	}
	if _, ok := prices["gpt-5.3-codex"]; !ok {
		t.Fatalf("model_name should take precedence: %#v", prices)
	}
	if _, ok := prices["codex-mini"]; !ok {
		t.Fatalf("provider prefix should be stripped: %#v", prices)
	}
}
