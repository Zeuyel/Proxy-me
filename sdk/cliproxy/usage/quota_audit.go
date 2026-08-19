package usage

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tidwall/gjson"
)

// PriceSnapshot is the immutable price input used to calculate an audit cost.
// Values are USD per one million tokens. A nil value means that component is
// unavailable and prevents a cost calculation when that component is present.
type PriceSnapshot struct {
	InputPerMillionUSD     *float64  `json:"input_per_million_usd,omitempty"`
	OutputPerMillionUSD    *float64  `json:"output_per_million_usd,omitempty"`
	ReasoningPerMillionUSD *float64  `json:"reasoning_per_million_usd,omitempty"`
	CachedPerMillionUSD    *float64  `json:"cached_per_million_usd,omitempty"`
	Currency               string    `json:"currency,omitempty"`
	CapturedAt             time.Time `json:"captured_at,omitempty"`
	Source                 string    `json:"source,omitempty"`
	Version                string    `json:"version,omitempty"`
	Fingerprint            string    `json:"fingerprint,omitempty"`
	Unit                   string    `json:"unit,omitempty"`
	Immutable              bool      `json:"immutable,omitempty"`
}

type QuotaWindowSnapshot struct {
	SnapshotID            string    `json:"snapshot_id"`
	AuthID                string    `json:"auth_id,omitempty"`
	AuthIndex             string    `json:"auth_index,omitempty"`
	Account               string    `json:"account,omitempty"`
	PlanType              string    `json:"plan_type,omitempty"`
	Window                string    `json:"window"`
	ObservedAt            time.Time `json:"observed_at"`
	CapturedAt            time.Time `json:"captured_at"`
	UsedPercent           *float64  `json:"used_percent,omitempty"`
	RemainingPercent      *float64  `json:"remaining_percent,omitempty"`
	WindowDurationSeconds *float64  `json:"window_duration_seconds,omitempty"`
	ResetAt               time.Time `json:"reset_at,omitempty"`
	Shape                 string    `json:"shape,omitempty"`
	Version               string    `json:"version,omitempty"`
	sequence              int64
}

type QuotaAuditUsage struct {
	Provider        string         `json:"provider,omitempty"`
	Model           string         `json:"model,omitempty"`
	AuthID          string         `json:"auth_id,omitempty"`
	AuthIndex       string         `json:"auth_index,omitempty"`
	SessionID       string         `json:"session_id,omitempty"`
	RequestID       string         `json:"request_id,omitempty"`
	Timestamp       time.Time      `json:"timestamp"`
	InputTokens     int64          `json:"input"`
	OutputTokens    int64          `json:"output"`
	ReasoningTokens int64          `json:"reasoning"`
	CachedTokens    int64          `json:"cached"`
	TotalTokens     int64          `json:"total"`
	Failed          bool           `json:"failed,omitempty"`
	CostUSD         *float64       `json:"cost_usd,omitempty"`
	PriceSnapshot   *PriceSnapshot `json:"price_snapshot,omitempty"`
}

type QuotaAuditExport struct {
	Snapshots      []QuotaWindowSnapshot    `json:"snapshots"`
	Usage          []QuotaAuditUsage        `json:"usage"`
	PriceSnapshots map[string]PriceSnapshot `json:"price_snapshots,omitempty"`
}

type QuotaAuditQuery struct {
	Auth    string
	Account string
	Window  string
	Model   string
	From    time.Time
	To      time.Time
}

type QuotaAuditTokens struct {
	Input     int64 `json:"input"`
	Output    int64 `json:"output"`
	Reasoning int64 `json:"reasoning"`
	Cached    int64 `json:"cached"`
	Total     int64 `json:"total"`
}

