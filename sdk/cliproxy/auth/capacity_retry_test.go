package auth

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/executor"
)

type testCapacityError struct{}

func (testCapacityError) Error() string         { return "Selected model is at capacity" }
func (testCapacityError) StatusCode() int       { return http.StatusTooManyRequests }
func (testCapacityError) IsCapacityError() bool { return true }
func (testCapacityError) RetryAfter() *time.Duration {
	wait := time.Millisecond
	return &wait
}

type capacityRetryExecutor struct {
	executeCalls   atomic.Int32
	streamCalls    atomic.Int32
	alwaysCapacity atomic.Bool
}

func (*capacityRetryExecutor) Identifier() string { return "codex" }

func (e *capacityRetryExecutor) Execute(context.Context, *Auth, cliproxyexecutor.Request, cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	call := e.executeCalls.Add(1)
	if e.alwaysCapacity.Load() || call == 1 {
		return cliproxyexecutor.Response{}, testCapacityError{}
	}
	return cliproxyexecutor.Response{Payload: []byte("ok")}, nil
}

func (e *capacityRetryExecutor) ExecuteStream(context.Context, *Auth, cliproxyexecutor.Request, cliproxyexecutor.Options) (<-chan cliproxyexecutor.StreamChunk, error) {
	call := e.streamCalls.Add(1)
	out := make(chan cliproxyexecutor.StreamChunk, 1)
	if e.alwaysCapacity.Load() || call == 1 {
		out <- cliproxyexecutor.StreamChunk{Err: testCapacityError{}}
	} else {
		out <- cliproxyexecutor.StreamChunk{Payload: []byte("ok")}
	}
	close(out)
	return out, nil
}

func TestManagerCapacityRetryStopsWhenContextCanceled(t *testing.T) {
	executor := &capacityRetryExecutor{}
	executor.alwaysCapacity.Store(true)
	m := newCapacityRetryManager(t, executor)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	_, err := m.Execute(ctx, []string{"codex"}, cliproxyexecutor.Request{}, cliproxyexecutor.Options{})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Execute error = %v, want context deadline", err)
	}
	if got := executor.executeCalls.Load(); got == 0 {
		t.Fatal("capacity retry did not attempt execution")
	}
}

func TestManagerCapacityStreamRetryStopsWhenContextCanceled(t *testing.T) {
	executor := &capacityRetryExecutor{}
	executor.alwaysCapacity.Store(true)
	m := newCapacityRetryManager(t, executor)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	stream, err := m.ExecuteStream(ctx, []string{"codex"}, cliproxyexecutor.Request{}, cliproxyexecutor.Options{})
	if err != nil {
		t.Fatalf("ExecuteStream error: %v", err)
	}
	select {
	case _, ok := <-stream:
		if ok {
			t.Fatal("stream emitted a chunk after context cancellation")
		}
	case <-time.After(time.Second):
		t.Fatal("stream did not stop after context cancellation")
	}
	if got := executor.streamCalls.Load(); got == 0 {
		t.Fatal("capacity stream retry did not attempt execution")
	}
}

func (*capacityRetryExecutor) Refresh(context.Context, *Auth) (*Auth, error) { return nil, nil }

func (*capacityRetryExecutor) CountTokens(context.Context, *Auth, cliproxyexecutor.Request, cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	return cliproxyexecutor.Response{}, nil
}

func (*capacityRetryExecutor) HttpRequest(context.Context, *Auth, *http.Request) (*http.Response, error) {
	return nil, nil
}

func newCapacityRetryManager(t *testing.T, executor *capacityRetryExecutor) *Manager {
	t.Helper()
	m := NewManager(nil, nil, nil)
	m.SetRetryConfig(0, 0)
	m.RegisterExecutor(executor)
	if _, err := m.Register(context.Background(), &Auth{
		ID:       "auth-capacity",
		Provider: "codex",
		Status:   StatusActive,
	}); err != nil {
		t.Fatalf("register auth: %v", err)
	}
	return m
}

func TestManagerCapacityErrorRetriesWithoutChangingAccountHealth(t *testing.T) {
	executor := &capacityRetryExecutor{}
	m := newCapacityRetryManager(t, executor)

	resp, err := m.Execute(context.Background(), []string{"codex"}, cliproxyexecutor.Request{}, cliproxyexecutor.Options{})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if string(resp.Payload) != "ok" {
		t.Fatalf("payload = %q, want ok", resp.Payload)
	}
	if got := executor.executeCalls.Load(); got != 2 {
		t.Fatalf("execute calls = %d, want 2", got)
	}
	auth, ok := m.GetByID("auth-capacity")
	if !ok || auth == nil {
		t.Fatal("expected auth state")
	}
	if auth.Failed != 0 {
		t.Fatalf("failed count = %d, want 0", auth.Failed)
	}
	if auth.Success != 1 {
		t.Fatalf("success count = %d, want 1", auth.Success)
	}
	if auth.Unavailable || auth.Status != StatusActive {
		t.Fatalf("capacity response changed auth availability: unavailable=%v status=%s", auth.Unavailable, auth.Status)
	}
}

func TestManagerCapacityStreamRetriesBeforeForwardingError(t *testing.T) {
	executor := &capacityRetryExecutor{}
	m := newCapacityRetryManager(t, executor)

	stream, err := m.ExecuteStream(context.Background(), []string{"codex"}, cliproxyexecutor.Request{}, cliproxyexecutor.Options{})
	if err != nil {
		t.Fatalf("ExecuteStream error: %v", err)
	}
	select {
	case chunk, ok := <-stream:
		if !ok {
			t.Fatal("stream closed before retry payload")
		}
		if chunk.Err != nil {
			t.Fatalf("unexpected stream error: %v", chunk.Err)
		}
		if string(chunk.Payload) != "ok" {
			t.Fatalf("payload = %q, want ok", chunk.Payload)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for retry payload")
	}
	if got := executor.streamCalls.Load(); got != 2 {
		t.Fatalf("stream calls = %d, want 2", got)
	}
	auth, ok := m.GetByID("auth-capacity")
	if !ok || auth == nil {
		t.Fatal("expected auth state")
	}
	if auth.Failed != 0 || auth.Success != 1 {
		t.Fatalf("unexpected auth counters: failed=%d success=%d", auth.Failed, auth.Success)
	}
}
