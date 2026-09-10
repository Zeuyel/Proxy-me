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

func TestCCHPriceSyncAddsLatestDatedBaseModelAliases(t *testing.T) {
	store := NewQuotaAuditStore()
	models := `{"version":"1","models":[
		{"id":"gpt-5.4-2025-08-07","input":1,"output":2},
		{"id":"gpt-5.4-2026-02-03","input":3,"output":4},
		{"id":"gpt-5.4-fast-2026-12-01","input":90,"output":91},
		{"id":"gpt-5.4-2026-12-01-high","input":92,"output":93},
		{"id":"gpt-5.5-2025-11-01","input":5,"output":6},
		{"id":"gpt-5.5-2026-01-15","input":7,"output":8},
		{"id":"gpt-5.5-mini-2026-12-01","input":94,"output":95}
	]}`
	if _, err := newCCHTestSync(t, store, models, http.StatusOK).Sync(context.Background()); err != nil {
		t.Fatalf("sync: %v", err)
	}
	exported := store.Export().PriceSnapshots
	for _, test := range []struct {
		base  string
		dated string
		input float64
	}{
		{base: "gpt-5.4", dated: "gpt-5.4-2026-02-03", input: 3},
		{base: "gpt-5.5", dated: "gpt-5.5-2026-01-15", input: 7},
	} {
		alias, ok := exported[test.base]
		if !ok || alias.InputPerMillionUSD == nil || *alias.InputPerMillionUSD != test.input {
			t.Fatalf("alias %s = %#v", test.base, alias)
		}
		dated := exported[test.dated]
		if alias.Source != dated.Source || alias.Version != dated.Version || alias.OutputPerMillionUSD == nil || dated.OutputPerMillionUSD == nil || *alias.OutputPerMillionUSD != *dated.OutputPerMillionUSD {
			t.Fatalf("alias %s did not preserve the dated price: %#v vs %#v", test.base, alias, dated)
		}
		if alias.Fingerprint != priceModelFingerprint(test.base, alias) || alias.Fingerprint == dated.Fingerprint {
			t.Fatalf("alias %s fingerprint = %q", test.base, alias.Fingerprint)
		}
	}
}

func TestCCHPriceSyncBaseModelAliasesRequireExactDateOnly(t *testing.T) {
	store := NewQuotaAuditStore()
	models := `{"version":"1","models":[
		{"id":"gpt-5.4-2026-02-03","input":3,"output":4},
		{"id":"gpt-5.5-fast-2026-12-01","input":90,"output":91},
		{"id":"gpt-5.5-2026-02-03-mini","input":92,"output":93},
		{"id":"gpt-5.5-pro-2026-12-01","input":94,"output":95},
		{"id":"gpt-5.5-low-2026-12-01","input":96,"output":97},
		{"id":"gpt-5.5-nano-2026-12-01","input":98,"output":99},
		{"id":"gpt-5.5-2026-12-01-batch","input":100,"output":101},
		{"id":"gpt-5.5-flex-2026-12-01","input":102,"output":103},
		{"id":"gpt-5.5-2026-12-01-us","input":104,"output":105},
		{"id":"gpt-5.5-2026-02-31","input":106,"output":107}
	]}`
	if _, err := newCCHTestSync(t, store, models, http.StatusOK).Sync(context.Background()); err != nil {
		t.Fatalf("sync: %v", err)
	}
	exported := store.Export().PriceSnapshots
	if alias, ok := exported["gpt-5.4"]; !ok || alias.InputPerMillionUSD == nil || *alias.InputPerMillionUSD != 3 {
		t.Fatalf("single dated alias = %#v", alias)
	}
	if _, ok := exported["gpt-5.5"]; ok {
		t.Fatalf("variant-only gpt-5.5 alias = %#v", exported["gpt-5.5"])
	}
}

func TestCCHPriceSyncDoesNotOverrideExplicitBaseModelPrice(t *testing.T) {
	store := NewQuotaAuditStore()
	models := `{"version":"1","models":[
		{"id":"gpt-5.4","input":77,"output":78},
		{"id":"gpt-5.4-2026-02-03","input":3,"output":4},
		{"id":"gpt-5.5-2026-01-15","input":7,"output":8}
	]}`
	manual := 99.0
	store.SetManualPriceSnapshot("gpt-5.5", PriceSnapshot{InputPerMillionUSD: &manual, OutputPerMillionUSD: &manual, Version: "manual"})
	if _, err := newCCHTestSync(t, store, models, http.StatusOK).Sync(context.Background()); err != nil {
		t.Fatalf("sync: %v", err)
	}
	exported := store.Export().PriceSnapshots
	if price := exported["gpt-5.4"]; price.InputPerMillionUSD == nil || *price.InputPerMillionUSD != 77 {
		t.Fatalf("explicit gpt-5.4 price was replaced: %#v", price)
	}
	if price := exported["gpt-5.5"]; price.InputPerMillionUSD == nil || *price.InputPerMillionUSD != manual {
		t.Fatalf("manual gpt-5.5 price was replaced: %#v", price)
	}
}

func TestCCHPriceSyncBaseAliasRepricesHistoricalUsage(t *testing.T) {
	store := NewQuotaAuditStore()
	t0 := time.Date(2026, 8, 20, 4, 0, 0, 0, time.UTC)
	store.RecordQuotaSnapshot("auth", "auth-index", "", t0, []byte(`{"rate_limit":{"primary_window":{"used_percent":0}}}`))
	store.CaptureUsage(Record{Provider: "codex", Model: "gpt-5.4", AuthID: "auth", AuthIndex: "auth-index", RequestedAt: t0.Add(time.Minute), Detail: Detail{InputTokens: 1_000_000, OutputTokens: 1_000_000, TotalTokens: 2_000_000}})
	if usage := store.Export().Usage[0]; usage.CostUSD != nil {
		t.Fatalf("usage was priced before sync: %#v", usage)
	}
	if _, err := newCCHTestSync(t, store, `{"version":"1","models":[{"id":"gpt-5.4-2026-02-03","input":3,"output":4}]}`, http.StatusOK).Sync(context.Background()); err != nil {
		t.Fatalf("sync: %v", err)
	}
	t1 := t0.Add(2 * time.Minute)
	store.RecordQuotaSnapshot("auth", "auth-index", "", t1, []byte(`{"rate_limit":{"primary_window":{"used_percent":1}}}`))
	rows := store.Build(QuotaAuditQuery{}, t1.Add(time.Minute)).Rows
	for _, row := range rows {
		if row.Model == "gpt-5.4" && row.CostDeltaUSD != nil {
			if *row.CostDeltaUSD != 7 || row.CostStatus != "priced" {
				t.Fatalf("historical alias cost = %#v", row)
			}
			return
		}
	}
	t.Fatalf("missing repriced historical usage rows: %#v", rows)
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
