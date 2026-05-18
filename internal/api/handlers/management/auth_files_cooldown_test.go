package management

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
)

func TestResolveAuthCooldown_PrefersEarliestRecoverTime(t *testing.T) {
	now := time.Now()
	weekly := now.Add(7 * 24 * time.Hour)
	fiveHours := now.Add(5 * time.Hour)
	auth := &coreauth.Auth{
		Quota: coreauth.QuotaState{
			Exceeded:      true,
			Reason:        "codex_weekly_limit",
			NextRecoverAt: weekly,
		},
		ModelStates: map[string]*coreauth.ModelState{
			"gpt-5": {
				Quota: coreauth.QuotaState{
					Exceeded:      true,
					Reason:        "codex_5h_limit",
					NextRecoverAt: fiveHours,
				},
			},
		},
	}

	active, reason, until := resolveAuthCooldown(auth, now)
	if !active {
		t.Fatalf("expected cooldown to be active")
	}
	if reason != "codex_5h_limit" {
		t.Fatalf("expected earliest reason codex_5h_limit, got %q", reason)
	}
	if !until.Equal(fiveHours) {
		t.Fatalf("expected earliest recover time %v, got %v", fiveHours, until)
	}
}

func TestResolveAuthCooldown_IgnoresExpiredQuota(t *testing.T) {
	now := time.Now()
	auth := &coreauth.Auth{
		Quota: coreauth.QuotaState{
			Exceeded:      true,
			Reason:        "codex_5h_limit",
			NextRecoverAt: now.Add(-1 * time.Minute),
		},
	}
	active, reason, until := resolveAuthCooldown(auth, now)
	if active {
		t.Fatalf("expected cooldown to be inactive, got reason=%q until=%v", reason, until)
	}
}

func TestResolveAuthCooldown_IncludesTemporaryRetryWindows(t *testing.T) {
	now := time.Now()
	authRetry := now.Add(20 * time.Minute)
	modelRetry := now.Add(5 * time.Minute)
	auth := &coreauth.Auth{
		NextRetryAfter: authRetry,
		ModelStates: map[string]*coreauth.ModelState{
			"gpt-5": {
				StatusMessage:  "transient upstream error",
				NextRetryAfter: modelRetry,
			},
		},
	}

	active, reason, until := resolveAuthCooldown(auth, now)
	if !active {
		t.Fatalf("expected temporary retry window to be treated as cooldown")
	}
	if reason != "transient upstream error" {
		t.Fatalf("expected model status message to be used as reason, got %q", reason)
	}
	if !until.Equal(modelRetry) {
		t.Fatalf("expected earliest retry time %v, got %v", modelRetry, until)
	}
}

func TestResetAuthFileCooldown_ByNameClearsRuntimeCooldowns(t *testing.T) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	store := &memoryAuthStore{}
	manager := coreauth.NewManager(store, nil, nil)
	retryAt := time.Now().Add(10 * time.Minute)
	auth := &coreauth.Auth{
		ID:             "auth-1",
		FileName:       "foo.json",
		Provider:       "claude",
		Status:         coreauth.StatusError,
		StatusMessage:  "transient upstream error",
		Unavailable:    true,
		NextRetryAfter: retryAt,
		Quota: coreauth.QuotaState{
			Exceeded:      true,
			Reason:        "codex_5h_limit",
			NextRecoverAt: retryAt,
		},
	}
	if _, err := manager.Register(context.Background(), auth); err != nil {
		t.Fatalf("register auth: %v", err)
	}

	h := &Handler{authManager: manager}
	r := gin.New()
	r.POST("/reset", h.ResetAuthFileCooldown)

	body := bytes.NewBufferString(`{"name":"foo.json","include_quota":true}`)
	req := httptest.NewRequest(http.MethodPost, "/reset", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	var payload struct {
		Status       string   `json:"status"`
		Reset        int      `json:"reset"`
		IncludeQuota bool     `json:"include_quota"`
		Targets      []string `json:"targets"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.Status != "ok" || payload.Reset != 1 || !payload.IncludeQuota {
		t.Fatalf("unexpected response payload: %+v", payload)
	}
	if len(payload.Targets) != 1 || payload.Targets[0] != "foo.json" {
		t.Fatalf("unexpected targets: %+v", payload.Targets)
	}

	updated, ok := manager.GetByID(auth.ID)
	if !ok || updated == nil {
		t.Fatalf("expected auth to exist after reset")
	}
	if updated.Unavailable || !updated.NextRetryAfter.IsZero() {
		t.Fatalf("expected auth transient cooldown to be cleared")
	}
	if updated.Quota.Exceeded || !updated.Quota.NextRecoverAt.IsZero() {
		t.Fatalf("expected auth quota cooldown to be cleared")
	}
	if updated.Status != coreauth.StatusActive {
		t.Fatalf("expected auth status active, got %s", updated.Status)
	}
}

func TestResetAuthFileCooldown_AllCanPreserveQuotaCooldowns(t *testing.T) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	store := &memoryAuthStore{}
	manager := coreauth.NewManager(store, nil, nil)
	retryAt := time.Now().Add(10 * time.Minute)

	transientAuth := &coreauth.Auth{
		ID:             "auth-transient",
		FileName:       "transient.json",
		Provider:       "claude",
		Status:         coreauth.StatusError,
		StatusMessage:  "transient upstream error",
		Unavailable:    true,
		NextRetryAfter: retryAt,
	}
	quotaAuth := &coreauth.Auth{
		ID:          "auth-quota",
		FileName:    "quota.json",
		Provider:    "codex",
		Status:      coreauth.StatusError,
		Unavailable: true,
		Quota: coreauth.QuotaState{
			Exceeded:      true,
			Reason:        "codex_5h_limit",
			NextRecoverAt: retryAt,
		},
	}
	if _, err := manager.Register(context.Background(), transientAuth); err != nil {
		t.Fatalf("register transient auth: %v", err)
	}
	if _, err := manager.Register(context.Background(), quotaAuth); err != nil {
		t.Fatalf("register quota auth: %v", err)
	}

	h := &Handler{authManager: manager}
	r := gin.New()
	r.POST("/reset", h.ResetAuthFileCooldown)

	body := bytes.NewBufferString(`{"all":true,"include_quota":false}`)
	req := httptest.NewRequest(http.MethodPost, "/reset", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	var payload struct {
		Reset        int      `json:"reset"`
		IncludeQuota bool     `json:"include_quota"`
		Targets      []string `json:"targets"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.Reset != 1 || payload.IncludeQuota {
		t.Fatalf("unexpected response payload: %+v", payload)
	}
	if len(payload.Targets) != 1 || payload.Targets[0] != "transient.json" {
		t.Fatalf("unexpected targets: %+v", payload.Targets)
	}

	updatedTransient, ok := manager.GetByID(transientAuth.ID)
	if !ok || updatedTransient == nil {
		t.Fatalf("expected transient auth to exist")
	}
	if updatedTransient.Unavailable || !updatedTransient.NextRetryAfter.IsZero() {
		t.Fatalf("expected transient auth cooldown to be cleared")
	}

	updatedQuota, ok := manager.GetByID(quotaAuth.ID)
	if !ok || updatedQuota == nil {
		t.Fatalf("expected quota auth to exist")
	}
	if !updatedQuota.Quota.Exceeded {
		t.Fatalf("expected quota cooldown to remain when include_quota=false")
	}
}
