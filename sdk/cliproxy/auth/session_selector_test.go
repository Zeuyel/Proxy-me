package auth

import (
	"context"
	"testing"
	"time"

	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/executor"
)

func TestSessionSelectorPick_MixedCreatesProviderScopedBinding(t *testing.T) {
	selector := NewSessionSelector(SessionSelectorConfig{
		Enabled:          true,
		Providers:        []string{"codex"},
		TTL:              5 * time.Minute,
		FailureThreshold: 1,
		Cooldown:         5 * time.Minute,
		LoadWindow:       0,
	})

	auths := []*Auth{
		{ID: "auth-a", Provider: "codex", Status: StatusActive},
	}
	opts := cliproxyexecutor.Options{
		Metadata: map[string]any{
			cliproxyexecutor.SessionIDMetadataKey: "session-1",
		},
	}

	selected, err := selector.Pick(context.Background(), "mixed", "test-model", opts, auths)
	if err != nil {
		t.Fatalf("Pick() error = %v", err)
	}
	if selected == nil || selected.ID != "auth-a" {
		t.Fatalf("Pick() selected = %v, want auth-a", selected)
	}

	selector.mu.Lock()
	_, hasProviderScoped := selector.sessions["codex:session-1"]
	_, hasMixedScoped := selector.sessions["mixed:session-1"]
	selector.mu.Unlock()

	if !hasProviderScoped {
		t.Fatalf("expected provider-scoped binding codex:session-1 to exist")
	}
	if hasMixedScoped {
		t.Fatalf("expected no mixed-scoped binding mixed:session-1")
	}
}

func TestSessionSelectorPick_MixedCooldownFailover(t *testing.T) {
	selector := NewSessionSelector(SessionSelectorConfig{
		Enabled:          true,
		Providers:        []string{"codex"},
		TTL:              5 * time.Minute,
		FailureThreshold: 1,
		Cooldown:         5 * time.Minute,
		LoadWindow:       0,
	})

	auths := []*Auth{
		{ID: "auth-a", Provider: "codex", Status: StatusActive},
		{ID: "auth-b", Provider: "codex", Status: StatusActive},
	}
	opts := cliproxyexecutor.Options{
		Metadata: map[string]any{
			cliproxyexecutor.SessionIDMetadataKey: "session-1",
		},
	}

	first, err := selector.Pick(context.Background(), "mixed", "test-model", opts, auths)
	if err != nil {
		t.Fatalf("first Pick() error = %v", err)
	}
	if first == nil || first.ID != "auth-a" {
		t.Fatalf("first Pick() selected = %v, want auth-a", first)
	}

	ctx := WithSessionID(context.Background(), "session-1")
	selector.RecordResult(ctx, Result{
		AuthID:   first.ID,
		Provider: "codex",
		Model:    "test-model",
		Success:  false,
		Error: &Error{
			HTTPStatus: 429,
			Message:    "rate limited",
		},
	})

	second, err := selector.Pick(context.Background(), "mixed", "test-model", opts, auths)
	if err != nil {
		t.Fatalf("second Pick() error = %v", err)
	}
	if second == nil || second.ID != "auth-b" {
		t.Fatalf("second Pick() selected = %v, want auth-b", second)
	}
}

func TestSessionSelectorPick_UsesOriginalRequestPromptCacheKeyWhenMetadataMissing(t *testing.T) {
	selector := NewSessionSelector(SessionSelectorConfig{
		Enabled:          true,
		Providers:        []string{"codex"},
		TTL:              5 * time.Minute,
		FailureThreshold: 1,
		Cooldown:         5 * time.Minute,
		LoadWindow:       0,
	})

	auths := []*Auth{
		{ID: "auth-a", Provider: "codex", Status: StatusActive},
	}
	opts := cliproxyexecutor.Options{
		OriginalRequest: []byte(`{"input":[],"prompt_cache_key":"prompt-cache-key-1234567890"}`),
	}

	selected, err := selector.Pick(context.Background(), "codex", "test-model", opts, auths)
	if err != nil {
		t.Fatalf("Pick() error = %v", err)
	}
	if selected == nil || selected.ID != "auth-a" {
		t.Fatalf("Pick() selected = %v, want auth-a", selected)
	}

	selector.mu.Lock()
	_, hasBinding := selector.sessions["codex:prompt-cache-key-1234567890"]
	selector.mu.Unlock()
	if !hasBinding {
		t.Fatalf("expected binding for prompt_cache_key extracted from OriginalRequest")
	}
}

