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
