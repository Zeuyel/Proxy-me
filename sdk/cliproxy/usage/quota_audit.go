package usage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"math/rand"
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
	Accounts       []QuotaAuditAccount      `json:"accounts,omitempty"`
	PriceSnapshots map[string]PriceSnapshot `json:"price_snapshots,omitempty"`
	ManualPrices   []string                 `json:"manual_prices,omitempty"`
}

type QuotaAuditQuery struct {
	AuthIndex string
	Auth      string
	Account   string
	Window    string
	Model     string
	From      time.Time
	To        time.Time
}

type QuotaAuditAccount struct {
	AuthID    string    `json:"auth_id"`
	AuthIndex string    `json:"auth_index,omitempty"`
	Account   string    `json:"account,omitempty"`
	Provider  string    `json:"provider,omitempty"`
	Disabled  bool      `json:"disabled,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
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
	AuthID              string           `json:"auth_id,omitempty"`
	AuthIndex           string           `json:"auth_index,omitempty"`
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
	Snapshots     []QuotaAuditRow     `json:"snapshots"`
	Rows          []QuotaAuditRow     `json:"rows"`
	Accounts      []QuotaAuditAccount `json:"accounts"`
	Summary       QuotaAuditSummary   `json:"summary"`
	PriceSnapshot *PriceSnapshot      `json:"price_snapshot,omitempty"`
}

type QuotaAuditStore struct {
	mu           sync.RWMutex
	snapshots    []QuotaWindowSnapshot
	usage        []QuotaAuditUsage
	accounts     map[string]QuotaAuditAccount
	prices       map[string]PriceSnapshot
	manualPrices map[string]struct{}
	nextSequence int64
	sampler      *QuotaCostSampler
	changeHook   func()
}

type QuotaProbeRequest struct {
	AuthID    string
	AuthIndex string
}

type QuotaProbeFunc func(context.Context, QuotaProbeRequest) error

type QuotaCostSamplerOptions struct {
	MinCostUSD   float64
	MaxCostUSD   float64
	Random       func() float64
	RetryBase    time.Duration
	RetryMax     time.Duration
	ProbeTimeout time.Duration
}

type QuotaCostSampler struct {
	mu           sync.Mutex
	states       map[string]*quotaSamplerState
	probe        QuotaProbeFunc
	random       func() float64
	minCostUSD   float64
	maxCostUSD   float64
	retryBase    time.Duration
	retryMax     time.Duration
	probeTimeout time.Duration
	closed       bool
}

type quotaSamplerState struct {
	authID       string
	authIndex    string
	accumulated  float64
	target       float64
	inFlight     bool
	pending      bool
	retryCount   int
	retryAt      time.Time
	seenRequests map[string]struct{}
	timer        *time.Timer
}

var ErrQuotaSamplerClosed = errors.New("quota cost sampler is closed")

func NewQuotaCostSampler(probe QuotaProbeFunc, options QuotaCostSamplerOptions) *QuotaCostSampler {
	minCost := options.MinCostUSD
	maxCost := options.MaxCostUSD
	if minCost <= 0 {
		minCost = 20
	}
	if maxCost < minCost {
		maxCost = minCost
	}
	randomFn := options.Random
	if randomFn == nil {
		randomFn = rand.Float64
	}
	retryBase := options.RetryBase
	if retryBase <= 0 {
		retryBase = 15 * time.Second
	}
	retryMax := options.RetryMax
	if retryMax < retryBase {
		retryMax = 10 * time.Minute
	}
	probeTimeout := options.ProbeTimeout
	if probeTimeout <= 0 {
		probeTimeout = 15 * time.Second
	}
	return &QuotaCostSampler{
		states:       make(map[string]*quotaSamplerState),
		probe:        probe,
		random:       randomFn,
		minCostUSD:   minCost,
		maxCostUSD:   maxCost,
		retryBase:    retryBase,
		retryMax:     retryMax,
		probeTimeout: probeTimeout,
	}
}

func (s *QuotaCostSampler) SetProbe(probe QuotaProbeFunc) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.probe = probe
	if s.closed || probe == nil {
		s.mu.Unlock()
		return
	}
	launches := s.collectReadyLaunchesLocked(time.Now())
	s.mu.Unlock()
	for _, launch := range launches {
		s.launch(launch)
	}
}

func (s *QuotaCostSampler) Observe(_ context.Context, item QuotaAuditUsage) {
	if s == nil || item.CostUSD == nil || !strings.EqualFold(strings.TrimSpace(item.Provider), "codex") {
		return
	}
	authIndex := strings.TrimSpace(item.AuthIndex)
	identity := authIndex
	if identity == "" {
		identity = strings.TrimSpace(item.AuthID)
	}
	if identity == "" {
		return
	}
	cost := *item.CostUSD
	if math.IsNaN(cost) || math.IsInf(cost, 0) || cost < 0 {
		return
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	state := s.states[identity]
	if state == nil {
		state = &quotaSamplerState{authID: strings.TrimSpace(item.AuthID), authIndex: authIndex, target: s.nextTargetLocked(), seenRequests: make(map[string]struct{})}
		s.states[identity] = state
	}
	if state.authID == "" {
		state.authID = strings.TrimSpace(item.AuthID)
	}
	if requestID := strings.TrimSpace(item.RequestID); requestID != "" {
		if _, duplicate := state.seenRequests[requestID]; duplicate {
			s.mu.Unlock()
			return
		}
		if len(state.seenRequests) >= 10000 {
			for oldest := range state.seenRequests {
				delete(state.seenRequests, oldest)
				break
			}
		}
		state.seenRequests[requestID] = struct{}{}
	}
	state.accumulated += cost
	if state.accumulated >= state.target {
		state.pending = true
	}
	launches := s.collectReadyLaunchesLocked(time.Now())
	s.mu.Unlock()
	for _, launch := range launches {
		s.launch(launch)
	}
}

func (s *QuotaCostSampler) Close() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.closed = true
	for _, state := range s.states {
		if state.timer != nil {
			state.timer.Stop()
			state.timer = nil
		}
	}
	s.mu.Unlock()
}

type quotaSamplerLaunch struct {
	key       string
	authID    string
	authIndex string
}

func (s *QuotaCostSampler) nextTargetLocked() float64 {
	value := s.random()
	if math.IsNaN(value) || math.IsInf(value, 0) {
		value = 0.5
	}
	if value < 0 {
		value = 0
	}
	if value > 1 {
		value = 1
	}
	return s.minCostUSD + value*(s.maxCostUSD-s.minCostUSD)
}

func (s *QuotaCostSampler) collectReadyLaunchesLocked(now time.Time) []quotaSamplerLaunch {
	if s.probe == nil || s.closed {
		return nil
	}
	launches := make([]quotaSamplerLaunch, 0)
	for key, state := range s.states {
		if state.pending && !state.inFlight && (state.retryAt.IsZero() || !now.Before(state.retryAt)) {
			state.inFlight = true
			state.retryAt = time.Time{}
			if state.timer != nil {
				state.timer.Stop()
				state.timer = nil
			}
			launches = append(launches, quotaSamplerLaunch{key: key, authID: state.authID, authIndex: state.authIndex})
		}
	}
	return launches
}

func (s *QuotaCostSampler) launch(launch quotaSamplerLaunch) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), s.probeTimeout)
		err := ErrQuotaSamplerClosed
		s.mu.Lock()
		probe := s.probe
		closed := s.closed
		s.mu.Unlock()
		if !closed && probe != nil {
			err = probe(ctx, QuotaProbeRequest{AuthID: launch.authID, AuthIndex: launch.authIndex})
		}
		cancel()
		s.finish(launch, err)
	}()
}

func (s *QuotaCostSampler) finish(launch quotaSamplerLaunch, probeErr error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := s.states[launch.key]
	if state == nil {
		return
	}
	state.inFlight = false
	if probeErr != nil {
		state.pending = true
		state.retryCount++
		backoff := s.retryBase
		for i := 1; i < state.retryCount && backoff < s.retryMax; i++ {
			backoff *= 2
			if backoff >= s.retryMax {
				backoff = s.retryMax
				break
			}
		}
		state.retryAt = time.Now().Add(backoff)
		if !s.closed {
			state.timer = time.AfterFunc(backoff, func() {
				s.mu.Lock()
				launches := s.collectReadyLaunchesLocked(time.Now())
				s.mu.Unlock()
				for _, next := range launches {
					s.launch(next)
				}
			})
		}
		return
	}
	state.pending = state.accumulated >= state.target
	if state.pending {
		state.accumulated -= state.target
	}
	state.target = s.nextTargetLocked()
	state.retryCount = 0
	state.retryAt = time.Time{}
	if state.accumulated < state.target {
		state.pending = false
	}
	launches := s.collectReadyLaunchesLocked(time.Now())
	for _, next := range launches {
		go s.launch(next)
	}
}

func NewQuotaAuditStore() *QuotaAuditStore {
	store := &QuotaAuditStore{
		accounts:     make(map[string]QuotaAuditAccount),
		prices:       make(map[string]PriceSnapshot),
		manualPrices: make(map[string]struct{}),
	}
	store.sampler = NewQuotaCostSampler(nil, QuotaCostSamplerOptions{})
	return store
}

var defaultQuotaAuditStore = NewQuotaAuditStore()

func DefaultQuotaAuditStore() *QuotaAuditStore { return defaultQuotaAuditStore }

func RecordQuotaSnapshot(authID, authIndex, account string, observedAt time.Time, payload []byte) int {
	return defaultQuotaAuditStore.RecordQuotaSnapshot(authID, authIndex, account, observedAt, payload)
}

func CaptureQuotaUsage(record Record) { defaultQuotaAuditStore.CaptureUsage(record) }

func SetQuotaProbe(probe QuotaProbeFunc) { defaultQuotaAuditStore.SetQuotaProbe(probe) }

func SetPriceSnapshot(model string, snapshot PriceSnapshot) {
	defaultQuotaAuditStore.SetPriceSnapshot(model, snapshot)
}

// SetManualPriceSnapshot stores a price that remote synchronization must not replace.
func SetManualPriceSnapshot(model string, snapshot PriceSnapshot) {
	defaultQuotaAuditStore.SetManualPriceSnapshot(model, snapshot)
}

func SyncQuotaAuditAccounts(accounts []QuotaAuditAccount) {
	defaultQuotaAuditStore.SyncAccounts(accounts)
}

func BuildQuotaAudit(query QuotaAuditQuery) QuotaAuditResponse {
	return defaultQuotaAuditStore.Build(query, time.Now().UTC())
}

func (s *QuotaAuditStore) SetPriceSnapshot(model string, snapshot PriceSnapshot) {
	s.setPriceSnapshot(model, snapshot)
}

func (s *QuotaAuditStore) SetManualPriceSnapshot(model string, snapshot PriceSnapshot) {
	s.setPriceSnapshot(model, snapshot)
}

func (s *QuotaAuditStore) SetChangeHook(hook func()) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.changeHook = hook
	s.mu.Unlock()
}

func (s *QuotaAuditStore) notifyChanged() {
	if s == nil {
		return
	}
	s.mu.RLock()
	hook := s.changeHook
	s.mu.RUnlock()
	if hook != nil {
		hook()
	}
}

func (s *QuotaAuditStore) SyncAccounts(accounts []QuotaAuditAccount) {
	if s == nil {
		return
	}
	next := make(map[string]QuotaAuditAccount, len(accounts))
	for _, account := range accounts {
		account.AuthID = strings.TrimSpace(account.AuthID)
		account.AuthIndex = strings.TrimSpace(account.AuthIndex)
		account.Account = strings.TrimSpace(account.Account)
		account.Provider = strings.TrimSpace(account.Provider)
		identity := quotaAccountIdentity(account)
		if identity == "" {
			continue
		}
		if account.UpdatedAt.IsZero() {
			account.UpdatedAt = time.Now().UTC()
		} else {
			account.UpdatedAt = account.UpdatedAt.UTC()
		}
		next[identity] = account
	}
	s.mu.Lock()
	s.accounts = next
	s.mu.Unlock()
	s.notifyChanged()
}

func (s *QuotaAuditStore) setPriceSnapshot(model string, snapshot PriceSnapshot) {
	if s == nil {
		return
	}
	model = normalizeQuotaAuditModel(model)
	if model == "" {
		return
	}
	for _, rate := range []*float64{snapshot.InputPerMillionUSD, snapshot.OutputPerMillionUSD, snapshot.ReasoningPerMillionUSD, snapshot.CachedPerMillionUSD} {
		if rate != nil && (math.IsNaN(*rate) || math.IsInf(*rate, 0) || *rate < 0) {
			return
		}
	}
	if snapshot.CapturedAt.IsZero() {
		snapshot.CapturedAt = time.Now().UTC()
	}
	snapshot = clonePriceSnapshot(snapshot)
	s.mu.Lock()
	if s.prices == nil {
		s.prices = make(map[string]PriceSnapshot)
	}
	if s.manualPrices == nil {
		s.manualPrices = make(map[string]struct{})
	}
	s.prices[model] = snapshot
	s.manualPrices[model] = struct{}{}
	s.mu.Unlock()
	s.notifyChanged()
}

// SetSyncedPriceSnapshot stores a remotely sourced price unless a manual price exists.
// It returns false when a manual price already owns the model entry.
func (s *QuotaAuditStore) SetSyncedPriceSnapshot(model string, snapshot PriceSnapshot) bool {
	if s == nil {
		return false
	}
	model = normalizeQuotaAuditModel(model)
	if model == "" {
		return false
	}
	for _, rate := range []*float64{snapshot.InputPerMillionUSD, snapshot.OutputPerMillionUSD, snapshot.ReasoningPerMillionUSD, snapshot.CachedPerMillionUSD} {
		if rate != nil && (math.IsNaN(*rate) || math.IsInf(*rate, 0) || *rate < 0) {
			return false
		}
	}
	if snapshot.CapturedAt.IsZero() {
		snapshot.CapturedAt = time.Now().UTC()
	}
	snapshot = clonePriceSnapshot(snapshot)
	s.mu.Lock()
	if s.prices == nil {
		s.prices = make(map[string]PriceSnapshot)
	}
	if s.manualPrices == nil {
		s.manualPrices = make(map[string]struct{})
	}
	if _, manual := s.manualPrices[model]; manual {
		s.mu.Unlock()
		return false
	}
	s.prices[model] = snapshot
	s.mu.Unlock()
	s.notifyChanged()
	return true
}

func (s *QuotaAuditStore) priceSnapshot(model string) (PriceSnapshot, bool, bool) {
	if s == nil {
		return PriceSnapshot{}, false, false
	}
	model = normalizeQuotaAuditModel(model)
	s.mu.RLock()
	price, ok := s.prices[model]
	_, manual := s.manualPrices[model]
	s.mu.RUnlock()
	return clonePriceSnapshot(price), ok, manual
}

func normalizeQuotaAuditModel(model string) string {
	return strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(model)), " "))
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
	if price, ok := s.prices[normalizeQuotaAuditModel(record.Model)]; ok {
		priceCopy := clonePriceSnapshot(price)
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
	s.notifyChanged()
	if s.sampler != nil {
		s.sampler.Observe(context.Background(), item)
	}
}

func (s *QuotaAuditStore) SetQuotaProbe(probe QuotaProbeFunc) {
	if s == nil {
		return
	}
	if s.sampler == nil {
		s.sampler = NewQuotaCostSampler(probe, QuotaCostSamplerOptions{})
		return
	}
	s.sampler.SetProbe(probe)
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
	if added > 0 {
		s.notifyChanged()
	}
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
	result.Accounts = make([]QuotaAuditAccount, 0, len(s.accounts))
	for _, account := range s.accounts {
		result.Accounts = append(result.Accounts, account)
	}
	for key, value := range s.prices {
		result.PriceSnapshots[key] = clonePriceSnapshot(value)
	}
	for model := range s.manualPrices {
		result.ManualPrices = append(result.ManualPrices, model)
	}
	sort.Strings(result.ManualPrices)
	s.mu.RUnlock()
	return result
}

func (s *QuotaAuditStore) Merge(export QuotaAuditExport) (addedSnapshots, addedUsage int64) {
	return s.merge(export, true)
}

// MergePersisted restores a state file without treating remote price snapshots as manual overrides.
func (s *QuotaAuditStore) MergePersisted(export QuotaAuditExport) (addedSnapshots, addedUsage int64) {
	return s.merge(export, false)
}

func (s *QuotaAuditStore) merge(export QuotaAuditExport, importedPricesManual bool) (addedSnapshots, addedUsage int64) {
	if s == nil {
		return 0, 0
	}
	s.mu.Lock()
	if s.prices == nil {
		s.prices = make(map[string]PriceSnapshot)
	}
	if s.manualPrices == nil {
		s.manualPrices = make(map[string]struct{})
	}
	if s.accounts == nil {
		s.accounts = make(map[string]QuotaAuditAccount)
	}
	for _, account := range export.Accounts {
		account.AuthID = strings.TrimSpace(account.AuthID)
		account.AuthIndex = strings.TrimSpace(account.AuthIndex)
		account.Account = strings.TrimSpace(account.Account)
		account.Provider = strings.TrimSpace(account.Provider)
		if identity := quotaAccountIdentity(account); identity != "" {
			s.accounts[identity] = account
		}
	}
	for model, price := range export.PriceSnapshots {
		model = normalizeQuotaAuditModel(model)
		if _, exists := s.prices[model]; !exists {
			s.prices[model] = clonePriceSnapshot(price)
			if importedPricesManual {
				s.manualPrices[model] = struct{}{}
			}
		}
	}
	for _, model := range export.ManualPrices {
		model = normalizeQuotaAuditModel(model)
		if model != "" {
			s.manualPrices[model] = struct{}{}
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
	s.mu.Unlock()
	if len(export.Accounts) > 0 || len(export.PriceSnapshots) > 0 || len(export.ManualPrices) > 0 || addedSnapshots > 0 || addedUsage > 0 {
		s.notifyChanged()
	}
	return addedSnapshots, addedUsage
}

func usageKey(item QuotaAuditUsage) string {
	return strings.Join([]string{item.AuthID, item.AuthIndex, item.RequestID, item.SessionID,
		item.Model, item.Timestamp.UTC().Format(time.RFC3339Nano), strconv.FormatInt(item.InputTokens, 10),
		strconv.FormatInt(item.OutputTokens, 10), strconv.FormatInt(item.ReasoningTokens, 10),
		strconv.FormatInt(item.CachedTokens, 10), strconv.FormatInt(item.TotalTokens, 10)}, "|")
}

func clonePriceSnapshot(snapshot PriceSnapshot) PriceSnapshot {
	clone := snapshot
	if snapshot.InputPerMillionUSD != nil {
		value := *snapshot.InputPerMillionUSD
		clone.InputPerMillionUSD = &value
	}
	if snapshot.OutputPerMillionUSD != nil {
		value := *snapshot.OutputPerMillionUSD
		clone.OutputPerMillionUSD = &value
	}
	if snapshot.ReasoningPerMillionUSD != nil {
		value := *snapshot.ReasoningPerMillionUSD
		clone.ReasoningPerMillionUSD = &value
	}
	if snapshot.CachedPerMillionUSD != nil {
		value := *snapshot.CachedPerMillionUSD
		clone.CachedPerMillionUSD = &value
	}
	return clone
}

type quotaGroup struct {
	identity string
	window   string
}

func (s *QuotaAuditStore) Build(query QuotaAuditQuery, now time.Time) QuotaAuditResponse {
	result := QuotaAuditResponse{Snapshots: []QuotaAuditRow{}, Rows: []QuotaAuditRow{}, Accounts: []QuotaAuditAccount{}}
	if s == nil {
		return result
	}
	query.Auth = strings.TrimSpace(query.Auth)
	query.AuthIndex = strings.TrimSpace(query.AuthIndex)
	query.Account = strings.TrimSpace(query.Account)
	query.Window = strings.TrimSpace(query.Window)
	query.Model = normalizeQuotaAuditModel(query.Model)
	s.mu.RLock()
	snapshots := append([]QuotaWindowSnapshot(nil), s.snapshots...)
	usage := append([]QuotaAuditUsage(nil), s.usage...)
	allAccounts := make([]QuotaAuditAccount, 0, len(s.accounts))
	accounts := make([]QuotaAuditAccount, 0, len(s.accounts))
	for _, account := range s.accounts {
		allAccounts = append(allAccounts, account)
		if quotaAccountInQuery(account, query) {
			accounts = append(accounts, account)
		}
	}
	prices := make(map[string]PriceSnapshot, len(s.prices))
	for key, value := range s.prices {
		prices[key] = value
	}
	s.mu.RUnlock()

	groups := make(map[quotaGroup][]QuotaWindowSnapshot)
	for _, snapshot := range snapshots {
		if !snapshotInQuery(snapshot, query, allAccounts) {
			continue
		}
		key := quotaGroup{identity: quotaSnapshotIdentity(snapshot, allAccounts), window: snapshot.Window}
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
			identity := key.identity
			row := buildQuotaAuditRow(snapshot, previous, identity, usage, query.Model, prices, now, assignedUsage)
			if query.Model != "" && row.Model != query.Model {
				continue
			}
			result.Rows = append(result.Rows, row)
		}
	}
	result.Snapshots = append(result.Snapshots, result.Rows...)
	sort.Slice(accounts, func(i, j int) bool {
		left := quotaAccountLabel(accounts[i])
		right := quotaAccountLabel(accounts[j])
		if left == right {
			return quotaAccountIdentity(accounts[i]) < quotaAccountIdentity(accounts[j])
		}
		return left < right
	})
	result.Accounts = accounts
	result.Summary = summarizeQuotaRows(result.Rows)
	result.Summary.Accounts = quotaAuditAccountCount(result.Rows, accounts)
	if query.Model != "" {
		if price, ok := prices[query.Model]; ok {
			result.PriceSnapshot = &price
		}
	} else if len(prices) == 1 {
		for _, price := range prices {
			copyPrice := clonePriceSnapshot(price)
			result.PriceSnapshot = &copyPrice
		}
	}
	return result
}

func quotaAccountIdentity(account QuotaAuditAccount) string {
	if account.AuthIndex != "" {
		return account.AuthIndex
	}
	if account.AuthID != "" {
		return account.AuthID
	}
	return account.Account
}

func quotaAccountLabel(account QuotaAuditAccount) string {
	if account.Account != "" {
		return account.Account
	}
	return quotaAccountIdentity(account)
}

func quotaAccountInQuery(account QuotaAuditAccount, query QuotaAuditQuery) bool {
	identity := quotaAccountIdentity(account)
	if query.AuthIndex != "" && !quotaIdentityMatches(query.AuthIndex, identity, account.AuthID, account.AuthIndex, account.Account) {
		return false
	}
	if query.Auth != "" && !quotaIdentityMatches(query.Auth, identity, account.AuthID, account.AuthIndex, account.Account) {
		return false
	}
	if query.Account != "" && !quotaIdentityMatches(query.Account, identity, account.AuthID, account.AuthIndex, account.Account) {
		return false
	}
	return true
}

func quotaIdentityMatches(query string, identity string, aliases ...string) bool {
	if query == identity {
		return true
	}
	for _, alias := range aliases {
		if query == alias {
			return true
		}
	}
	return false
}

func quotaSnapshotIdentity(snapshot QuotaWindowSnapshot, accounts []QuotaAuditAccount) string {
	identity := quotaAccountIdentity(QuotaAuditAccount{AuthID: snapshot.AuthID, AuthIndex: snapshot.AuthIndex, Account: snapshot.Account})
	if snapshot.AuthIndex != "" {
		for _, account := range accounts {
			if snapshot.AuthIndex == account.AuthIndex || snapshot.AuthIndex == account.AuthID {
				return quotaAccountIdentity(account)
			}
		}
		return identity
	}
	for _, account := range accounts {
		if snapshot.AuthID != "" && (snapshot.AuthID == account.AuthID || snapshot.AuthID == account.AuthIndex) {
			return quotaAccountIdentity(account)
		}
	}
	if snapshot.Account != "" {
		var match string
		for _, account := range accounts {
			if snapshot.Account != account.Account {
				continue
			}
			candidate := quotaAccountIdentity(account)
			if match != "" && match != candidate {
				return identity
			}
			match = candidate
		}
		if match != "" {
			return match
		}
	}
	return identity
}

func quotaSnapshotMatchesQuery(value string, snapshot QuotaWindowSnapshot, identity string, accounts []QuotaAuditAccount) bool {
	if quotaIdentityMatches(value, identity, snapshot.AuthID, snapshot.AuthIndex, snapshot.Account) {
		return true
	}
	for _, account := range accounts {
		if quotaAccountIdentity(account) == identity && quotaIdentityMatches(value, identity, account.AuthID, account.AuthIndex, account.Account) {
			return true
		}
	}
	return false
}

func quotaAuditAccountCount(rows []QuotaAuditRow, accounts []QuotaAuditAccount) int {
	seen := make(map[string]struct{}, len(rows)+len(accounts))
	for _, account := range accounts {
		if identity := quotaAccountIdentity(account); identity != "" {
			seen[identity] = struct{}{}
		}
	}
	for _, row := range rows {
		identity := strings.TrimSpace(row.Auth)
		if identity == "" {
			identity = strings.TrimSpace(row.Account)
		}
		if identity != "" {
			seen[identity] = struct{}{}
		}
	}
	return len(seen)
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

func snapshotInQuery(snapshot QuotaWindowSnapshot, query QuotaAuditQuery, accounts []QuotaAuditAccount) bool {
	if query.Window != "" && !strings.EqualFold(query.Window, snapshot.Window) {
		return false
	}
	identity := quotaSnapshotIdentity(snapshot, accounts)
	if query.AuthIndex != "" && !quotaSnapshotMatchesQuery(query.AuthIndex, snapshot, identity, accounts) {
		return false
	}
	if query.Auth != "" && !quotaSnapshotMatchesQuery(query.Auth, snapshot, identity, accounts) {
		return false
	}
	if query.Account != "" && !quotaSnapshotMatchesQuery(query.Account, snapshot, identity, accounts) {
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

func buildQuotaAuditRow(snapshot QuotaWindowSnapshot, previous *QuotaWindowSnapshot, identity string, usage []QuotaAuditUsage, modelFilter string, prices map[string]PriceSnapshot, now time.Time, assigned map[string]struct{}) QuotaAuditRow {
	row := QuotaAuditRow{
		SnapshotID: snapshot.SnapshotID, Auth: identity, AuthID: snapshot.AuthID, AuthIndex: snapshot.AuthIndex, Account: snapshot.Account, Window: snapshot.Window,
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
		itemCost := item.CostUSD
		itemPrice := item.PriceSnapshot
		if itemCost == nil {
			if current, ok := prices[normalizeQuotaAuditModel(item.Model)]; ok {
				tokens := QuotaAuditTokens{Input: item.InputTokens, Output: item.OutputTokens, Reasoning: item.ReasoningTokens, Cached: item.CachedTokens, Total: item.TotalTokens}
				if computed, valid := calculateCost(tokens, current); valid {
					itemCost = &computed
					priceCopy := clonePriceSnapshot(current)
					itemPrice = &priceCopy
				}
			}
		}
		if itemCost == nil {
			priced = false
			continue
		}
		cost += *itemCost
		if row.PriceSnapshot == nil && itemPrice != nil {
			priceCopy := clonePriceSnapshot(*itemPrice)
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
		if snapshot.AuthIndex != "" {
			if item.AuthIndex != "" {
				if item.AuthIndex != snapshot.AuthIndex {
					continue
				}
			} else if item.AuthID != snapshot.AuthID {
				continue
			}
		} else if snapshot.AuthID != "" && item.AuthID != snapshot.AuthID {
			continue
		}
		if !start.IsZero() && !item.Timestamp.After(start) {
			continue
		}
		if item.Timestamp.After(snapshot.ObservedAt) {
			continue
		}
		if modelFilter != "" && normalizeQuotaAuditModel(item.Model) != modelFilter {
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
		identity := strings.TrimSpace(row.Auth)
		if identity == "" {
			identity = strings.TrimSpace(row.Account)
		}
		if identity != "" {
			accounts[identity] = struct{}{}
		}
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
	for _, key := range []string{"additional_rate_limits", "additionalRateLimits"} {
		additional := root.Get(key)
		if !additional.Exists() || additional.Type != gjson.JSON {
			continue
		}
		additional.ForEach(func(index, value gjson.Result) bool {
			if value.Type == gjson.JSON {
				addRateLimit("additional:"+index.String(), value)
			}
			return true
		})
	}
	return result
}

func makeQuotaSnapshot(authID, authIndex, account, planType, window string, observedAt time.Time, shape, version string, value gjson.Result) QuotaWindowSnapshot {
	result := QuotaWindowSnapshot{AuthID: authID, AuthIndex: authIndex, Account: account, PlanType: planType, Window: window, ObservedAt: observedAt, CapturedAt: observedAt, Shape: shape, Version: version}
	for _, key := range []string{"used_percent", "usedPercent"} {
		if number, ok := quotaPercent(value.Get(key)); ok {
			result.UsedPercent = &number
			break
		}
	}
	for _, key := range []string{"remaining_percent", "remainingPercent"} {
		if number, ok := quotaPercent(value.Get(key)); ok {
			result.RemainingPercent = &number
			break
		}
	}
	if result.RemainingPercent == nil {
		for _, key := range []string{"remaining_fraction", "remainingFraction"} {
			if number, ok := quotaFraction(value.Get(key)); ok {
				percent := number * 100
				result.RemainingPercent = &percent
				break
			}
		}
	}
	for _, key := range []string{"limit_window_seconds", "limitWindowSeconds", "window_seconds", "windowSeconds"} {
		if number, ok := quotaNumber(value.Get(key)); ok && number >= 0 {
			result.WindowDurationSeconds = &number
			break
		}
	}
	if result.WindowDurationSeconds == nil {
		for _, key := range []string{"limit_window_minutes", "limitWindowMinutes", "window_minutes", "windowMinutes"} {
			if number, ok := quotaNumber(value.Get(key)); ok && number >= 0 {
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
	for _, key := range []string{"rate_limit", "rateLimit", "code_review_rate_limit", "codeReviewRateLimit", "additional_rate_limits", "additionalRateLimits"} {
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
	var parsed float64
	if value.Type == gjson.Number {
		parsed = value.Float()
	} else {
		var err error
		parsed, err = strconv.ParseFloat(strings.TrimSpace(value.String()), 64)
		if err != nil {
			return 0, false
		}
	}
	if math.IsNaN(parsed) || math.IsInf(parsed, 0) {
		return 0, false
	}
	return parsed, true
}

func quotaPercent(value gjson.Result) (float64, bool) {
	number, ok := quotaNumber(value)
	return number, ok && number >= 0 && number <= 100
}

func quotaFraction(value gjson.Result) (float64, bool) {
	number, ok := quotaNumber(value)
	return number, ok && number >= 0 && number <= 1
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
