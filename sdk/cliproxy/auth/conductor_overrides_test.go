package auth

import (
	"context"
	"testing"
	"time"
)

func TestManager_ShouldRetryAfterError_RespectsAuthRequestRetryOverride(t *testing.T) {
	m := NewManager(nil, nil, nil)
	m.SetRetryConfig(3, 30*time.Second)

	model := "test-model"
	next := time.Now().Add(5 * time.Second)

	auth := &Auth{
		ID:       "auth-1",
		Provider: "claude",
		Metadata: map[string]any{
			"request_retry": float64(0),
		},
		ModelStates: map[string]*ModelState{
			model: {
				Unavailable:    true,
				Status:         StatusError,
				NextRetryAfter: next,
			},
		},
	}
	if _, errRegister := m.Register(context.Background(), auth); errRegister != nil {
		t.Fatalf("register auth: %v", errRegister)
	}

	_, maxWait := m.retrySettings()
	wait, shouldRetry := m.shouldRetryAfterError(&Error{HTTPStatus: 500, Message: "boom"}, 0, []string{"claude"}, model, maxWait)
	if shouldRetry {
		t.Fatalf("expected shouldRetry=false for request_retry=0, got true (wait=%v)", wait)
	}

	auth.Metadata["request_retry"] = float64(1)
	if _, errUpdate := m.Update(context.Background(), auth); errUpdate != nil {
		t.Fatalf("update auth: %v", errUpdate)
	}

	wait, shouldRetry = m.shouldRetryAfterError(&Error{HTTPStatus: 500, Message: "boom"}, 0, []string{"claude"}, model, maxWait)
	if !shouldRetry {
		t.Fatalf("expected shouldRetry=true for request_retry=1, got false")
	}
	if wait <= 0 {
		t.Fatalf("expected wait > 0, got %v", wait)
	}

	_, shouldRetry = m.shouldRetryAfterError(&Error{HTTPStatus: 500, Message: "boom"}, 1, []string{"claude"}, model, maxWait)
	if shouldRetry {
		t.Fatalf("expected shouldRetry=false on attempt=1 for request_retry=1, got true")
	}
}

func TestManager_MarkResult_RespectsAuthDisableCoolingOverride(t *testing.T) {
	prev := quotaCooldownDisabled.Load()
	quotaCooldownDisabled.Store(false)
	t.Cleanup(func() { quotaCooldownDisabled.Store(prev) })

	m := NewManager(nil, nil, nil)

	auth := &Auth{
		ID:       "auth-1",
		Provider: "claude",
		Metadata: map[string]any{
			"disable_cooling": true,
		},
	}
	if _, errRegister := m.Register(context.Background(), auth); errRegister != nil {
		t.Fatalf("register auth: %v", errRegister)
	}

	model := "test-model"
	m.MarkResult(context.Background(), Result{
		AuthID:   "auth-1",
		Provider: "claude",
		Model:    model,
		Success:  false,
		Error:    &Error{HTTPStatus: 500, Message: "boom"},
	})

	updated, ok := m.GetByID("auth-1")
	if !ok || updated == nil {
		t.Fatalf("expected auth to be present")
	}
	state := updated.ModelStates[model]
	if state == nil {
		t.Fatalf("expected model state to be present")
	}
	if !state.NextRetryAfter.IsZero() {
		t.Fatalf("expected NextRetryAfter to be zero when disable_cooling=true, got %v", state.NextRetryAfter)
	}
}

