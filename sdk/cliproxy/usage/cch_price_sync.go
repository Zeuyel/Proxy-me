package usage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	CCHPlusModelsURL = "https://cch-plus.com/pricing/v1/models.json"
	CCHPlusSchemaURL = "https://cch-plus.com/pricing/v1/models.schema.json"

	cchPlusSource     = "cch-plus"
	priceSyncTimeout  = 10 * time.Second
	maxPriceDocument  = 32 << 20
	maxPriceRedirects = 3
	priceSnapshotUnit = "usd_per_million_tokens"
	priceSnapshotCurr = "USD"
)

var errPriceNotModified = errors.New("price document not modified")

// PriceSyncResult is the management-safe summary of one remote price refresh.
// It intentionally contains no model table or source response body.
type PriceSyncResult struct {
	Source      string `json:"source"`
	Version     string `json:"version,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"`
	ETag        string `json:"etag,omitempty"`
	Updated     int    `json:"updated"`
	Unchanged   int    `json:"unchanged"`
	Failed      int    `json:"failed"`
}

// CCHPriceSync fetches and validates the CCH Plus CPT v1 price documents.
// A sync is serialized so a manual trigger cannot race another trigger.
type CCHPriceSync struct {
	store  *QuotaAuditStore
	client *http.Client

	mu              sync.Mutex
	lastVersion     string
	lastFingerprint string
	lastETag        string
	lastModelCount  int
}

func NewCCHPriceSync(store *QuotaAuditStore) *CCHPriceSync {
	return NewCCHPriceSyncWithClient(store, nil)
}

// NewCCHPriceSyncWithClient is useful for tests and embedders that need a custom
// transport. The configured endpoint and redirect policy remain fixed by this type.
func NewCCHPriceSyncWithClient(store *QuotaAuditStore, client *http.Client) *CCHPriceSync {
	if store == nil {
		store = DefaultQuotaAuditStore()
	}
	if client == nil {
		client = &http.Client{}
	}
	copyClient := *client
	copyClient.CheckRedirect = restrictedRedirect
	if copyClient.Timeout == 0 || copyClient.Timeout > priceSyncTimeout {
		copyClient.Timeout = priceSyncTimeout
	}
	return &CCHPriceSync{store: store, client: &copyClient}
}

// Sync fetches both documents and applies only validated remote prices.
// A document-level failure leaves all existing prices untouched.
func (s *CCHPriceSync) Sync(ctx context.Context) (PriceSyncResult, error) {
	result := PriceSyncResult{Source: cchPlusSource}
	if s == nil || s.store == nil {
		result.Failed = 1
		return result, errors.New("price sync is unavailable")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if ctx == nil {
		ctx = context.Background()
	}
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, priceSyncTimeout)
		defer cancel()
	}

	schemaBody, _, err := s.fetch(ctx, CCHPlusSchemaURL, "schema")
	if err != nil {
		result.Failed = 1
		return result, err
	}
	schemaVersion, err := validateCPTSchema(schemaBody)
	if err != nil {
		result.Failed = 1
		return result, err
	}
	modelsBody, headers, err := s.fetch(ctx, CCHPlusModelsURL, "models")
	if errors.Is(err, errPriceNotModified) && s.lastFingerprint != "" {
		result.Version = s.lastVersion
		result.Fingerprint = s.lastFingerprint
		result.ETag = strings.TrimSpace(headers.Get("ETag"))
		result.Unchanged = s.lastModelCount
		return result, nil
	}
	if err != nil {
		result.Failed = 1
		return result, err
	}
	version, prices, failed, err := parseCPTPrices(modelsBody, schemaVersion)
	result.Version = version
	result.ETag = strings.TrimSpace(headers.Get("ETag"))
	result.Failed = failed
	if err != nil {
		result.Failed++
		return result, err
	}
	if len(prices) == 0 {
		result.Failed++
		return result, errors.New("price document contains no valid models")
	}
	result.Fingerprint = priceTableFingerprint(version, prices)
	if result.Fingerprint == s.lastFingerprint && version == s.lastVersion {
		result.Unchanged = len(prices)
		s.lastETag = result.ETag
		return result, nil
	}

	models := make([]string, 0, len(prices))
	for model := range prices {
		models = append(models, model)
	}
	sort.Strings(models)
	for _, model := range models {
		incoming := prices[model]
		current, exists, manual := s.store.priceSnapshot(model)
		if manual || (exists && current.Fingerprint == incoming.Fingerprint && current.Version == incoming.Version) {
			result.Unchanged++
			continue
		}
		if s.store.SetSyncedPriceSnapshot(model, incoming) {
			result.Updated++
		} else {
			result.Unchanged++
		}
	}
	s.lastVersion = version
	s.lastFingerprint = result.Fingerprint
	s.lastETag = result.ETag
	s.lastModelCount = len(prices)
	return result, nil
}