type QuotaAuditRow struct {
	SnapshotID          string           `json:"snapshot_id"`
	Auth                string           `json:"auth"`
	Account             string           `json:"account,omitempty"`
	Window              string           `json:"window"`
	PlanType            string           `json:"plan_type,omitempty"`
	Model               string           `json:"model,omitempty"`
	SessionIDs          []string         `json:"session_ids"`
	ThreadIDs           []string         `json:"thread_ids"`
	Timestamp           time.Time        `json:"timestamp"`
	UsedPercent         *float64         `json:"used_percent"`
	RemainingPercent    *float64         `json:"remaining_percent,omitempty"`
	QuotaDeltaPercent   *float64         `json:"quota_delta_percent"`
	Tokens              QuotaAuditTokens `json:"tokens"`
	CostDeltaUSD        *float64         `json:"cost_delta_usd"`
	CostPerQuotaPercent *float64         `json:"cost_per_quota_percent"`
	CostStatus          string           `json:"cost_status"`
	Status              string           `json:"status"`
	Reset               bool             `json:"reset"`
	ResetAt             *time.Time       `json:"reset_at"`
	Stale               bool             `json:"stale"`
	Reason              string           `json:"reason,omitempty"`
	PriceSnapshot       *PriceSnapshot   `json:"price_snapshot,omitempty"`
}

type QuotaAuditSummary struct {
	Accounts            int      `json:"accounts"`
	Windows             int      `json:"windows"`
	Samples             int      `json:"samples"`
	UsedPercent         *float64 `json:"used_percent"`
	QuotaDeltaPercent   *float64 `json:"quota_delta_percent"`
	InputTokens         int64    `json:"input_tokens"`
	OutputTokens        int64    `json:"output_tokens"`
	ReasoningTokens     int64    `json:"reasoning_tokens"`
	CachedTokens        int64    `json:"cached_tokens"`
	TotalTokens         int64    `json:"total_tokens"`
	CostDeltaUSD        *float64 `json:"cost_delta_usd"`
	CostPerQuotaPercent *float64 `json:"cost_per_quota_percent"`
	StaleSamples        int      `json:"stale_samples"`
	ResetSamples        int      `json:"reset_samples"`
}

type QuotaAuditResponse struct {
	Snapshots     []QuotaAuditRow   `json:"snapshots"`
	Rows          []QuotaAuditRow   `json:"rows"`
	Summary       QuotaAuditSummary `json:"summary"`
	PriceSnapshot *PriceSnapshot    `json:"price_snapshot,omitempty"`
}

type QuotaAuditStore struct {
	mu           sync.RWMutex
	snapshots    []QuotaWindowSnapshot
	usage        []QuotaAuditUsage
	prices       map[string]PriceSnapshot
	nextSequence int64
}

func NewQuotaAuditStore() *QuotaAuditStore {
	return &QuotaAuditStore{prices: make(map[string]PriceSnapshot)}
}

var defaultQuotaAuditStore = NewQuotaAuditStore()

func DefaultQuotaAuditStore() *QuotaAuditStore { return defaultQuotaAuditStore }

func RecordQuotaSnapshot(authID, authIndex, account string, observedAt time.Time, payload []byte) int {
	return defaultQuotaAuditStore.RecordQuotaSnapshot(authID, authIndex, account, observedAt, payload)
}

func CaptureQuotaUsage(record Record) { defaultQuotaAuditStore.CaptureUsage(record) }

func SetPriceSnapshot(model string, snapshot PriceSnapshot) {
	defaultQuotaAuditStore.SetPriceSnapshot(model, snapshot)
}

func BuildQuotaAudit(query QuotaAuditQuery) QuotaAuditResponse {
	return defaultQuotaAuditStore.Build(query, time.Now().UTC())
}

func (s *QuotaAuditStore) SetPriceSnapshot(model string, snapshot PriceSnapshot) {
	if s == nil {
		return
	}
	model = strings.TrimSpace(model)
	if model == "" {
		return
	}
	if snapshot.CapturedAt.IsZero() {
		snapshot.CapturedAt = time.Now().UTC()
	}
	s.mu.Lock()
	if s.prices == nil {
		s.prices = make(map[string]PriceSnapshot)
	}
	s.prices[model] = snapshot
	s.mu.Unlock()
}

