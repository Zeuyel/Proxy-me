package usage

import (
	"math"
	"strconv"
	"testing"
	"time"
)

func quotaFloat(value float64) *float64 { return &value }

func quotaPrice(input, output float64) PriceSnapshot {
	return PriceSnapshot{
		InputPerMillionUSD: quotaFloat(input), OutputPerMillionUSD: quotaFloat(output),
		Currency: "USD", Unit: "usd_per_million_tokens", Source: "test", Version: "1", Immutable: true,
	}
}

func quotaPayload(primary, secondary float64, resetAt string) []byte {
	return []byte(`{"plan_type":"pro","rate_limit":{"primary_window":{"used_percent":` +
		formatQuotaFloat(primary) + `,"remaining_percent":` + formatQuotaFloat(100-primary) + `,"limit_window_seconds":18000,"reset_at":` + resetAt + `},"secondary_window":{"used_percent":` +
		formatQuotaFloat(secondary) + `,"limit_window_seconds":604800,"reset_at":` + resetAt + `}},"code_review_rate_limit":{"primary_window":{"used_percent":12,"reset_after_seconds":3600}}}`)
}

func formatQuotaFloat(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}

func TestQuotaAuditBuildsWindowsAndCorrelatesCost(t *testing.T) {
	store := NewQuotaAuditStore()
	t0 := time.Date(2026, 8, 20, 1, 0, 0, 0, time.UTC)
	t1 := t0.Add(10 * time.Minute)
	store.SetPriceSnapshot("gpt-5.6-codex", quotaPrice(2, 8))
	if got := store.RecordQuotaSnapshot("auth-1", "idx-1", "a***@example.com", t0, quotaPayload(10, 20, "1730000000")); got != 3 {
		t.Fatalf("expected three windows, got %d", got)
	}
	store.CaptureUsage(Record{Provider: "codex", Model: "gpt-5.6-codex", AuthID: "auth-1", AuthIndex: "idx-1", RequestedAt: t1, SessionID: "sess-1", Detail: Detail{InputTokens: 1000, OutputTokens: 500, TotalTokens: 1500}})
	if got := store.RecordQuotaSnapshot("auth-1", "idx-1", "a***@example.com", t1, quotaPayload(25, 31, "1730000000")); got != 3 {
		t.Fatalf("expected three windows at second sample, got %d", got)
	}
	response := store.Build(QuotaAuditQuery{}, t1.Add(time.Minute))
	if len(response.Rows) != 6 {
		t.Fatalf("expected six rows, got %d", len(response.Rows))
	}
	var primary QuotaAuditRow
	for _, row := range response.Rows {
		if row.Window == "primary" && row.Timestamp.Equal(t1) {
			primary = row
		}
	}
	if primary.QuotaDeltaPercent == nil || *primary.QuotaDeltaPercent != 15 {
		t.Fatalf("expected primary quota delta 15, got %#v", primary.QuotaDeltaPercent)
	}
	if primary.Tokens.Total != 1500 || primary.CostStatus != "priced" || primary.CostDeltaUSD == nil {
		t.Fatalf("expected correlated priced usage, got %#v", primary)
	}
	if len(primary.SessionIDs) != 1 || primary.SessionIDs[0] != "sess-1" {
		t.Fatalf("expected session id, got %#v", primary.SessionIDs)
	}
	if response.Summary.TotalTokens != 1500 {
		t.Fatalf("multi-window summary double counted usage: %d", response.Summary.TotalTokens)
	}
}