func (s *CCHPriceSync) fetch(ctx context.Context, rawURL, kind string) ([]byte, http.Header, error) {
	u, err := url.Parse(rawURL)
	if err != nil || !isAllowedCCHURL(u, rawURL) {
		return nil, nil, fmt.Errorf("invalid %s endpoint", kind)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("create %s request: %w", kind, err)
	}
	req.Header.Set("Accept", "application/json")
	if kind == "models" && s.lastETag != "" {
		req.Header.Set("If-None-Match", s.lastETag)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("fetch %s: %w", kind, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotModified {
		return nil, resp.Header, errPriceNotModified
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, resp.Header, fmt.Errorf("fetch %s: unexpected status %d", kind, resp.StatusCode)
	}
	limited := io.LimitReader(resp.Body, maxPriceDocument+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, resp.Header, fmt.Errorf("read %s: %w", kind, err)
	}
	if len(body) > maxPriceDocument {
		return nil, resp.Header, fmt.Errorf("%s document exceeds size limit", kind)
	}
	return body, resp.Header, nil
}

func restrictedRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= maxPriceRedirects {
		return errors.New("too many price document redirects")
	}
	if req == nil || req.URL == nil || !isAllowedCCHURL(req.URL, "") {
		return errors.New("price document redirect is outside the allowed origin")
	}
	if len(via) > 0 && req.URL.EscapedPath() != via[0].URL.EscapedPath() {
		return errors.New("price document redirect changed path")
	}
	return nil
}

func isAllowedCCHURL(u *url.URL, raw string) bool {
	if u == nil || !strings.EqualFold(u.Scheme, "https") || !strings.EqualFold(u.Host, "cch-plus.com") {
		return false
	}
	path := u.EscapedPath()
	return path == "/pricing/v1/models.json" || path == "/pricing/v1/models.schema.json"
}

func validateCPTSchema(raw []byte) (string, error) {
	var schema map[string]any
	if err := decodeJSON(raw, &schema); err != nil {
		return "", fmt.Errorf("invalid pricing schema json: %w", err)
	}
	if schema == nil {
		return "", errors.New("pricing schema must be an object")
	}
	if typ, ok := stringValue(schema["type"]); ok && typ != "object" {
		return "", errors.New("pricing schema root must be an object")
	}
	if _, ok := schema["properties"]; !ok {
		if _, ok := schema["$defs"]; !ok {
			return "", errors.New("pricing schema has no object definition")
		}
	}
	version := versionFromValue(schema["version"])
	if version == "" {
		if properties, ok := schema["properties"].(map[string]any); ok {
			if versionSpec, ok := properties["version"].(map[string]any); ok {
				version = versionFromValue(versionSpec["const"])
				if version == "" {
					if values, ok := versionSpec["enum"].([]any); ok && len(values) == 1 {
						version = versionFromValue(values[0])
					}
				}
			}
		}
	}
	if version == "" {
		if id, ok := stringValue(schema["$id"]); ok && (strings.Contains(strings.ToLower(id), "/v1") || strings.Contains(strings.ToLower(id), "pricing-table/v1")) {
			version = "v1"
		}
	}
	if version == "" {
		version = "1"
	}
	if !isV1(version) && !strings.Contains(strings.ToLower(version), "/v1") && !strings.Contains(strings.ToLower(version), "pricing-table/v1") {
		if id, ok := stringValue(schema["$id"]); ok && (strings.Contains(strings.ToLower(id), "/v1") || strings.Contains(strings.ToLower(id), "pricing-table/v1")) {
			version = "v1"
		} else {
			return "", fmt.Errorf("unsupported pricing schema version %q", version)
		}
	}
	if strings.Contains(strings.ToLower(version), "/v1") || strings.Contains(strings.ToLower(version), "pricing-table/v1") {
		return "v1", nil
	}
	if !isV1(version) {
		return "", fmt.Errorf("unsupported pricing schema version %q", version)
	}
	return version, nil
}

func parseCPTPrices(raw []byte, schemaVersion string) (string, map[string]PriceSnapshot, int, error) {
	var root map[string]any
	if err := decodeJSON(raw, &root); err != nil {
		return "", nil, 0, fmt.Errorf("invalid pricing json: %w", err)
	}
	if root == nil {
		return "", nil, 0, errors.New("pricing document must be an object")
	}
	version := versionFromValue(root["version"])
	if version == "" {
		version = versionFromValue(root["schema_version"])
	}
	if version == "" {
		return "", nil, 0, errors.New("pricing document version is missing")
	}
	if !validCPTVersion(version, schemaVersion) {
		return "", nil, 0, fmt.Errorf("unsupported pricing document version %q", version)
	}
	models := root["models"]
	if models == nil {
		models = root["data"]
	}
	if models == nil {
		models = root["pricing"]
	}
	entries := make([]remoteModel, 0)
	switch value := models.(type) {
	case []any:
		for _, item := range value {
			if obj, ok := item.(map[string]any); ok {
				entries = append(entries, remoteModel{fields: obj})
			}
		}
	case map[string]any:
		for name, item := range value {
			obj, ok := item.(map[string]any)
			if !ok {
				continue
			}
			entries = append(entries, remoteModel{name: name, fields: obj})
		}
	default:
		return "", nil, 0, errors.New("pricing document models must be an array or object")
	}

	tableUnit := cchFirstString(root, "unit", "pricing_unit", "price_unit")
	prices := make(map[string]PriceSnapshot, len(entries))
	failed := 0
	for _, entry := range entries {
		model, snapshot, ok := parseRemoteModel(entry, version, tableUnit)
		if !ok {
			failed++
			continue
		}
		if _, duplicate := prices[model]; duplicate {
			failed++
			continue
		}
		prices[model] = snapshot
	}
	return version, prices, failed, nil
}

type remoteModel struct {
	name   string
	fields map[string]any
}

func parseRemoteModel(entry remoteModel, version, tableUnit string) (string, PriceSnapshot, bool) {
	model := ""
	for _, key := range []string{"model_name", "slug", "id", "name", "model"} {
		if value, ok := stringValue(entry.fields[key]); ok {
			model = normalizeRemoteModelName(value)
			break
		}
	}
	if model == "" {
		model = normalizeRemoteModelName(entry.name)
	}
	model = normalizeRemoteModelName(model)
	if model == "" || entry.fields == nil {
		return "", PriceSnapshot{}, false
	}
	pricing := entry.fields
	if tiers, ok := entry.fields["pricing"].([]any); ok {
		if selected, ok := selectPricingTier(tiers); ok {
			pricing = mergePriceFields(entry.fields, selected)
		}
	} else {
		for _, key := range []string{"pricing", "prices", "cost", "rates"} {
			if nested, ok := entry.fields[key].(map[string]any); ok {
				pricing = mergePriceFields(entry.fields, nested)
				break
			}
		}
	}
	if charges, ok := pricing["charges"].(map[string]any); ok {
		pricing = mergePriceFields(pricing, charges)
	}
	input, inputUnit, inputOK := readPriceWithUnit(pricing, "input", "input_price", "input_cost", "prompt", "prompt_price", "prompt_cost")
	output, outputUnit, outputOK := readPriceWithUnit(pricing, "output", "output_price", "output_cost", "completion", "completion_price", "completion_cost")
	if !inputOK || !outputOK {
		return "", PriceSnapshot{}, false
	}
	reasoning, reasoningUnit, reasoningOK := readPriceWithUnit(pricing, "reasoning", "reasoning_price", "reasoning_cost", "thinking", "thinking_price")
	cached, cachedUnit, cachedOK := readPriceWithUnit(pricing, "cached", "cached_price", "cache", "cache_read", "cache_read_price", "cached_input")
	unit := cchFirstString(entry.fields, "unit", "pricing_unit", "price_unit")
	if unit == "" {
		unit = tableUnit
	}
	input = convertToMillion(input, firstNonEmpty(inputUnit, unit))
	output = convertToMillion(output, firstNonEmpty(outputUnit, unit))
	if reasoningOK {
		reasoning = convertToMillion(reasoning, firstNonEmpty(reasoningUnit, unit))
	}
	if cachedOK {
		cached = convertToMillion(cached, firstNonEmpty(cachedUnit, unit))
	}
	if !validRemoteRate(input) || !validRemoteRate(output) || (reasoningOK && !validRemoteRate(reasoning)) || (cachedOK && !validRemoteRate(cached)) {
		return "", PriceSnapshot{}, false
	}
	snapshot := PriceSnapshot{
		InputPerMillionUSD:  &input,
		OutputPerMillionUSD: &output,
		Currency:            priceSnapshotCurr,
		Source:              cchPlusSource,
		Version:             version,
		Unit:                priceSnapshotUnit,
		Immutable:           true,
		CapturedAt:          time.Now().UTC(),
	}
	if reasoningOK {
		snapshot.ReasoningPerMillionUSD = &reasoning
	}
	if cachedOK {
		snapshot.CachedPerMillionUSD = &cached
	}
	snapshot.Fingerprint = priceModelFingerprint(model, snapshot)
	return model, snapshot, true
}

func normalizeRemoteModelName(model string) string {
	model = normalizeQuotaAuditModel(model)
	for _, prefix := range []string{"openai/", "openai:", "cch-plus/", "cch-plus:"} {
		if strings.HasPrefix(model, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(model, prefix))
		}
	}
	return model
}

