package auth

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"

	internalconfig "github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/executor"
)

type recordingExecutor struct {
	provider string
	mu       sync.Mutex
	lastAuth string
}

func (e *recordingExecutor) Identifier() string { return e.provider }

func (e *recordingExecutor) Execute(ctx context.Context, auth *Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	_ = ctx
	_ = req
	_ = opts
	e.mu.Lock()
	if auth != nil {
		e.lastAuth = auth.ID
	}
	e.mu.Unlock()
	return cliproxyexecutor.Response{Payload: []byte("{}")}, nil
}

func (e *recordingExecutor) ExecuteStream(context.Context, *Auth, cliproxyexecutor.Request, cliproxyexecutor.Options) (<-chan cliproxyexecutor.StreamChunk, error) {
	return nil, errors.New("not implemented")
}

func (e *recordingExecutor) Refresh(context.Context, *Auth) (*Auth, error) { return nil, nil }

func (e *recordingExecutor) CountTokens(context.Context, *Auth, cliproxyexecutor.Request, cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	return cliproxyexecutor.Response{Payload: []byte("{}")}, nil
}

func (e *recordingExecutor) HttpRequest(context.Context, *Auth, *http.Request) (*http.Response, error) {
	return nil, nil
}

func (e *recordingExecutor) lastAuthID() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.lastAuth
}

func TestAPIKeyAuthPermissions_AllowsConfiguredAccount(t *testing.T) {
	t.Parallel()

	manager := NewManager(nil, &FillFirstSelector{}, NoopHook{})
	exec := &recordingExecutor{provider: "gemini"}
	manager.RegisterExecutor(exec)

	cfg := &internalconfig.Config{
		APIKeyAuth: map[string][]string{
			"client-1": {"auth-allowed"},
		},
	}
	manager.SetConfig(cfg)

	ctx := context.Background()
	_, _ = manager.Register(ctx, &Auth{ID: "auth-allowed", Provider: "gemini", Status: StatusActive})
	_, _ = manager.Register(ctx, &Auth{ID: "auth-denied", Provider: "gemini", Status: StatusActive})

	opts := cliproxyexecutor.Options{
		Metadata: map[string]any{
			cliproxyexecutor.ClientAPIKeyMetadataKey: "client-1",
		},
	}
	_, err := manager.Execute(ctx, []string{"gemini"}, cliproxyexecutor.Request{}, opts)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got := exec.lastAuthID(); got != "auth-allowed" {
		t.Fatalf("Execute() used auth %q, want %q", got, "auth-allowed")
	}
}

func TestAPIKeyAuthPermissions_DenyAll(t *testing.T) {
	t.Parallel()

	manager := NewManager(nil, &FillFirstSelector{}, NoopHook{})
	exec := &recordingExecutor{provider: "gemini"}
	manager.RegisterExecutor(exec)

	cfg := &internalconfig.Config{
		APIKeyAuth: map[string][]string{
			"client-1": {},
		},
	}
	manager.SetConfig(cfg)

	opts := cliproxyexecutor.Options{
		Metadata: map[string]any{
			cliproxyexecutor.ClientAPIKeyMetadataKey: "client-1",
		},
	}
	_, err := manager.Execute(context.Background(), []string{"gemini"}, cliproxyexecutor.Request{}, opts)
	if err == nil {
		t.Fatal("Execute() expected error, got nil")
	}
	if se, ok := err.(interface{ StatusCode() int }); !ok || se == nil || se.StatusCode() != http.StatusForbidden {
		t.Fatalf("Execute() StatusCode = %v, want %d", statusCodeFromError(err), http.StatusForbidden)
	}
}
