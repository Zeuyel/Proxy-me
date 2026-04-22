package auth

import (
	"context"
	"net/http"
	"testing"
	"time"

	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/executor"
)

type ineffectiveRefreshExecutor struct{}
type alwaysRefreshRuntime struct{}

func (alwaysRefreshRuntime) ShouldRefresh(time.Time, *Auth) bool { return true }

func (ineffectiveRefreshExecutor) Identifier() string { return "codex" }

func (ineffectiveRefreshExecutor) Execute(context.Context, *Auth, cliproxyexecutor.Request, cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	return cliproxyexecutor.Response{}, nil
}

func (ineffectiveRefreshExecutor) ExecuteStream(context.Context, *Auth, cliproxyexecutor.Request, cliproxyexecutor.Options) (<-chan cliproxyexecutor.StreamChunk, error) {
	return nil, nil
}

func (ineffectiveRefreshExecutor) Refresh(_ context.Context, auth *Auth) (*Auth, error) {
	if auth == nil {
		return nil, nil
	}
	updated := auth.Clone()
	// Leave expiry unchanged so shouldRefresh() still considers the auth stale.
	return updated, nil
}

func (ineffectiveRefreshExecutor) CountTokens(context.Context, *Auth, cliproxyexecutor.Request, cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	return cliproxyexecutor.Response{}, nil
}

func (ineffectiveRefreshExecutor) HttpRequest(context.Context, *Auth, *http.Request) (*http.Response, error) {
	return nil, nil
}

func TestRefreshAuth_IneffectiveRefreshSchedulesBackoff(t *testing.T) {
	manager := NewManager(nil, nil, nil)
	manager.RegisterExecutor(ineffectiveRefreshExecutor{})

	now := time.Now()
	auth := &Auth{
		ID:              "auth-1",
		Provider:        "codex",
		Status:          StatusActive,
		LastRefreshedAt: now.Add(-2 * time.Hour),
		Runtime:         alwaysRefreshRuntime{},
	}
	_, _ = manager.Register(context.Background(), auth)

	manager.refreshAuth(context.Background(), auth.ID)

	refreshed, ok := manager.GetByID(auth.ID)
	if !ok || refreshed == nil {
		t.Fatalf("expected refreshed auth to exist")
	}
	if refreshed.NextRefreshAfter.IsZero() {
		t.Fatal("expected ineffective refresh to schedule NextRefreshAfter")
	}
	if wait := time.Until(refreshed.NextRefreshAfter); wait < 20*time.Second || wait > 40*time.Second {
		t.Fatalf("expected ineffective refresh backoff around 30s, got %s", wait)
	}
}