func mergePriceFields(base, nested map[string]any) map[string]any {
	merged := make(map[string]any, len(base)+len(nested))
	for key, value := range base {
		merged[key] = value
	}
	for key, value := range nested {
		merged[key] = value
	}
	return merged
}

func readPriceWithUnit(fields map[string]any, keys ...string) (float64, string, bool) {
	for _, key := range keys {
		value, exists := fields[key]
		if !exists {
			continue
		}
		if number, ok := numberValue(value); ok {
			return number, "", true
		}
		if nested, ok := value.(map[string]any); ok {
			unit := cchFirstString(nested, "unit", "pricing_unit", "price_unit")
			for _, nestedKey := range []string{"price", "cost", "usd", "per_million", "per_1m_tokens", "value"} {
				if number, ok := numberValue(nested[nestedKey]); ok {
					return number, unit, true
				}
			}
		}
	}
	return 0, "", false
}

func selectPricingTier(tiers []any) (map[string]any, bool) {
	// Prefer explicit official pricing, then OpenAI/CCH tiers, with feed order as the final tie-breaker.
	var selected map[string]any
	selectedRank := 100
	for _, item := range tiers {
		tier, ok := item.(map[string]any)
		if !ok {
			continue
		}
		rank := 10
		if official, ok := tier["official"].(bool); ok && official {
			rank = 0
		}
		provider := strings.ToLower(strings.TrimSpace(cchFirstString(tier, "provider", "source", "vendor")))
		switch provider {
		case "official":
			rank = minInt(rank, 1)
		case "openai":
			rank = minInt(rank, 2)
		case "cch", "cch-plus", "cchplus":
			rank = minInt(rank, 3)
		}
		if _, hasCharges := tier["charges"]; !hasCharges {
			continue
		}
		if selected == nil || rank < selectedRank {
			selected = tier
			selectedRank = rank
		}
	}
	return selected, selected != nil
}

