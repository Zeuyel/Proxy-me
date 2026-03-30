package auth

import (
	"context"
	"math"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/executor"
	"github.com/tidwall/gjson"
)

const (
	sessionIDMaxLengthSelector = 256
	codexSessionPrefixSelector = "codex_prev_"
	codexSessionMinLenSelector = 21
)

var sessionIDPatternSelector = regexp.MustCompile(`^[A-Za-z0-9_.:\-]+$`)

// SessionSelectorConfig controls session-aware routing behavior.
type SessionSelectorConfig struct {
	Enabled          bool
	Providers        []string
	TTL              time.Duration
	FailureThreshold int
	Cooldown         time.Duration
	LoadWindow       time.Duration
	LoadWeight       float64
	HealthWindow     int
	WeightSuccess    float64
	WeightQuota      float64
	Penalty429       float64
	Penalty403       float64
	Penalty5xx       float64
	PenaltyExponent  float64
	LoadBalanceMode  string
}

type sessionBinding struct {
	authID        string
	lastUsed      time.Time
	failCount     int
	cooldownUntil time.Time
}

type resultSample struct {
	timestamp time.Time
	success   bool
	status    int
}

type authStats struct {
	recentResults   []resultSample
	recentRequests  []time.Time
	pendingRequests []time.Time
}

// SessionSelector applies session stickiness with health/load scoring.
type SessionSelector struct {
	mu       sync.Mutex
	cfg      SessionSelectorConfig
	sessions map[string]*sessionBinding
	stats    map[string]*authStats
	clock    func() time.Time
}

// NewSessionSelector constructs a session-aware selector.
func NewSessionSelector(cfg SessionSelectorConfig) *SessionSelector {
	normalised := normaliseSessionConfig(cfg)
	return &SessionSelector{
		cfg:      normalised,
		sessions: make(map[string]*sessionBinding),
		stats:    make(map[string]*authStats),
		clock:    time.Now,
	}
}

// UpdateConfig refreshes selector settings without clearing runtime state.
func (s *SessionSelector) UpdateConfig(cfg SessionSelectorConfig) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.cfg = normaliseSessionConfig(cfg)
	s.mu.Unlock()
}