func TestQuotaAuditResetNegativeAndDuplicate(t *testing.T) {
	store := NewQuotaAuditStore()
	t0 := time.Date(2026, 8, 20, 2, 0, 0, 0, time.UTC)
	if got := store.RecordQuotaSnapshot("a", "i", "", t0, []byte(`{"rate_limit":{"primary_window":{"used_percent":50,"reset_at":1730000000}}}`)); got != 1 {
		t.Fatal(got)
	}
	if got := store.RecordQuotaSnapshot("a", "i", "", t0, []byte(`{"rate_limit":{"primary_window":{"used_percent":50,"reset_at":1730000000}}}`)); got != 0 {
		t.Fatalf("identical snapshot should deduplicate, got %d", got)
	}
	if got := store.RecordQuotaSnapshot("a", "i", "", t0, []byte(`{"rate_limit":{"primary_window":{"used_percent":51,"reset_at":1730000000}}}`)); got != 1 {
		t.Fatalf("changed same-time snapshot should be retained, got %d", got)
	}
	t1 := t0.Add(time.Minute)
	store.RecordQuotaSnapshot("a", "i", "", t1, []byte(`{"rate_limit":{"primary_window":{"used_percent":20,"reset_at":1731000000}}}`))
	rows := store.Build(QuotaAuditQuery{}, t1).Rows
	if len(rows) != 3 || rows[2].Status != "reset" || rows[2].QuotaDeltaPercent != nil {
		t.Fatalf("expected reset delta null, got %#v", rows)
	}

	negative := NewQuotaAuditStore()
	negative.RecordQuotaSnapshot("a", "i", "", t0, []byte(`{"rate_limit":{"primary_window":{"used_percent":50}}}`))
	negative.RecordQuotaSnapshot("a", "i", "", t1, []byte(`{"rate_limit":{"primary_window":{"used_percent":20}}}`))
	negativeRows := negative.Build(QuotaAuditQuery{}, t1).Rows
	if negativeRows[1].Status != "negative" || negativeRows[1].Reset || negativeRows[1].QuotaDeltaPercent != nil {
		t.Fatalf("expected negative non-reset row, got %#v", negativeRows[1])
	}
}

func TestQuotaAuditMissingPriceTokenFiltersAndExport(t *testing.T) {
	store := NewQuotaAuditStore()
	t0 := time.Date(2026, 8, 20, 3, 0, 0, 0, time.UTC)
	store.RecordQuotaSnapshot("a", "i", "", t0, []byte(`{"plan_type":"free","rate_limit":{"primary_window":{"used_percent":10}}}`))
	response := store.Build(QuotaAuditQuery{Window: "primary", Auth: "a"}, t0)
	if len(response.Rows) != 1 || response.Rows[0].CostDeltaUSD != nil || response.Rows[0].CostStatus != "unpriced" || response.Rows[0].Reason == "" {
		t.Fatalf("expected explicit missing price/token state, got %#v", response.Rows)
	}
	exported := store.Export()
	other := NewQuotaAuditStore()
	addedSnapshots, _ := other.Merge(exported)
	if addedSnapshots != 1 || len(other.Build(QuotaAuditQuery{}, t0).Rows) != 1 {
		t.Fatalf("export/import did not preserve audit snapshots")
	}
}

func TestQuotaAuditPriceSnapshotIsCapturedPerUsage(t *testing.T) {
	store := NewQuotaAuditStore()
	t0 := time.Date(2026, 8, 20, 4, 0, 0, 0, time.UTC)
	store.RecordQuotaSnapshot("a", "", "", t0, []byte(`{"rate_limit":{"primary_window":{"used_percent":0}}}`))
	store.SetPriceSnapshot("model", quotaPrice(1, 1))
	t1 := t0.Add(time.Minute)
	store.CaptureUsage(Record{Provider: "codex", Model: "model", AuthID: "a", RequestedAt: t1, Detail: Detail{InputTokens: 1000000, TotalTokens: 1000000}})
	store.RecordQuotaSnapshot("a", "", "", t1, []byte(`{"rate_limit":{"primary_window":{"used_percent":1}}}`))
	store.SetPriceSnapshot("model", quotaPrice(9, 9))
	t2 := t1.Add(time.Minute)
	store.CaptureUsage(Record{Provider: "codex", Model: "model", AuthID: "a", RequestedAt: t2, Detail: Detail{InputTokens: 1000000, TotalTokens: 1000000}})
	store.RecordQuotaSnapshot("a", "", "", t2, []byte(`{"rate_limit":{"primary_window":{"used_percent":3}}}`))
	rows := store.Build(QuotaAuditQuery{}, t0.Add(3*time.Minute)).Rows
	if len(rows) != 3 || rows[1].CostDeltaUSD == nil || *rows[1].CostDeltaUSD != 1 || rows[2].CostDeltaUSD == nil || *rows[2].CostDeltaUSD != 9 {
		t.Fatalf("expected immutable per-event pricing, got %#v", rows)
	}
}

