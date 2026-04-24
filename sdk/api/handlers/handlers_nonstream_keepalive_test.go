package handlers

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	sdkconfig "github.com/router-for-me/CLIProxyAPI/v6/sdk/config"
)

func TestNonStreamingKeepAliveInterval_DefaultEnabled(t *testing.T) {
	got := NonStreamingKeepAliveInterval(nil)
	want := 5 * time.Second
	if got != want {
		t.Fatalf("NonStreamingKeepAliveInterval(nil) = %v, want %v", got, want)
	}
}

func TestNonStreamingKeepAliveInterval_ZeroUsesDefaultAndNegativeDisables(t *testing.T) {
	if got := NonStreamingKeepAliveInterval(&sdkconfig.SDKConfig{NonStreamKeepAliveInterval: 0}); got != 5*time.Second {
		t.Fatalf("NonStreamingKeepAliveInterval(0) = %v, want %v", got, 5*time.Second)
	}
	if got := NonStreamingKeepAliveInterval(&sdkconfig.SDKConfig{NonStreamKeepAliveInterval: -1}); got != 0 {
		t.Fatalf("NonStreamingKeepAliveInterval(-1) = %v, want 0", got)
	}
}

func TestStartNonStreamingKeepAlive_WritesJSONSafeWhitespace(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)

	handler := NewBaseAPIHandlers(&sdkconfig.SDKConfig{
		NonStreamKeepAliveInterval: 1,
	}, coreauth.NewManager(nil, nil, nil))

	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stop := handler.StartNonStreamingKeepAlive(ctx, runCtx)
	defer stop()

	time.Sleep(1200 * time.Millisecond)
	stop()

	body := recorder.Body.String()
	if body == "" {
		t.Fatal("expected keepalive payload, got empty body")
	}
	if strings.Trim(body, " \n\r\t") != "" {
		t.Fatalf("expected JSON-safe whitespace only, got %q", body)
	}
	if got := recorder.Header().Get("X-Accel-Buffering"); got != "no" {
		t.Fatalf("X-Accel-Buffering = %q, want no", got)
	}
}