func (s *QuotaAuditStore) CaptureUsage(record Record) {
	if s == nil || !strings.EqualFold(strings.TrimSpace(record.Provider), "codex") {
		return
	}
	timestamp := record.RequestedAt
	if timestamp.IsZero() {
		timestamp = time.Now().UTC()
	}
	item := QuotaAuditUsage{
		Provider: record.Provider, Model: record.Model, AuthID: record.AuthID,
		AuthIndex: record.AuthIndex, SessionID: record.SessionID, RequestID: record.RequestID,
		Timestamp: timestamp.UTC(), InputTokens: record.Detail.InputTokens,
		OutputTokens: record.Detail.OutputTokens, ReasoningTokens: record.Detail.ReasoningTokens,
		CachedTokens: record.Detail.CachedTokens, TotalTokens: record.Detail.TotalTokens,
		Failed: record.Failed,
	}
	if item.TotalTokens == 0 {
		item.TotalTokens = item.InputTokens + item.OutputTokens + item.ReasoningTokens + item.CachedTokens
		if strings.EqualFold(strings.TrimSpace(item.Provider), "codex") {
			item.TotalTokens = item.InputTokens + item.OutputTokens
		}
	}
	s.mu.Lock()
	if price, ok := s.prices[strings.TrimSpace(record.Model)]; ok {
		priceCopy := price
		item.PriceSnapshot = &priceCopy
		tokens := QuotaAuditTokens{Input: item.InputTokens, Output: item.OutputTokens, Reasoning: item.ReasoningTokens, Cached: item.CachedTokens, Total: item.TotalTokens}
		if cost, valid := calculateCost(tokens, price); valid {
			item.CostUSD = &cost
		}
	}
	if len(s.usage) >= 10000 {
		s.usage = append(s.usage[:0], s.usage[len(s.usage)-9999:]...)
	}
	s.usage = append(s.usage, item)
	s.mu.Unlock()
}

func (s *QuotaAuditStore) RecordQuotaSnapshot(authID, authIndex, account string, observedAt time.Time, payload []byte) int {
	if s == nil || !json.Valid(payload) {
		return 0
	}
	if observedAt.IsZero() {
		observedAt = time.Now().UTC()
	}
	parsed := parseCodexQuotaPayload(authID, authIndex, account, observedAt.UTC(), payload)
	if len(parsed) == 0 {
		return 0
	}
	s.mu.Lock()
	added := 0
	for i := range parsed {
		s.nextSequence++
		parsed[i].sequence = s.nextSequence

		duplicate := false
		for _, existing := range s.snapshots {
			if (existing.SnapshotID != "" && parsed[i].SnapshotID != "" && existing.SnapshotID == parsed[i].SnapshotID) ||
				(existing.SnapshotID == "" && parsed[i].SnapshotID == "" && sameSnapshotFields(existing, parsed[i])) {
				duplicate = true
				break
			}
		}
		if duplicate {
			continue
		}
		s.snapshots = append(s.snapshots, parsed[i])
		added++
	}
	if len(s.snapshots) > 10000 {
		s.snapshots = append([]QuotaWindowSnapshot(nil), s.snapshots[len(s.snapshots)-10000:]...)
	}
	s.mu.Unlock()
	return added
}

func (s *QuotaAuditStore) Export() QuotaAuditExport {
	result := QuotaAuditExport{PriceSnapshots: make(map[string]PriceSnapshot)}
	if s == nil {
		return result
	}
	s.mu.RLock()
	result.Snapshots = append([]QuotaWindowSnapshot(nil), s.snapshots...)
	result.Usage = append([]QuotaAuditUsage(nil), s.usage...)
	for key, value := range s.prices {
		result.PriceSnapshots[key] = value
	}
	s.mu.RUnlock()
	return result
}