// RecordResult updates selector metrics for scoring and session failover.
func (s *SessionSelector) RecordResult(ctx context.Context, result Result) {
	if s == nil || result.AuthID == "" {
		return
	}
	now := s.now()
	status := 0
	if result.Error != nil {
		status = result.Error.HTTPStatus
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	stats := s.stats[result.AuthID]
	if stats == nil {
		stats = &authStats{}
		s.stats[result.AuthID] = stats
	}
	stats.recentResults = append(stats.recentResults, resultSample{
		timestamp: now,
		success:   result.Success,
		status:    status,
	})
	if s.cfg.HealthWindow > 0 && len(stats.recentResults) > s.cfg.HealthWindow {
		stats.recentResults = stats.recentResults[len(stats.recentResults)-s.cfg.HealthWindow:]
	}
	if s.cfg.LoadWindow > 0 {
		if len(stats.pendingRequests) > 0 {
			stats.pendingRequests = stats.pendingRequests[1:]
		}
		stats.recentRequests = append(stats.recentRequests, now)
		stats.recentRequests = pruneOldTimestamps(stats.recentRequests, now.Add(-s.cfg.LoadWindow))
		stats.pendingRequests = pruneOldTimestamps(stats.pendingRequests, now.Add(-s.cfg.LoadWindow))
	}

	sessionID := SessionIDFromContext(ctx)
	if sessionID == "" || !s.isProviderEnabled(result.Provider) {
		return
	}
	key := s.sessionKey(result.Provider, sessionID)
	binding := s.sessions[key]
	if binding == nil || binding.authID != result.AuthID {
		return
	}
	binding.lastUsed = now
	if result.Success {
		binding.failCount = 0
		binding.cooldownUntil = time.Time{}
		return
	}
	binding.failCount++
	if s.cfg.FailureThreshold > 0 && binding.failCount >= s.cfg.FailureThreshold {
		if s.cfg.Cooldown > 0 {
			binding.cooldownUntil = now.Add(s.cfg.Cooldown)
		}
		binding.failCount = 0
	}
}

// Pick selects an auth candidate with session stickiness and scoring.
func (s *SessionSelector) Pick(ctx context.Context, provider, model string, opts cliproxyexecutor.Options, auths []*Auth) (*Auth, error) {
	if s == nil {
		selector := &RoundRobinSelector{}
		return selector.Pick(ctx, provider, model, opts, auths)
	}

	now := s.now()
	available, err := getAvailableAuths(auths, provider, model, now)
	if err != nil {
		return nil, err
	}

	sessionID := extractSessionIDFromOptions(opts)
	providerKey := strings.TrimSpace(strings.ToLower(provider))
	isMixedProvider := providerKey == "mixed"

	s.mu.Lock()
	s.cleanupLocked(now)
	var excludedAuthID string
	if sessionID != "" {
		if isMixedProvider {
			if auth, excluded := s.resolveMixedBindingLocked(sessionID, available, now); auth != nil {
				s.trackPendingRequestLocked(auth.ID, now)
				s.mu.Unlock()
				return auth, nil
			} else {
				excludedAuthID = excluded
			}
		} else if s.isProviderEnabled(provider) {
			key := s.sessionKey(provider, sessionID)
			if binding := s.sessions[key]; binding != nil {
				if binding.lastUsed.Add(s.cfg.TTL).After(now) && binding.cooldownUntil.After(now) {
					excludedAuthID = binding.authID
				} else if binding.lastUsed.Add(s.cfg.TTL).After(now) {
					if auth := findAuthByID(available, binding.authID); auth != nil {
						binding.lastUsed = now
						s.trackPendingRequestLocked(auth.ID, now)
						s.mu.Unlock()
						return auth, nil
					}
				} else {
					delete(s.sessions, key)
				}
			}
		}
	}

	candidates := filterAuthsByID(available, excludedAuthID)
	if len(candidates) == 0 {
		candidates = available
	}

	selected := s.pickBestCandidateLocked(candidates, model, now)
	if sessionID != "" {
		bindingProvider := provider
		if isMixedProvider {
			bindingProvider = selected.Provider
		}
		if s.isProviderEnabled(bindingProvider) {
			key := s.sessionKey(bindingProvider, sessionID)
			s.sessions[key] = &sessionBinding{
				authID:   selected.ID,
				lastUsed: now,
			}
		}
	}
	s.trackPendingRequestLocked(selected.ID, now)
	s.mu.Unlock()
	return selected, nil
}

func (s *SessionSelector) now() time.Time {
	if s.clock != nil {
		return s.clock()
	}
	return time.Now()
}

func normaliseSessionConfig(cfg SessionSelectorConfig) SessionSelectorConfig {
	out := cfg
	if out.TTL <= 0 {
		out.TTL = 5 * time.Minute
	}
	if out.FailureThreshold <= 0 {
		out.FailureThreshold = 3
	}
	if out.Cooldown <= 0 {
		out.Cooldown = 5 * time.Minute
	}
	if out.LoadWindow <= 0 {
		out.LoadWindow = 10 * time.Minute
	}
	if out.LoadWeight < 0 {
		out.LoadWeight = 0
	}
	if out.LoadWeight > 1 {
		out.LoadWeight = 1
	}
	if out.HealthWindow <= 0 {
		out.HealthWindow = 50
	}
	if out.WeightSuccess <= 0 && out.WeightQuota <= 0 {
		out.WeightSuccess = 0.6
		out.WeightQuota = 0.4
	}
	if out.Penalty429 <= 0 {
		out.Penalty429 = 1.0
	}
	if out.Penalty403 <= 0 {
		out.Penalty403 = 0.7
	}
	if out.Penalty5xx <= 0 {
		out.Penalty5xx = 0.4
	}
	if out.PenaltyExponent <= 0 {
		out.PenaltyExponent = 1.0
	}
	if out.LoadBalanceMode == "" {
		out.LoadBalanceMode = "exponential"
	}
	return out
}

func (s *SessionSelector) isProviderEnabled(provider string) bool {
	if len(s.cfg.Providers) == 0 {
		return true
	}
	for _, name := range s.cfg.Providers {
		if strings.EqualFold(strings.TrimSpace(name), provider) {
			return true
		}
	}
	return false
}

func (s *SessionSelector) sessionKey(provider, sessionID string) string {
	return strings.ToLower(provider) + ":" + sessionID
}

func (s *SessionSelector) cleanupLocked(now time.Time) {
	ttlCutoff := now.Add(-s.cfg.TTL)
	for key, binding := range s.sessions {
		if binding == nil || binding.lastUsed.Before(ttlCutoff) {
			delete(s.sessions, key)
		}
	}
	if s.cfg.LoadWindow <= 0 {
		return
	}
	cutoff := now.Add(-s.cfg.LoadWindow)
	for _, stats := range s.stats {
		stats.recentRequests = pruneOldTimestamps(stats.recentRequests, cutoff)
		stats.pendingRequests = pruneOldTimestamps(stats.pendingRequests, cutoff)
	}
}

func (s *SessionSelector) resolveMixedBindingLocked(sessionID string, available []*Auth, now time.Time) (*Auth, string) {
	if sessionID == "" || len(available) == 0 {
		return nil, ""
	}

	availableByID := make(map[string]*Auth, len(available))
	providerSet := make(map[string]struct{}, len(available))
	for _, auth := range available {
		if auth == nil || auth.ID == "" {
			continue
		}
		availableByID[auth.ID] = auth
		providerKey := strings.TrimSpace(strings.ToLower(auth.Provider))
		if providerKey == "" {
			continue
		}
		providerSet[providerKey] = struct{}{}
	}

	var (
		stickyAuth     *Auth
		stickyBinding  *sessionBinding
		stickyLastUsed time.Time
		excludedAuthID string
		excludedUsed   time.Time
	)

	for providerKey := range providerSet {
		if !s.isProviderEnabled(providerKey) {
			continue
		}
		key := s.sessionKey(providerKey, sessionID)
		binding := s.sessions[key]
		if binding == nil {
			continue
		}
		if !binding.lastUsed.Add(s.cfg.TTL).After(now) {
			delete(s.sessions, key)
			continue
		}
		auth := availableByID[binding.authID]
		if auth == nil {
			continue
		}
		if binding.cooldownUntil.After(now) {
			if excludedAuthID == "" || binding.lastUsed.After(excludedUsed) {
				excludedAuthID = binding.authID
				excludedUsed = binding.lastUsed
			}
			continue
		}
		if stickyBinding == nil || binding.lastUsed.After(stickyLastUsed) {
			stickyAuth = auth
			stickyBinding = binding
			stickyLastUsed = binding.lastUsed
		}
	}

	if stickyAuth != nil && stickyBinding != nil {
		stickyBinding.lastUsed = now
		return stickyAuth, ""
	}
	return nil, excludedAuthID
}

func (s *SessionSelector) trackPendingRequestLocked(authID string, now time.Time) {
	if s.cfg.LoadWindow <= 0 || authID == "" {
		return
	}
	stats := s.stats[authID]
	if stats == nil {
		stats = &authStats{}
		s.stats[authID] = stats
	}
	stats.pendingRequests = append(stats.pendingRequests, now)
	stats.pendingRequests = pruneOldTimestamps(stats.pendingRequests, now.Add(-s.cfg.LoadWindow))
}

func (s *SessionSelector) pickBestCandidateLocked(candidates []*Auth, model string, now time.Time) *Auth {
	if len(candidates) == 1 {
		return candidates[0]
	}
	type scored struct {
		auth  *Auth
		score float64
		load  int
	}
	scoredList := make([]scored, 0, len(candidates))
	for _, auth := range candidates {
		score, load := s.scoreAuthLocked(auth, model, now)
		scoredList = append(scoredList, scored{auth: auth, score: score, load: load})
	}

	sort.Slice(scoredList, func(i, j int) bool {
		if math.Abs(scoredList[i].score-scoredList[j].score) > 0.0001 {
			return scoredList[i].score > scoredList[j].score
		}
		if scoredList[i].load != scoredList[j].load {
			return scoredList[i].load < scoredList[j].load
		}
		return scoredList[i].auth.ID < scoredList[j].auth.ID
	})
	return scoredList[0].auth
}

func (s *SessionSelector) scoreAuthLocked(auth *Auth, model string, now time.Time) (float64, int) {
	stats := s.stats[auth.ID]
	successRate := 0.5
	penaltyRatio := 0.0
	loadCount := 0

	if stats != nil {
		recent := stats.recentResults
		if len(recent) > 0 {
			var successCount, count429, count403, count5xx int
			for _, sample := range recent {
				if sample.success {
					successCount++
					continue
				}
				switch {
				case sample.status == 429:
					count429++
				case sample.status == 403 || sample.status == 402:
					count403++
				case sample.status >= 500 && sample.status <= 599:
					count5xx++
				}
			}
			successRate = float64(successCount) / float64(len(recent))

			// 计算加权错误率
			errorRatio := (float64(count429)*s.cfg.Penalty429 +
				float64(count403)*s.cfg.Penalty403 +
				float64(count5xx)*s.cfg.Penalty5xx) / float64(len(recent))

			// 指数惩罚：1 - exp(-k * errorRatio)
			// 当 errorRatio 很小时，惩罚接近 0
			// 当 errorRatio 增大时，惩罚指数增长
			penaltyRatio = 1.0 - math.Exp(-s.cfg.PenaltyExponent*errorRatio)
			if penaltyRatio < 0 {
				penaltyRatio = 0
			}
			if penaltyRatio > 1 {
				penaltyRatio = 1
			}
		}
		if s.cfg.LoadWindow > 0 {
			stats.recentRequests = pruneOldTimestamps(stats.recentRequests, now.Add(-s.cfg.LoadWindow))
			loadCount = len(stats.recentRequests)
			stats.pendingRequests = pruneOldTimestamps(stats.pendingRequests, now.Add(-s.cfg.LoadWindow))
			loadCount += len(stats.pendingRequests)
		}
	}

	quotaScore := quotaHealth(auth, model, now)
	weightTotal := s.cfg.WeightSuccess + s.cfg.WeightQuota
	weighted := 0.0
	if weightTotal > 0 {
		weighted = (successRate*s.cfg.WeightSuccess + quotaScore*s.cfg.WeightQuota) / weightTotal
	}

	// 负载惩罚计算
	loadPenalty := 0.0
	if loadCount > 0 {
		if s.cfg.LoadBalanceMode == "exponential" {
			// 计算平均负载
			avgLoad := 0.0
			validCount := 0
			for _, st := range s.stats {
				if st != nil {
					count := len(st.recentRequests) + len(st.pendingRequests)
					avgLoad += float64(count)
					validCount++
				}
			}
			if validCount > 0 {
				avgLoad /= float64(validCount)
			}
			if avgLoad < 1.0 {
				avgLoad = 1.0 // 避免除零
			}

			// 指数负载惩罚：1 - exp(-LoadWeight * loadCount / avgLoad)
			// 低于平均负载：惩罚很小
			// 高于平均负载：惩罚快速增长
			loadPenalty = 1.0 - math.Exp(-s.cfg.LoadWeight*float64(loadCount)/avgLoad)
			if loadPenalty < 0 {
				loadPenalty = 0
			}
			if loadPenalty > 1 {
				loadPenalty = 1
			}
		} else {
			// 线性模式（向后兼容）
			loadPenalty = float64(loadCount) / float64(loadCount+1)
		}
	}

	score := weighted - loadPenalty
	score = score * (1.0 - penaltyRatio)
	if score < 0 {
		score = 0
	}
	return score, loadCount
}

func quotaHealth(auth *Auth, model string, now time.Time) float64 {
	if auth == nil {
		return 0
	}
	quota := auth.Quota
	if model != "" && auth.ModelStates != nil {
		if state := auth.ModelStates[model]; state != nil {
			quota = state.Quota
		}
	}
	if quota.Exceeded {
		return 0
	}
	if !quota.NextRecoverAt.IsZero() && quota.NextRecoverAt.After(now) {
		return 0.2
	}
	return 1
}

func extractSessionIDFromOptions(opts cliproxyexecutor.Options) string {
	if opts.Metadata != nil {
		raw, ok := opts.Metadata[cliproxyexecutor.SessionIDMetadataKey]
		if ok {
			if value, ok := raw.(string); ok {
				return strings.TrimSpace(value)
			}
		}
	}
	if opts.Headers != nil {
		if value := sanitizeCodexSessionIDSelector(opts.Headers.Get("session_id")); value != "" {
			return value
		}
		if value := sanitizeCodexSessionIDSelector(opts.Headers.Get("x-session-id")); value != "" {
			return value
		}
	}
	if value := extractSessionIDFromOriginalRequest(opts.OriginalRequest); value != "" {
		return value
	}
	return ""
}

func sanitizeSessionIDSelector(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	if len(trimmed) > sessionIDMaxLengthSelector {
		return ""
	}
	if !sessionIDPatternSelector.MatchString(trimmed) {
		return ""
	}
	return trimmed
}

func sanitizeCodexSessionIDSelector(value string) string {
	normalized := sanitizeSessionIDSelector(value)
	if len(normalized) < codexSessionMinLenSelector {
		return ""
	}
	return normalized
}

func extractSessionIDFromOriginalRequest(rawJSON []byte) string {
	if len(rawJSON) == 0 {
		return ""
	}
	if value := sanitizeCodexSessionIDSelector(gjson.GetBytes(rawJSON, "prompt_cache_key").String()); value != "" {
		return value
	}
	if value := sanitizeCodexSessionIDSelector(gjson.GetBytes(rawJSON, "metadata.session_id").String()); value != "" {
		return value
	}
	if value := sanitizeCodexSessionIDSelector(gjson.GetBytes(rawJSON, "previous_response_id").String()); value != "" {
		if prefixed := codexSessionPrefixSelector + value; len(prefixed) <= sessionIDMaxLengthSelector {
			return prefixed
		}
	}
	if value := sanitizeSessionIDSelector(gjson.GetBytes(rawJSON, "metadata.session_id").String()); value != "" {
		return value
	}
	return ""
}

func findAuthByID(auths []*Auth, id string) *Auth {
	if id == "" {
		return nil
	}
	for _, auth := range auths {
		if auth != nil && auth.ID == id {
			return auth
		}
	}
	return nil
}

func filterAuthsByID(auths []*Auth, excludedID string) []*Auth {
	if excludedID == "" {
		return auths
	}
	filtered := make([]*Auth, 0, len(auths))
	for _, auth := range auths {
		if auth == nil || auth.ID == excludedID {
			continue
		}
		filtered = append(filtered, auth)
	}
	return filtered
}

func pruneOldTimestamps(values []time.Time, cutoff time.Time) []time.Time {
	if len(values) == 0 {
		return values
	}
	idx := 0
	for idx < len(values) && values[idx].Before(cutoff) {
		idx++
	}
	if idx == 0 {
		return values
	}
	return append([]time.Time(nil), values[idx:]...)
}

func clamp01(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}

// SessionSelectorHook forwards execution results to the selector.
type SessionSelectorHook struct {
	Selector *SessionSelector
}

// OnAuthRegistered implements Hook.
func (SessionSelectorHook) OnAuthRegistered(context.Context, *Auth) {}

// OnAuthUpdated implements Hook.
func (SessionSelectorHook) OnAuthUpdated(context.Context, *Auth) {}

// OnResult implements Hook.
func (h SessionSelectorHook) OnResult(ctx context.Context, result Result) {
	if h.Selector == nil {
		return
	}
	h.Selector.RecordResult(ctx, result)
}
