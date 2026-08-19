package executor

import (
	"context"
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