func TestSessionSelectorPick_BindsToAuthFileAcrossAuthIDRefresh(t *testing.T) {
	selector := NewSessionSelector(SessionSelectorConfig{
		Enabled:          true,
		Providers:        []string{"codex"},
		TTL:              5 * time.Minute,
		FailureThreshold: 1,
		Cooldown:         5 * time.Minute,
		LoadWindow:       0,
	})
	firstAuth := &Auth{ID: "runtime-id-1", FileName: "codex-account.json", Provider: "codex", Status: StatusActive}
	otherAuth := &Auth{ID: "z-other", FileName: "other.json", Provider: "codex", Status: StatusActive}
	opts := cliproxyexecutor.Options{Metadata: map[string]any{cliproxyexecutor.SessionIDMetadataKey: "session-file-binding"}}

	selected, err := selector.Pick(context.Background(), "codex", "test-model", opts, []*Auth{firstAuth, otherAuth})
	if err != nil {
		t.Fatalf("initial Pick() error = %v", err)
	}
	if selected != firstAuth {
		t.Fatalf("initial Pick() selected = %v, want file-backed auth", selected)
	}

	refreshedAuth := &Auth{ID: "runtime-id-2", FileName: "codex-account.json", Provider: "codex", Status: StatusActive}
	selected, err = selector.Pick(context.Background(), "codex", "test-model", opts, []*Auth{refreshedAuth, otherAuth})
	if err != nil {
		t.Fatalf("refreshed Pick() error = %v", err)
	}
	if selected != refreshedAuth {
		t.Fatalf("refreshed Pick() selected = %v, want same auth file", selected)
	}
}

func TestSessionSelectorPick_FailsOverFromUnavailableFileBinding(t *testing.T) {
	selector := NewSessionSelector(SessionSelectorConfig{
		Enabled:          true,
		Providers:        []string{"codex"},
		TTL:              5 * time.Minute,
		FailureThreshold: 1,
		Cooldown:         5 * time.Minute,
		LoadWindow:       0,
	})
	authA := &Auth{ID: "auth-a", FileName: "a.json", Provider: "codex", Status: StatusActive}
	authB := &Auth{ID: "auth-b", FileName: "b.json", Provider: "codex", Status: StatusActive}
	opts := cliproxyexecutor.Options{Metadata: map[string]any{cliproxyexecutor.SessionIDMetadataKey: "session-failover"}}

	first, err := selector.Pick(context.Background(), "codex", "test-model", opts, []*Auth{authA, authB})
	if err != nil {
		t.Fatalf("initial Pick() error = %v", err)
	}
	if first != authA {
		t.Fatalf("initial Pick() selected = %v, want auth-a", first)
	}

	ctx := WithSessionID(context.Background(), "session-failover")
	selector.RecordResult(ctx, Result{AuthID: authA.ID, Provider: "codex", Model: "test-model", Success: false, Error: &Error{HTTPStatus: 429}})
	authA.Unavailable = true
	authA.NextRetryAfter = time.Now().Add(time.Hour)
	second, err := selector.Pick(context.Background(), "codex", "test-model", opts, []*Auth{authA, authB})
	if err != nil {
		t.Fatalf("failover Pick() error = %v", err)
	}
	if second != authB {
		t.Fatalf("failover Pick() selected = %v, want auth-b", second)
	}

	authA.Unavailable = false
	authA.NextRetryAfter = time.Time{}
	third, err := selector.Pick(context.Background(), "codex", "test-model", opts, []*Auth{authA, authB})
	if err != nil {
		t.Fatalf("sticky fallback Pick() error = %v", err)
	}
	if third != authB {
		t.Fatalf("sticky fallback Pick() selected = %v, want auth-b", third)
	}
}