func (s *QuotaAuditStore) Merge(export QuotaAuditExport) (addedSnapshots, addedUsage int64) {
	if s == nil {
		return 0, 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.prices == nil {
		s.prices = make(map[string]PriceSnapshot)
	}
	for model, price := range export.PriceSnapshots {
		if _, exists := s.prices[model]; !exists {
			s.prices[model] = price
		}
	}
	for _, snapshot := range export.Snapshots {
		duplicate := false
		for _, existing := range s.snapshots {
			if (existing.SnapshotID != "" && snapshot.SnapshotID != "" && existing.SnapshotID == snapshot.SnapshotID) ||
				(existing.SnapshotID == "" && snapshot.SnapshotID == "" && sameSnapshotFields(existing, snapshot)) {
				duplicate = true
				break
			}
		}
		if duplicate {
			continue
		}
		s.nextSequence++
		snapshot.sequence = s.nextSequence
		s.snapshots = append(s.snapshots, snapshot)
		addedSnapshots++
	}
	for _, item := range export.Usage {
		duplicate := false
		for _, existing := range s.usage {
			if usageKey(existing) == usageKey(item) {
				duplicate = true
				break
			}
		}
		if duplicate {
			continue
		}
		s.usage = append(s.usage, item)
		addedUsage++
	}
	return addedSnapshots, addedUsage
}

func usageKey(item QuotaAuditUsage) string {
	return strings.Join([]string{item.AuthID, item.AuthIndex, item.RequestID, item.SessionID,
		item.Model, item.Timestamp.UTC().Format(time.RFC3339Nano), strconv.FormatInt(item.TotalTokens, 10)}, "|")
}

type quotaGroup struct {
	identity string
	window   string
}

func (s *QuotaAuditStore) Build(query QuotaAuditQuery, now time.Time) QuotaAuditResponse {
	result := QuotaAuditResponse{Snapshots: []QuotaAuditRow{}, Rows: []QuotaAuditRow{}}
	if s == nil {
		return result
	}
	query.Auth = strings.TrimSpace(query.Auth)
	query.Account = strings.TrimSpace(query.Account)
	query.Window = strings.TrimSpace(query.Window)
	query.Model = strings.TrimSpace(query.Model)
	s.mu.RLock()
	snapshots := append([]QuotaWindowSnapshot(nil), s.snapshots...)
	usage := append([]QuotaAuditUsage(nil), s.usage...)
	prices := make(map[string]PriceSnapshot, len(s.prices))
	for key, value := range s.prices {
		prices[key] = value
	}
	s.mu.RUnlock()

	groups := make(map[quotaGroup][]QuotaWindowSnapshot)
	for _, snapshot := range snapshots {
		if !snapshotInQuery(snapshot, query) {
			continue
		}
		key := quotaGroup{identity: snapshotIdentity(snapshot), window: snapshot.Window}
		groups[key] = append(groups[key], snapshot)
	}
	keys := make([]quotaGroup, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].identity == keys[j].identity {
			if windowRank(keys[i].window) != windowRank(keys[j].window) {
				return windowRank(keys[i].window) < windowRank(keys[j].window)
			}
			return keys[i].window < keys[j].window
		}
		return keys[i].identity < keys[j].identity
	})
	assignedUsage := make(map[string]struct{})
	for _, key := range keys {
		items := groups[key]
		sort.SliceStable(items, func(i, j int) bool {
			if items[i].ObservedAt.Equal(items[j].ObservedAt) {
				return items[i].sequence < items[j].sequence
			}
			return items[i].ObservedAt.Before(items[j].ObservedAt)
		})
		for i, snapshot := range items {
			var previous *QuotaWindowSnapshot
			if i > 0 {
				previous = &items[i-1]
			}
			row := buildQuotaAuditRow(snapshot, previous, usage, query.Model, now, assignedUsage)
			if query.Model != "" && row.Model != query.Model {
				continue
			}
			result.Rows = append(result.Rows, row)
		}
	}
	result.Snapshots = append(result.Snapshots, result.Rows...)
	result.Summary = summarizeQuotaRows(result.Rows)
	if query.Model != "" {
		if price, ok := prices[query.Model]; ok {
			result.PriceSnapshot = &price
		}
	} else if len(prices) == 1 {
		for _, price := range prices {
			copyPrice := price
			result.PriceSnapshot = &copyPrice
		}
	}
	return result
}

func windowRank(window string) int {
	switch strings.ToLower(strings.TrimSpace(window)) {
	case "primary":
		return 0
	case "secondary":
		return 1
	case "code-review":
		return 2
	default:
		return 3
	}
}

func snapshotInQuery(snapshot QuotaWindowSnapshot, query QuotaAuditQuery) bool {
	if query.Window != "" && !strings.EqualFold(query.Window, snapshot.Window) {
		return false
	}
	if query.Auth != "" && query.Auth != snapshot.AuthID && query.Auth != snapshot.AuthIndex && query.Auth != snapshot.Account {
		return false
	}
	if query.Account != "" && query.Account != snapshot.Account && query.Account != snapshot.AuthID && query.Account != snapshot.AuthIndex {
		return false
	}
	if !query.From.IsZero() && snapshot.ObservedAt.Before(query.From) {
		return false
	}
	if !query.To.IsZero() && snapshot.ObservedAt.After(query.To) {
		return false
	}
	return true
}

