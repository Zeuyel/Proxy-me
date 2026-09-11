package executor

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/logging"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/executor"
	"github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/usage"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v6/sdk/translator"
)

const codexCapacityEvent = `data: {"type":"response.failed","response":{"error":{"message":"Selected model is at capacity. Please try a different model."}}}`

func TestCodexExecuteReportsCapacityFailureFromSSE(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, codexCapacityEvent+"\n\n")
	}))
	defer server.Close()

	exec := NewCodexExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{
		Provider: "codex",
		Metadata: map[string]any{"access_token": fakeCodexJWT(t, "acct-capacity")},
		Attributes: map[string]string{
			"base_url": server.URL,
		},
	}
	_, err := exec.Execute(context.Background(), auth, cliproxyexecutor.Request{
		Model:   "gpt-5-codex",
		Payload: []byte(`{"model":"gpt-5-codex","input":"hi"}`),
	}, cliproxyexecutor.Options{SourceFormat: sdktranslator.FromString("openai-response")})
	if err == nil || !cliproxyexecutor.IsCapacityError(err) {
		t.Fatalf("expected capacity error, got %v", err)
	}
	if status, ok := err.(interface{ StatusCode() int }); !ok || status.StatusCode() != http.StatusTooManyRequests {
		t.Fatalf("expected capacity status 429, got %T/%v", err, err)
	}
}