func minInt(first, second int) int {
	if first < second {
		return first
	}
	return second
}

func firstNonEmpty(first, second string) string {
	if strings.TrimSpace(first) != "" {
		return first
	}
	return second
}

func convertToMillion(value float64, unit string) float64 {
	normalized := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(unit), " ", ""))
	switch {
	case strings.Contains(normalized, "per_m_tokens"), strings.Contains(normalized, "per_1m"), strings.Contains(normalized, "per_million"):
		return value
	case strings.Contains(normalized, "per_token"), strings.Contains(normalized, "/token"), strings.Contains(normalized, "token") && !strings.Contains(normalized, "million") && !strings.Contains(normalized, "1m"):
		return value * 1_000_000
	case strings.Contains(normalized, "per_1k"), strings.Contains(normalized, "/1k"), strings.Contains(normalized, "1000"):
		return value * 1000
	default:
		return value
	}
}

func validRemoteRate(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0
}

func priceTableFingerprint(version string, prices map[string]PriceSnapshot) string {
	models := make([]string, 0, len(prices))
	for model := range prices {
		models = append(models, model)
	}
	sort.Strings(models)
	parts := make([]string, 0, len(models)+1)
	parts = append(parts, version)
	for _, model := range models {
		price := prices[model]
		parts = append(parts, model+"="+priceModelFingerprint(model, price))
	}
	digest := sha256.Sum256([]byte(strings.Join(parts, "|")))
	return hex.EncodeToString(digest[:])
}

