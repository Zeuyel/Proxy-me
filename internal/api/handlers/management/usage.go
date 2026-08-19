package management

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/usage"
	coreusage "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/usage"
)

type usageExportPayload struct {
	Version    int                        `json:"version"`
	ExportedAt time.Time                  `json:"exported_at"`
	Usage      usage.StatisticsSnapshot   `json:"usage"`
	QuotaAudit coreusage.QuotaAuditExport `json:"quota_audit,omitempty"`
}

type usageImportPayload struct {
	Version    int                        `json:"version"`
	Usage      usage.StatisticsSnapshot   `json:"usage"`
	QuotaAudit coreusage.QuotaAuditExport `json:"quota_audit"`
}

// GetUsageStatistics returns the in-memory request statistics snapshot.
func (h *Handler) GetUsageStatistics(c *gin.Context) {
	var snapshot usage.StatisticsSnapshot
	if h != nil && h.usageStats != nil {
		snapshot = h.usageStats.Snapshot()
	}
	c.JSON(http.StatusOK, gin.H{
		"usage":           snapshot,
		"failed_requests": snapshot.FailureCount,
	})
}

// ExportUsageStatistics returns a complete usage snapshot for backup/migration.
func (h *Handler) ExportUsageStatistics(c *gin.Context) {
	var snapshot usage.StatisticsSnapshot
	if h != nil && h.usageStats != nil {
		snapshot = h.usageStats.Snapshot()
	}
	c.JSON(http.StatusOK, usageExportPayload{
		Version:    2,
		ExportedAt: time.Now().UTC(),
		Usage:      snapshot,
		QuotaAudit: coreusage.DefaultQuotaAuditStore().Export(),
	})
}

// ImportUsageStatistics merges a previously exported usage snapshot into memory.
func (h *Handler) ImportUsageStatistics(c *gin.Context) {
	if h == nil || h.usageStats == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "usage statistics unavailable"})
		return
	}

	data, err := c.GetRawData()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "failed to read request body"})
		return
	}

	var payload usageImportPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid json"})
		return
	}
	if payload.Version != 0 && payload.Version != 1 && payload.Version != 2 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unsupported version"})
		return
	}

	result := h.usageStats.MergeSnapshot(payload.Usage)
	quotaSnapshots, quotaUsage := coreusage.DefaultQuotaAuditStore().Merge(payload.QuotaAudit)
	snapshot := h.usageStats.Snapshot()
	c.JSON(http.StatusOK, gin.H{
		"added":           result.Added,
		"skipped":         result.Skipped,
		"total_requests":  snapshot.TotalRequests,
		"failed_requests": snapshot.FailureCount,
		"quota_snapshots": quotaSnapshots,
		"quota_usage":     quotaUsage,
	})
}

// GetQuotaAudit returns parsed Codex quota snapshots joined with usage records.
func (h *Handler) GetQuotaAudit(c *gin.Context) {
	query := coreusage.QuotaAuditQuery{
		Auth: c.Query("auth"), Account: c.Query("account"), Window: c.Query("window"), Model: c.Query("model"),
	}
	var err error
	if raw := c.Query("from"); raw != "" {
		query.From, err = time.Parse(time.RFC3339, raw)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid from timestamp"})
			return
		}
	}
	if raw := c.Query("to"); raw != "" {
		query.To, err = time.Parse(time.RFC3339, raw)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid to timestamp"})
			return
		}
	}
	c.JSON(http.StatusOK, coreusage.BuildQuotaAudit(query))
}

// SyncQuotaAuditPrices refreshes prices from the validated CCH Plus CPT v1 feed.
func (h *Handler) SyncQuotaAuditPrices(c *gin.Context) {
	if h == nil || h.priceSync == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "price synchronization unavailable", "failed": 1})
		return
	}
	result, err := h.priceSync.Sync(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{
			"error":       err.Error(),
			"source":      result.Source,
			"version":     result.Version,
			"fingerprint": result.Fingerprint,
			"etag":        result.ETag,
			"updated":     result.Updated,
			"unchanged":   result.Unchanged,
			"failed":      result.Failed,
		})
		return
	}
	c.JSON(http.StatusOK, result)
}

// PutQuotaAuditPrice stores an immutable server-side price input for a model.
func (h *Handler) PutQuotaAuditPrice(c *gin.Context) {
	var payload struct {
		Model         string                  `json:"model"`
		PriceSnapshot coreusage.PriceSnapshot `json:"price_snapshot"`
	}
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "model and price_snapshot are required"})
		return
	}
	payload.Model = strings.TrimSpace(payload.Model)
	if payload.Model == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "model and price_snapshot are required"})
		return
	}
	if payload.PriceSnapshot.InputPerMillionUSD == nil || payload.PriceSnapshot.OutputPerMillionUSD == nil ||
		!validPriceRate(payload.PriceSnapshot.InputPerMillionUSD) || !validPriceRate(payload.PriceSnapshot.OutputPerMillionUSD) ||
		!validPriceRate(payload.PriceSnapshot.ReasoningPerMillionUSD) || !validPriceRate(payload.PriceSnapshot.CachedPerMillionUSD) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "price rates must be finite and non-negative; input and output are required"})
		return
	}
	if payload.PriceSnapshot.Unit == "" {
		payload.PriceSnapshot.Unit = "usd_per_million_tokens"
	}
	if payload.PriceSnapshot.Currency == "" {
		payload.PriceSnapshot.Currency = "USD"
	}
	if !strings.EqualFold(payload.PriceSnapshot.Currency, "USD") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "currency must be USD"})
		return
	}
	if payload.PriceSnapshot.CapturedAt.IsZero() {
		payload.PriceSnapshot.CapturedAt = time.Now().UTC()
	} else {
		payload.PriceSnapshot.CapturedAt = payload.PriceSnapshot.CapturedAt.UTC()
	}
	if payload.PriceSnapshot.Source == "" {
		payload.PriceSnapshot.Source = "management-api"
	}
	if payload.PriceSnapshot.Version == "" {
		payload.PriceSnapshot.Version = "1"
	}
	computedFingerprint := priceFingerprint(payload.Model, payload.PriceSnapshot)
	if payload.PriceSnapshot.Fingerprint != "" && !strings.EqualFold(payload.PriceSnapshot.Fingerprint, computedFingerprint) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "price_snapshot fingerprint does not match its rates"})
		return
	}
	payload.PriceSnapshot.Fingerprint = computedFingerprint
	payload.PriceSnapshot.Immutable = true
	coreusage.SetManualPriceSnapshot(payload.Model, payload.PriceSnapshot)
	c.JSON(http.StatusOK, gin.H{"model": payload.Model, "price_snapshot": payload.PriceSnapshot})
}

func validPriceRate(rate *float64) bool {
	return rate == nil || (!math.IsNaN(*rate) && !math.IsInf(*rate, 0) && *rate >= 0)
}

func priceRate(rate *float64) string {
	if rate == nil {
		return "null"
	}
	return strconv.FormatFloat(*rate, 'g', -1, 64)
}

func priceFingerprint(model string, snapshot coreusage.PriceSnapshot) string {
	raw := strings.Join([]string{model, snapshot.Version,
		priceRate(snapshot.InputPerMillionUSD), priceRate(snapshot.OutputPerMillionUSD),
		priceRate(snapshot.ReasoningPerMillionUSD), priceRate(snapshot.CachedPerMillionUSD)}, "|")
	digest := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(digest[:])
}