func TestQuotaAuditRecalculatesUsageAfterPriceSync(t *testing.T) {
	store := NewQuotaAuditStore()
	t0 := time.Date(2026, 8, 20, 4, 30, 0, 0, time.UTC)
	store.RecordQuotaSnapshot("a", "", "", t0, []byte(`{"rate_limit":{"primary_window":{"used_percent":0}}}`))
	t1 := t0.Add(time.Minute)
	store.CaptureUsage(Record{Provider: "codex", Model: "late-model", AuthID: "a", RequestedAt: t1, Detail: Detail{InputTokens: 1_000_000, OutputTokens: 500_000, TotalTokens: 1_500_000}})
	store.SetPriceSnapshot("late-model", quotaPrice(2, 8))
	store.RecordQuotaSnapshot("a", "", "", t1, []byte(`{"rate_limit":{"primary_window":{"used_percent":5}}}`))

	rows := store.Build(QuotaAuditQuery{}, t1.Add(time.Minute)).Rows
	if len(rows) != 2 {
		t.Fatalf("expected two rows, got %d", len(rows))
	}
	row := rows[1]
	if row.CostStatus != "priced" || row.CostDeltaUSD == nil || *row.CostDeltaUSD != 6 || row.PriceSnapshot == nil {
		t.Fatalf("expected historical usage to be priced after sync, got %#v", row)
	}
}

func TestQuotaAuditCostTreatsReasoningAndCachedAsSubsets(t *testing.T) {
	tokens := QuotaAuditTokens{Input: 1_000_000, Output: 1_000_000, Reasoning: 200_000, Cached: 100_000, Total: 2_000_000}
	price := quotaPrice(1, 2)
	price.ReasoningPerMillionUSD = quotaFloat(5)
	price.CachedPerMillionUSD = quotaFloat(0.5)
	cost, ok := calculateCost(tokens, price)
	if !ok || math.Abs(cost-3.55) > 1e-9 {
		t.Fatalf("expected input-cached + non-reasoning output + reasoning + cached cost 3.55, got %v (ok=%t)", cost, ok)
	}
}

func TestQuotaAuditParsesAliasesAndRejectsInvalidNumbers(t *testing.T) {
	store := NewQuotaAuditStore()
	observedAt := time.Date(2026, 8, 20, 5, 0, 0, 0, time.UTC)
	payload := []byte(`{"rateLimit":{"primaryWindow":{"usedPercent":"NaN","remainingFraction":0.75,"resetAt":"2026-08-20T06:00:00.123Z"}},"additionalRateLimits":[{"secondaryWindow":{"usedPercent":25}}]}`)
	if got := store.RecordQuotaSnapshot("auth", "index", "", observedAt, payload); got != 2 {
		t.Fatalf("expected two aliased windows, got %d", got)
	}
	rows := store.Build(QuotaAuditQuery{}, observedAt).Rows
	if rows[0].UsedPercent != nil || rows[0].RemainingPercent == nil || *rows[0].RemainingPercent != 75 {
		t.Fatalf("expected invalid used and valid remaining fraction, got %#v", rows[0])
	}
	if rows[0].ResetAt == nil || !rows[0].ResetAt.Equal(time.Date(2026, 8, 20, 6, 0, 0, 123000000, time.UTC)) {
		t.Fatalf("expected RFC3339 reset timestamp, got %v", rows[0].ResetAt)
	}
}

func TestQuotaAuditPriceSnapshotCopiesPointerRates(t *testing.T) {
	store := NewQuotaAuditStore()
	input := 1.0
	output := 2.0
	price := PriceSnapshot{InputPerMillionUSD: &input, OutputPerMillionUSD: &output}
	store.SetPriceSnapshot("model", price)
	input = 9
	output = 9
	exported := store.Export()
	if exported.PriceSnapshots["model"].InputPerMillionUSD == nil || *exported.PriceSnapshots["model"].InputPerMillionUSD != 1 {
		t.Fatalf("price snapshot was mutated through caller pointer: %#v", exported.PriceSnapshots["model"])
	}
}

func TestQuotaAuditAccountsFollowAuthFiles(t *testing.T) {
	store := NewQuotaAuditStore()
	store.SyncAccounts([]QuotaAuditAccount{
		{AuthID: "old-auth.json", AuthIndex: "old-index", Account: "a***@example.com", Provider: "codex"},
		{AuthID: "new-auth.json", AuthIndex: "new-index", Account: "a***@example.com", Provider: "codex"},
	})

	response := store.Build(QuotaAuditQuery{}, time.Now().UTC())
	if len(response.Accounts) != 2 || response.Summary.Accounts != 2 {
		t.Fatalf("expected current auth roster in audit response, got accounts=%#v summary=%d", response.Accounts, response.Summary.Accounts)
	}
	response = store.Build(QuotaAuditQuery{Auth: "new-index"}, time.Now().UTC())
	if len(response.Accounts) != 1 || response.Accounts[0].AuthID != "new-auth.json" {
		t.Fatalf("expected auth identity filter to isolate colliding labels, got %#v", response.Accounts)
	}

	store.SyncAccounts([]QuotaAuditAccount{
		{AuthID: "new-auth.json", AuthIndex: "new-index", Account: "n***@example.com", Provider: "codex"},
	})
	response = store.Build(QuotaAuditQuery{Account: "n***@example.com"}, time.Now().UTC())
	if len(response.Accounts) != 1 || response.Accounts[0].AuthID != "new-auth.json" {
		t.Fatalf("expected deleted auth to disappear from roster, got %#v", response.Accounts)
	}
}