func snapshotIdentity(snapshot QuotaWindowSnapshot) string {
	if snapshot.AuthID != "" {
		return snapshot.AuthID
	}
	if snapshot.AuthIndex != "" {
		return snapshot.AuthIndex
	}
	return snapshot.Account
}

func buildQuotaAuditRow(snapshot QuotaWindowSnapshot, previous *QuotaWindowSnapshot, usage []QuotaAuditUsage, modelFilter string, now time.Time, assigned map[string]struct{}) QuotaAuditRow {
	row := QuotaAuditRow{
		SnapshotID: snapshot.SnapshotID, Auth: snapshotIdentity(snapshot), Account: snapshot.Account, Window: snapshot.Window,
		PlanType: snapshot.PlanType, Timestamp: snapshot.ObservedAt, UsedPercent: snapshot.UsedPercent,
		RemainingPercent: snapshot.RemainingPercent, SessionIDs: []string{}, ThreadIDs: []string{}, Tokens: QuotaAuditTokens{}, Status: "ok", CostStatus: "unpriced", Stale: quotaSnapshotStale(snapshot, now),
	}
	if !snapshot.ResetAt.IsZero() {
		resetAt := snapshot.ResetAt
		row.ResetAt = &resetAt
	}
	reasons := make([]string, 0, 4)
	if row.Stale {
		reasons = append(reasons, "stale")
	}
	if previous == nil {
		reasons = append(reasons, "no_previous_snapshot")
	} else if snapshot.ObservedAt.Equal(previous.ObservedAt) {
		reasons = append(reasons, "duplicate_timestamp")
	} else {
		if snapshot.UsedPercent != nil && previous.UsedPercent != nil {
			delta := *snapshot.UsedPercent - *previous.UsedPercent
			if delta < 0 {
				reasons = append(reasons, "used_percent_decreased", "negative_delta")
				if !snapshot.ResetAt.IsZero() && !previous.ResetAt.IsZero() && !snapshot.ResetAt.Equal(previous.ResetAt) {
					row.Reset = true
					reasons = append(reasons, "reset")
				}
			} else {
				row.QuotaDeltaPercent = &delta
			}
		}
		if snapshot.sequence < previous.sequence {
			reasons = append(reasons, "out_of_order")
		}
	}

	start := time.Time{}
	if previous != nil {
		start = previous.ObservedAt
	}
	matched := matchingUsage(snapshot, usage, start, modelFilter, assigned)
	for _, item := range matched {
		assigned[usageKey(item)] = struct{}{}
	}
	for _, item := range matched {
		if item.SessionID != "" && !containsString(row.SessionIDs, item.SessionID) {
			row.SessionIDs = append(row.SessionIDs, item.SessionID)
		}
	}
	model := ""
	modelNames := make(map[string]struct{})
	for _, item := range matched {
		name := strings.TrimSpace(item.Model)
		if name != "" {
			modelNames[name] = struct{}{}
		}
		row.Tokens.Input += item.InputTokens
		row.Tokens.Output += item.OutputTokens
		row.Tokens.Reasoning += item.ReasoningTokens
		row.Tokens.Cached += item.CachedTokens
		row.Tokens.Total += item.TotalTokens
	}
	if len(modelNames) == 1 {
		for name := range modelNames {
			model = name
		}
	} else if len(modelNames) > 1 {
		model = "mixed"
	}
	row.Model = model
	if len(matched) == 0 || (row.Tokens.Input == 0 && row.Tokens.Output == 0 && row.Tokens.Reasoning == 0 && row.Tokens.Cached == 0 && row.Tokens.Total == 0) {
		reasons = append(reasons, "missing_token")
	}
	var cost float64
	priced := len(matched) > 0
	for _, item := range matched {
		if item.TotalTokens == 0 && item.InputTokens == 0 && item.OutputTokens == 0 && item.ReasoningTokens == 0 && item.CachedTokens == 0 {
			continue
		}
		if item.CostUSD == nil {
			priced = false
			continue
		}
		cost += *item.CostUSD
		if row.PriceSnapshot == nil && item.PriceSnapshot != nil {
			priceCopy := *item.PriceSnapshot
			row.PriceSnapshot = &priceCopy
		}
	}
	if priced {
		row.CostStatus = "priced"
		row.CostDeltaUSD = &cost
		if row.QuotaDeltaPercent != nil && *row.QuotaDeltaPercent > 0 {
			unit := cost / *row.QuotaDeltaPercent
			row.CostPerQuotaPercent = &unit
		}
	} else {
		row.CostStatus = "unpriced"
		reasons = append(reasons, "missing_price")
	}
	if snapshot.UsedPercent == nil {
		reasons = append(reasons, "missing_used_percent")
	}
	row.Reason = strings.Join(uniqueStrings(reasons), ",")
	if row.Stale || row.Reset || row.UsedPercent == nil {
		row.QuotaDeltaPercent = nil
		row.CostPerQuotaPercent = nil
	}
	row.Status = quotaRowStatus(row, reasons)
	return row
}