func priceModelFingerprint(model string, snapshot PriceSnapshot) string {
	parts := []string{model, snapshot.Version, priceRateString(snapshot.InputPerMillionUSD), priceRateString(snapshot.OutputPerMillionUSD), priceRateString(snapshot.ReasoningPerMillionUSD), priceRateString(snapshot.CachedPerMillionUSD)}
	digest := sha256.Sum256([]byte(strings.Join(parts, "|")))
	return hex.EncodeToString(digest[:])
}

func priceRateString(value *float64) string {
	if value == nil {
		return "null"
	}
	return strconv.FormatFloat(*value, 'g', -1, 64)
}

func decodeJSON(raw []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("multiple json values")
		}
		return err
	}
	return nil
}

func cchFirstString(fields map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := stringValue(fields[key]); ok {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func stringValue(value any) (string, bool) {
	switch value := value.(type) {
	case string:
		return value, strings.TrimSpace(value) != ""
	case json.Number:
		return value.String(), true
	case float64:
		return strconv.FormatFloat(value, 'g', -1, 64), true
	default:
		return "", false
	}
}

func versionFromValue(value any) string {
	version, _ := stringValue(value)
	return strings.TrimSpace(version)
}

func numberValue(value any) (float64, bool) {
	var number float64
	switch value := value.(type) {
	case json.Number:
		parsed, err := strconv.ParseFloat(value.String(), 64)
		if err != nil {
			return 0, false
		}
		number = parsed
	case float64:
		number = value
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
		if err != nil {
			return 0, false
		}
		number = parsed
	default:
		return 0, false
	}
	return number, validRemoteRate(number)
}

func isV1(version string) bool { return sameMajorVersion(version, "1") }

func validCPTVersion(version, schemaVersion string) bool {
	if strings.TrimSpace(version) == "" {
		return false
	}
	if isV1(version) {
		return true
	}
	if strings.EqualFold(strings.TrimSpace(schemaVersion), "v1") || isV1(schemaVersion) {
		for _, character := range version {
			if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9') || strings.ContainsRune("._:-", character) {
				continue
			}
			return false
		}
		return true
	}
	return sameMajorVersion(version, schemaVersion)
}

func sameMajorVersion(first, second string) bool {
	major := func(value string) string {
		value = strings.TrimSpace(strings.TrimPrefix(strings.ToLower(value), "v"))
		if index := strings.IndexByte(value, '.'); index >= 0 {
			value = value[:index]
		}
		return value
	}
	return major(first) != "" && major(first) == major(second)
}