func TestQuotaAuditUsesStableAuthIndexAcrossLegacySnapshots(t *testing.T) {
	store := NewQuotaAuditStore()
	store.SyncAccounts([]QuotaAuditAccount{
		{AuthID: "current-auth", AuthIndex: "stable-index", Account: "a***@example.com", Provider: "codex"},
	})
	t0 := time.Date(2026, 8, 20, 6, 0, 0, 0, time.UTC)
	t1 := t0.Add(time.Minute)
	store.RecordQuotaSnapshot("legacy-auth", "", "a***@example.com", t0, []byte(`{"rate_limit":{"primary_window":{"used_percent":10}}}`))
	store.RecordQuotaSnapshot("current-auth", "stable-index", "a***@example.com", t1, []byte(`{"rate_limit":{"primary_window":{"used_percent":20}}}`))

	response := store.Build(QuotaAuditQuery{Auth: "stable-index"}, t1)
	if len(response.Rows) != 2 || response.Summary.Accounts != 1 {
		t.Fatalf("expected legacy and current snapshots under one account, got rows=%#v summary=%#v", response.Rows, response.Summary)
	}
	if response.Rows[0].Auth != "stable-index" || response.Rows[1].Auth != "stable-index" {
		t.Fatalf("expected stable auth index in rows, got %#v", response.Rows)
	}
	if response.Rows[1].QuotaDeltaPercent == nil || *response.Rows[1].QuotaDeltaPercent != 10 {
		t.Fatalf("expected delta across auth aliases, got %#v", response.Rows[1].QuotaDeltaPercent)
	}

	response = store.Build(QuotaAuditQuery{Auth: "current-auth"}, t1)
	if len(response.Rows) != 2 || response.Accounts[0].AuthIndex != "stable-index" {
		t.Fatalf("expected auth id alias to select canonical account, got %#v", response)
	}
}

func TestQuotaAuditAuthIndexSurvivesAuthIDChanges(t *testing.T) {
	store := NewQuotaAuditStore()
	store.SyncAccounts([]QuotaAuditAccount{
		{AuthID: "current-auth", AuthIndex: "stable-index", Account: "a***@example.com", Provider: "codex"},
	})
	t0 := time.Date(2026, 8, 20, 7, 0, 0, 0, time.UTC)
	t1 := t0.Add(time.Minute)
	store.RecordQuotaSnapshot("previous-auth", "stable-index", "a***@example.com", t0, []byte(`{"rate_limit":{"primary_window":{"used_percent":10}}}`))
	store.CaptureUsage(Record{Provider: "codex", Model: "gpt-5.6-codex", AuthID: "previous-auth", AuthIndex: "stable-index", RequestedAt: t0.Add(30 * time.Second), Detail: Detail{InputTokens: 1000, OutputTokens: 500, TotalTokens: 1500}})
	store.RecordQuotaSnapshot("current-auth", "stable-index", "a***@example.com", t1, []byte(`{"rate_limit":{"primary_window":{"used_percent":20}}}`))

	response := store.Build(QuotaAuditQuery{AuthIndex: "stable-index"}, t1)
	if len(response.Rows) != 2 || response.Summary.Accounts != 1 {
		t.Fatalf("expected one auth-index group, got rows=%#v summary=%#v", response.Rows, response.Summary)
	}
	if response.Rows[0].Auth != "stable-index" || response.Rows[1].Auth != "stable-index" {
		t.Fatalf("expected canonical auth index in both rows, got %#v", response.Rows)
	}
	if response.Rows[1].Tokens.Total != 1500 {
		t.Fatalf("expected usage matched by auth index, got %#v", response.Rows[1].Tokens)
	}
}