func matchingUsage(snapshot QuotaWindowSnapshot, usage []QuotaAuditUsage, start time.Time, modelFilter string, assigned map[string]struct{}) []QuotaAuditUsage {
	result := make([]QuotaAuditUsage, 0)
	for _, item := range usage {
		if _, alreadyAssigned := assigned[usageKey(item)]; alreadyAssigned {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(item.Provider), "codex") {
			continue
		}
		if snapshot.AuthID != "" && item.AuthID != snapshot.AuthID {
			continue
		}
		if snapshot.AuthID == "" && snapshot.AuthIndex != "" && item.AuthIndex != snapshot.AuthIndex {
			continue
		}
		if !start.IsZero() && !item.Timestamp.After(start) {
			continue
		}
		if item.Timestamp.After(snapshot.ObservedAt) {
			continue
		}
		if modelFilter != "" && item.Model != modelFilter {
			continue
		}
		result = append(result, item)
	}
	return result
}

func quotaSnapshotStale(snapshot QuotaWindowSnapshot, now time.Time) bool {
	if snapshot.ObservedAt.IsZero() || now.Before(snapshot.ObservedAt) {
		return false
	}
	threshold := 24 * time.Hour
	if snapshot.WindowDurationSeconds != nil && *snapshot.WindowDurationSeconds > 0 {
		threshold = time.Duration(*snapshot.WindowDurationSeconds * float64(time.Second))
	}
	return now.Sub(snapshot.ObservedAt) > threshold
}

func calculateCost(tokens QuotaAuditTokens, price PriceSnapshot) (float64, bool) {
	cost := float64(0)
	input := tokens.Input - tokens.Cached
	if input < 0 {
		input = 0
	}
	if input > 0 {
		if price.InputPerMillionUSD == nil {
			return 0, false
		}
		cost += float64(input) / 1_000_000 * *price.InputPerMillionUSD
	}
	if tokens.Cached > 0 {
		if price.CachedPerMillionUSD == nil {
			return 0, false
		}
		cost += float64(tokens.Cached) / 1_000_000 * *price.CachedPerMillionUSD
	}
	nonReasoningOutput := tokens.Output - tokens.Reasoning
	if nonReasoningOutput < 0 {
		nonReasoningOutput = 0
	}
	if nonReasoningOutput > 0 {
		if price.OutputPerMillionUSD == nil {
			return 0, false
		}
		cost += float64(nonReasoningOutput) / 1_000_000 * *price.OutputPerMillionUSD
	}
	if tokens.Reasoning > 0 {
		if price.ReasoningPerMillionUSD != nil {
			cost += float64(tokens.Reasoning) / 1_000_000 * *price.ReasoningPerMillionUSD
		} else if price.OutputPerMillionUSD != nil {
			cost += float64(tokens.Reasoning) / 1_000_000 * *price.OutputPerMillionUSD
		} else {
			return 0, false
		}
	}
	return cost, true
}

func quotaRowStatus(row QuotaAuditRow, reasons []string) string {
	if row.Reset {
		return "reset"
	}
	for _, reason := range reasons {
		if reason == "negative_delta" || reason == "used_percent_decreased" {
			return "negative"
		}
	}
	if row.Stale {
		return "stale"
	}
	if row.UsedPercent == nil || len(reasons) > 0 {
		if row.UsedPercent == nil && len(reasons) == 1 && reasons[0] == "missing_used_percent" {
			return "unknown"
		}
		return "warning"
	}
	return "ok"
}