func TestCodexExecuteTriggersOAuthAuxiliaryRequests(t *testing.T) {
	paths := make(chan string, len(codexAuxiliaryEndpoints)+1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths <- r.URL.Path
		if r.URL.Path == "/models" && r.URL.Query().Get("client_version") != codexClientVersion {
			t.Errorf("models client_version = %q, want %q", r.URL.Query().Get("client_version"), codexClientVersion)
		}
		if r.URL.Path != "/responses" && r.Header.Get("Chatgpt-Account-Id") != "acct-auxiliary" {
			t.Errorf("%s missing Chatgpt-Account-Id", r.URL.Path)
		}
		if r.URL.Path == "/wham/settings/user" && r.Header.Get("Cache-Control") != "no-cache, no-store" {
			t.Errorf("settings request cache-control = %q", r.Header.Get("Cache-Control"))
		}
		if r.URL.Path == "/responses" {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_done\",\"output\":[]},\"usage\":{\"input_tokens\":1,\"output_tokens\":1,\"total_tokens\":2}}\n\n")
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	exec := NewCodexExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{
		ID:       "oauth-auxiliary-test",
		Provider: "codex",
		Metadata: map[string]any{"access_token": fakeCodexJWT(t, "acct-auxiliary")},
		Attributes: map[string]string{
			"auth_kind": "oauth",
			"base_url":  server.URL,
		},
	}
	_, err := exec.Execute(context.Background(), auth, cliproxyexecutor.Request{
		Model:   "gpt-5-codex",
		Payload: []byte(`{"model":"gpt-5-codex","input":"hi"}`),
	}, cliproxyexecutor.Options{SourceFormat: sdktranslator.FromString("openai-response")})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	want := map[string]bool{"/responses": true, "/models": true, "/wham/usage": true, "/wham/profiles/me": true, "/wham/config/bundle": true, "/wham/settings/user": true}
	got := make(map[string]bool)
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	for len(got) < len(want) {
		select {
		case path := <-paths:
			got[path] = true
		case <-deadline.C:
			t.Fatalf("auxiliary requests incomplete: got %v, want %v", got, want)
		}
	}
	for path := range want {
		if !got[path] {
			t.Errorf("missing request %s", path)
		}
	}
}

func TestCodexUsageRefreshIntervalForPayload(t *testing.T) {
	tests := []struct {
		name string
		used float64
		want time.Duration
	}{
		{name: "normal", used: 40, want: time.Minute},
		{name: "elevated", used: 75, want: 30 * time.Second},
		{name: "near limit", used: 90, want: 15 * time.Second},
		{name: "exhausted", used: 99, want: 5 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload := []byte(fmt.Sprintf(`{"rate_limit":{"primary_window":{"used_percent":%.1f}}}`, tt.used))
			if got := codexUsageRefreshIntervalForPayload(payload); got != tt.want {
				t.Fatalf("refresh interval = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestCodexExecuteStreamReportsCapacityFailureFromSSE(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, codexCapacityEvent+"\n\n")
	}))
	defer server.Close()

	exec := NewCodexExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{
		Provider: "codex",
		Metadata: map[string]any{"access_token": fakeCodexJWT(t, "acct-capacity")},
		Attributes: map[string]string{
			"base_url": server.URL,
		},
	}
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	inbound, err := http.NewRequest(http.MethodPost, "https://example.com/inbound", nil)
	if err != nil {
		t.Fatalf("new inbound request: %v", err)
	}
	ginCtx.Request = inbound
	ctx := context.WithValue(context.Background(), "gin", ginCtx)
	ctx = logging.WithRequestID(ctx, "capacity-stream-monitor-test")
	stream, err := exec.ExecuteStream(ctx, auth, cliproxyexecutor.Request{
		Model:   "gpt-5-codex",
		Payload: []byte(`{"model":"gpt-5-codex","input":"hi"}`),
	}, cliproxyexecutor.Options{SourceFormat: sdktranslator.FromString("openai-response")})
	if err != nil {
		t.Fatalf("ExecuteStream error: %v", err)
	}
	chunk, ok := <-stream
	if !ok {
		t.Fatal("stream closed without an error chunk")
	}
	if chunk.Payload != nil {
		t.Fatalf("capacity failure should not emit a payload: %s", chunk.Payload)
	}
	if !cliproxyexecutor.IsCapacityError(chunk.Err) {
		t.Fatalf("expected capacity error chunk, got %v", chunk.Err)
	}
	if _, exists := ginCtx.Get(monitorStreamErrorKey); exists {
		t.Fatal("capacity stream error was stored as monitor failure")
	}
}

func TestNewCodexStatusErrSeparatesCapacityFromQuota(t *testing.T) {
	err := newCodexStatusErr(context.Background(), nil, nil, http.StatusTooManyRequests, []byte(codexCapacityEvent), nil)
	if !err.IsCapacityError() {
		t.Fatal("expected capacity marker")
	}
	if err.StatusCode() != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want %d", err.StatusCode(), http.StatusTooManyRequests)
	}
	if err.QuotaReason() != "" {
		t.Fatalf("capacity error should not carry a quota reason: %q", err.QuotaReason())
	}
}

type capacityUsageCapture struct {
	records chan usage.Record
}

func (c *capacityUsageCapture) HandleUsage(_ context.Context, record usage.Record) {
	select {
	case c.records <- record:
	default:
	}
}

func TestUsageReporterSkipsCapacityFailureRecordAndMonitorError(t *testing.T) {
	capture := &capacityUsageCapture{records: make(chan usage.Record, 8)}
	usage.RegisterPlugin(capture)

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	inbound, err := http.NewRequest(http.MethodPost, "https://example.com/inbound", nil)
	if err != nil {
		t.Fatalf("new inbound request: %v", err)
	}
	ginCtx.Request = inbound
	requestID := "capacity-reporter-test"
	ctx := logging.WithRequestID(context.WithValue(context.Background(), "gin", ginCtx), requestID)
	reporter := newUsageReporter(ctx, "codex", "gpt-5-codex", &cliproxyauth.Auth{ID: "capacity-reporter-auth", Provider: "codex"})
	capacityErr := error(newCodexStatusErr(ctx, nil, nil, http.StatusTooManyRequests, []byte(codexCapacityEvent), nil))
	reporter.trackFailure(ctx, &capacityErr)
	if _, exists := ginCtx.Get(monitorUpstreamErrorKey); exists {
		t.Fatal("capacity failure was stored as upstream monitor error")
	}

	// Publish a success sentinel to prove the capacity path did not consume the reporter.
	reporter.ensurePublished(ctx)
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	for {
		select {
		case record := <-capture.records:
			if record.RequestID != requestID {
				continue
			}
			if record.Failed {
				t.Fatal("capacity failure published a failed usage record")
			}
			return
		case <-deadline.C:
			t.Fatal("timed out waiting for usage sentinel")
		}
	}
}