func TestManager_SyncQuotaProbe_ClearsQuotaCooldown(t *testing.T) {
	m := NewManager(nil, nil, nil)
	recoverAt := time.Now().Add(30 * time.Minute)

	auth := &Auth{
		ID:             "auth-1",
		Provider:       "codex",
		Status:         StatusError,
		StatusMessage:  "quota exhausted",
		Unavailable:    true,
		NextRetryAfter: recoverAt,
		LastError:      &Error{HTTPStatus: 429, Message: "quota"},
		Quota: QuotaState{
			Exceeded:      true,
			Reason:        "codex_5h_limit",
			NextRecoverAt: recoverAt,
		},
		ModelStates: map[string]*ModelState{
			"gpt-5": {
				Status:         StatusError,
				StatusMessage:  "quota exhausted",
				Unavailable:    true,
				NextRetryAfter: recoverAt,
				LastError:      &Error{HTTPStatus: 429, Message: "quota"},
				Quota: QuotaState{
					Exceeded:      true,
					Reason:        "codex_5h_limit",
					NextRecoverAt: recoverAt,
				},
			},
			"manual-disabled": {
				Status:         StatusDisabled,
				NextRetryAfter: recoverAt,
				Quota: QuotaState{
					Exceeded:      true,
					Reason:        "codex_5h_limit",
					NextRecoverAt: recoverAt,
				},
			},
		},
	}
	if _, errRegister := m.Register(context.Background(), auth); errRegister != nil {
		t.Fatalf("register auth: %v", errRegister)
	}

	m.SyncQuotaProbe(context.Background(), auth.ID, false, "", time.Time{})

	updated, ok := m.GetByID(auth.ID)
	if !ok || updated == nil {
		t.Fatalf("expected auth to be present")
	}
	if updated.Quota.Exceeded {
		t.Fatalf("expected auth quota cooldown to be cleared")
	}
	if updated.Unavailable {
		t.Fatalf("expected auth to be available after cooldown clears")
	}
	if updated.Status != StatusActive {
		t.Fatalf("expected auth status active, got %s", updated.Status)
	}
	if updated.StatusMessage != "" {
		t.Fatalf("expected auth status message cleared, got %q", updated.StatusMessage)
	}
	if updated.LastError != nil {
		t.Fatalf("expected auth last error cleared")
	}

	state := updated.ModelStates["gpt-5"]
	if state == nil {
		t.Fatalf("expected model state to be present")
	}
	if state.Quota.Exceeded {
		t.Fatalf("expected model quota cooldown to be cleared")
	}
	if state.Unavailable {
		t.Fatalf("expected model to be available after cooldown clears")
	}
	if state.Status != StatusActive {
		t.Fatalf("expected model status active, got %s", state.Status)
	}

	disabledState := updated.ModelStates["manual-disabled"]
	if disabledState == nil {
		t.Fatalf("expected disabled model state to be present")
	}
	if disabledState.Status != StatusDisabled {
		t.Fatalf("expected disabled model state to stay disabled, got %s", disabledState.Status)
	}
	if disabledState.Quota.Exceeded {
		t.Fatalf("expected disabled model stale quota cooldown to be cleared")
	}
}

func TestManager_SyncQuotaProbe_SetsQuotaCooldown(t *testing.T) {
	m := NewManager(nil, nil, nil)
	recoverAt := time.Now().Add(5 * time.Hour)

	auth := &Auth{
		ID:       "auth-1",
		Provider: "codex",
		Status:   StatusActive,
		ModelStates: map[string]*ModelState{
			"gpt-5": {
				Status: StatusActive,
			},
		},
	}
	if _, errRegister := m.Register(context.Background(), auth); errRegister != nil {
		t.Fatalf("register auth: %v", errRegister)
	}

	m.SyncQuotaProbe(context.Background(), auth.ID, true, "codex_5h_limit", recoverAt)

	updated, ok := m.GetByID(auth.ID)
	if !ok || updated == nil {
		t.Fatalf("expected auth to be present")
	}
	if !updated.Quota.Exceeded {
		t.Fatalf("expected auth quota cooldown to be set")
	}
	if updated.Quota.Reason != "codex_5h_limit" {
		t.Fatalf("expected auth quota reason codex_5h_limit, got %q", updated.Quota.Reason)
	}
	if !updated.Unavailable {
		t.Fatalf("expected auth to be unavailable while quota is exceeded")
	}
	if updated.Status != StatusError {
		t.Fatalf("expected auth status error, got %s", updated.Status)
	}

	state := updated.ModelStates["gpt-5"]
	if state == nil {
		t.Fatalf("expected model state to be present")
	}
	if !state.Quota.Exceeded {
		t.Fatalf("expected model quota cooldown to be set")
	}
	if state.Quota.Reason != "codex_5h_limit" {
		t.Fatalf("expected model quota reason codex_5h_limit, got %q", state.Quota.Reason)
	}
	if !state.Unavailable {
		t.Fatalf("expected model to be unavailable while quota is exceeded")
	}
	if state.Status != StatusError {
		t.Fatalf("expected model status error, got %s", state.Status)
	}
}