func summarizeQuotaRows(rows []QuotaAuditRow) QuotaAuditSummary {
	result := QuotaAuditSummary{Samples: len(rows)}
	accounts := make(map[string]struct{})
	windows := make(map[string]struct{})
	var usedSum float64
	var usedCount int
	var deltaSum float64
	var deltaCount int
	var costSum float64
	var costCount int
	var unitSum float64
	var unitCount int
	for _, row := range rows {
		accounts[row.Account] = struct{}{}
		windows[row.Window] = struct{}{}
		if row.UsedPercent != nil {
			usedSum += *row.UsedPercent
			usedCount++
		}
		if row.QuotaDeltaPercent != nil {
			deltaSum += *row.QuotaDeltaPercent
			deltaCount++
		}
		result.InputTokens += row.Tokens.Input
		result.OutputTokens += row.Tokens.Output
		result.ReasoningTokens += row.Tokens.Reasoning
		result.CachedTokens += row.Tokens.Cached
		result.TotalTokens += row.Tokens.Total
		if row.CostDeltaUSD != nil {
			costSum += *row.CostDeltaUSD
			costCount++
		}
		if row.CostPerQuotaPercent != nil {
			unitSum += *row.CostPerQuotaPercent
			unitCount++
		}
		if row.Stale {
			result.StaleSamples++
		}
		if row.Reset {
			result.ResetSamples++
		}
	}
	result.Accounts = len(accounts)
	result.Windows = len(windows)
	if usedCount > 0 {
		value := usedSum / float64(usedCount)
		result.UsedPercent = &value
	}
	if deltaCount > 0 {
		result.QuotaDeltaPercent = &deltaSum
	}
	if costCount > 0 {
		result.CostDeltaUSD = &costSum
	}
	if unitCount > 0 {
		value := unitSum / float64(unitCount)
		result.CostPerQuotaPercent = &value
	}
	return result
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok || value == "" {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func parseCodexQuotaPayload(authID, authIndex, account string, observedAt time.Time, payload []byte) []QuotaWindowSnapshot {
	root := gjson.ParseBytes(payload)
	if !root.Exists() || root.Type != gjson.JSON {
		return nil
	}
	planType := firstString(root, "plan_type", "planType", "plan")
	version := firstString(root, "version", "schema_version", "schemaVersion")
	if version == "" {
		version = "wham"
	}
	shape := quotaPayloadShape(root)
	result := make([]QuotaWindowSnapshot, 0, 4)
	addRateLimit := func(path string, rateLimit gjson.Result) {
		if !rateLimit.Exists() || rateLimit.Type == gjson.Null {
			return
		}
		if path == "code_review_rate_limit" || path == "codeReviewRateLimit" {
			for _, key := range []string{"primary_window", "primaryWindow", "window"} {
				window := rateLimit.Get(key)
				if window.Exists() && window.Type != gjson.Null {
					result = append(result, makeQuotaSnapshot(authID, authIndex, account, planType, "code-review", observedAt, shape, version, window))
					break
				}
			}
			return
		}
		for _, entry := range []struct {
			name string
			keys []string
		}{
			{name: "primary", keys: []string{"primary_window", "primaryWindow"}},
			{name: "secondary", keys: []string{"secondary_window", "secondaryWindow"}},
		} {
			for _, key := range entry.keys {
				window := rateLimit.Get(key)
				if !window.Exists() || window.Type == gjson.Null {
					continue
				}
				result = append(result, makeQuotaSnapshot(authID, authIndex, account, planType, entry.name, observedAt, shape, version, window))
				break
			}
		}
	}
	for _, path := range []string{"rate_limit", "rateLimit", "code_review_rate_limit", "codeReviewRateLimit"} {
		addRateLimit(path, root.Get(path))
	}
	additional := root.Get("additional_rate_limits")
	if additional.Exists() && additional.Type == gjson.JSON {
		additional.ForEach(func(key, value gjson.Result) bool {
			if value.Type == gjson.JSON {
				addRateLimit("additional:"+key.String(), value)
			}
			return true
		})
	}
	return result
}

func makeQuotaSnapshot(authID, authIndex, account, planType, window string, observedAt time.Time, shape, version string, value gjson.Result) QuotaWindowSnapshot {
	result := QuotaWindowSnapshot{AuthID: authID, AuthIndex: authIndex, Account: account, PlanType: planType, Window: window, ObservedAt: observedAt, CapturedAt: observedAt, Shape: shape, Version: version}
	for _, key := range []string{"used_percent", "usedPercent"} {
		if number, ok := quotaNumber(value.Get(key)); ok {
			result.UsedPercent = &number
			break
		}
	}
	for _, key := range []string{"remaining_percent", "remainingPercent"} {
		if number, ok := quotaNumber(value.Get(key)); ok {
			result.RemainingPercent = &number
			break
		}
	}
	if result.RemainingPercent == nil {
		for _, key := range []string{"remaining_fraction", "remainingFraction"} {
			if number, ok := quotaNumber(value.Get(key)); ok {
				percent := number * 100
				result.RemainingPercent = &percent
				break
			}
		}
	}
	for _, key := range []string{"limit_window_seconds", "limitWindowSeconds", "window_seconds", "windowSeconds"} {
		if number, ok := quotaNumber(value.Get(key)); ok {
			result.WindowDurationSeconds = &number
			break
		}
	}
	if result.WindowDurationSeconds == nil {
		for _, key := range []string{"limit_window_minutes", "limitWindowMinutes", "window_minutes", "windowMinutes"} {
			if number, ok := quotaNumber(value.Get(key)); ok {
				seconds := number * 60
				result.WindowDurationSeconds = &seconds
				break
			}
		}
	}
	for _, key := range []string{"reset_at", "resetAt"} {
		if resetAt, ok := quotaTime(value.Get(key)); ok {
			result.ResetAt = resetAt
			break
		}
	}
	if result.ResetAt.IsZero() {
		for _, key := range []string{"reset_after_seconds", "resetAfterSeconds"} {
			if number, ok := quotaNumber(value.Get(key)); ok && number > 0 {
				result.ResetAt = observedAt.Add(time.Duration(number * float64(time.Second))).UTC()
				break
			}
		}
	}
	idInput := strings.Join([]string{authID, authIndex, account, planType, window, observedAt.UTC().Format(time.RFC3339Nano), shape, version, value.Raw}, "|")
	digest := sha256.Sum256([]byte(idInput))
	result.SnapshotID = hex.EncodeToString(digest[:])
	return result
}

func sameFloat(left, right *float64) bool {
	if left == nil || right == nil {
		return left == right
	}
	return *left == *right
}

func sameSnapshotFields(left, right QuotaWindowSnapshot) bool {
	return left.AuthID == right.AuthID && left.AuthIndex == right.AuthIndex && left.Account == right.Account &&
		left.Window == right.Window && left.ObservedAt.Equal(right.ObservedAt) && sameFloat(left.UsedPercent, right.UsedPercent) &&
		left.ResetAt.Equal(right.ResetAt)
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func firstString(root gjson.Result, keys ...string) string {
	for _, key := range keys {
		value := root.Get(key)
		if value.Exists() {
			if text := strings.TrimSpace(value.String()); text != "" {
				return text
			}
		}
	}
	return ""
}

func quotaPayloadShape(root gjson.Result) string {
	parts := make([]string, 0, 4)
	for _, key := range []string{"rate_limit", "rateLimit", "code_review_rate_limit", "codeReviewRateLimit", "additional_rate_limits"} {
		if root.Get(key).Exists() {
			parts = append(parts, key)
		}
	}
	if len(parts) == 0 {
		return "unknown"
	}
	sort.Strings(parts)
	return strings.Join(parts, "+")
}

func quotaNumber(value gjson.Result) (float64, bool) {
	if !value.Exists() {
		return 0, false
	}
	if value.Type == gjson.Number {
		return value.Float(), true
	}
	parsed, err := strconv.ParseFloat(strings.TrimSpace(value.String()), 64)
	return parsed, err == nil
}

func quotaTime(value gjson.Result) (time.Time, bool) {
	if !value.Exists() {
		return time.Time{}, false
	}
	if value.Type == gjson.String {
		if parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(value.String())); err == nil {
			return parsed.UTC(), true
		}
	}
	seconds, ok := quotaNumber(value)
	if !ok || seconds <= 0 {
		return time.Time{}, false
	}
	if seconds > 1e12 {
		return time.UnixMilli(int64(seconds)).UTC(), true
	}
	return time.Unix(int64(seconds), 0).UTC(), true
}
