package executor

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v6/sdk/translator"
	"github.com/tidwall/gjson"
)

func TestCodexPrepareRequestUsesAccessTokenMetadata(t *testing.T) {
	accountID := "acct-123"
	token := fakeCodexJWT(t, accountID)
	req, err := http.NewRequest(http.MethodPost, "https://example.com/responses", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	auth := &cliproxyauth.Auth{
		Provider: "codex",
		Metadata: map[string]any{
			"type":         "codex",
			"access_token": token,
		},
	}

	exec := NewCodexExecutor(nil)
	if err := exec.PrepareRequest(req, auth); err != nil {
		t.Fatalf("PrepareRequest error: %v", err)
	}

	if got := req.Header.Get("Authorization"); got != "Bearer "+token {
		t.Fatalf("Authorization = %q, want Bearer token", got)
	}
	if got := req.Header.Get("Chatgpt-Account-Id"); got != accountID {
		t.Fatalf("Chatgpt-Account-Id = %q, want %q", got, accountID)
	}
	if got := req.Header.Get("Originator"); got != defaultCodexOriginator {
		t.Fatalf("Originator = %q, want %q", got, defaultCodexOriginator)
	}
	if got := req.Header.Get("X-Client-Request-Id"); got == "" {
		t.Fatalf("X-Client-Request-Id should not be empty")
	}
	if got := req.Header.Get("Session_id"); got == "" {
		t.Fatalf("Session_id should not be empty")
	}
}

func TestApplyCodexHeadersDoesNotInjectWebHeadersForAPIKey(t *testing.T) {
	req, err := http.NewRequest(http.MethodPost, "https://example.com/responses", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	auth := &cliproxyauth.Auth{
		Provider: "codex",
		Attributes: map[string]string{
			"api_key": "sk-test",
		},
		Metadata: map[string]any{
			"account_id": "acct-ignored",
		},
	}

	applyCodexHeaders(req, auth, "sk-test", true)

	if got := req.Header.Get("Originator"); got != "" {
		t.Fatalf("Originator = %q, want empty for api_key auth", got)
	}
	if got := req.Header.Get("Chatgpt-Account-Id"); got != "" {
		t.Fatalf("Chatgpt-Account-Id = %q, want empty for api_key auth", got)
	}
}

func TestApplyCodexHeadersPassesThroughCodexTelemetryHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	inboundReq, err := http.NewRequest(http.MethodPost, "https://example.com/inbound", nil)
	if err != nil {
		t.Fatalf("new inbound request: %v", err)
	}
	inboundReq.Header.Set("Traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	inboundReq.Header.Set("Tracestate", "vendor=value")
	inboundReq.Header.Set("X-Codex-Turn-State", "turn-state")
	inboundReq.Header.Set("X-Codex-Turn-Metadata", "{\"turn_id\":\"t-1\"}")
	inboundReq.Header.Set("X-Codex-Beta-Features", "beta-a,beta-b")
	inboundReq.Header.Set("X-Openai-Subagent", "planner")
	inboundReq.Header.Set("X-Openai-Internal-Codex-Residency", "us")
	inboundReq.Header.Set("X-Client-Request-Id", "client-123")
	ginCtx.Request = inboundReq

	token := fakeCodexJWT(t, "acct-123")
	req, err := http.NewRequest(http.MethodPost, "https://example.com/responses", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req = req.WithContext(context.WithValue(req.Context(), "gin", ginCtx))

	auth := &cliproxyauth.Auth{
		Provider: "codex",
		Metadata: map[string]any{
			"access_token": token,
		},
	}

	applyCodexHeaders(req, auth, token, true)

	for key, want := range map[string]string{
		"Traceparent":                       "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
		"Tracestate":                        "vendor=value",
		"X-Codex-Turn-State":                "turn-state",
		"X-Codex-Turn-Metadata":             "{\"turn_id\":\"t-1\"}",
		"X-Codex-Beta-Features":             "beta-a,beta-b",
		"X-Openai-Subagent":                 "planner",
		"X-Openai-Internal-Codex-Residency": "us",
		"X-Client-Request-Id":               "client-123",
	} {
		if got := req.Header.Get(key); got != want {
			t.Fatalf("%s = %q, want %q", key, got, want)
		}
	}
}

func TestCodexCacheHelperUsesOriginalPreviousResponseIDForConversationHeaders(t *testing.T) {
	exec := NewCodexExecutor(nil)
	req := cliproxyexecutor.Request{
		Model:   "gpt-5-codex",
		Payload: []byte(`{"model":"gpt-5-codex","input":"hi"}`),
	}
	opts := cliproxyexecutor.Options{
		SourceFormat:    sdktranslator.FromString("openai-response"),
		OriginalRequest: []byte(`{"model":"gpt-5-codex","input":"hi","previous_response_id":"resp_12345678901234567890"}`),
	}
	body := []byte(`{"model":"gpt-5-codex","input":"hi","previous_response_id":"resp_12345678901234567890"}`)

	httpReq, err := exec.cacheHelper(context.Background(), sdktranslator.FromString("openai-response"), "https://example.com/responses", req, opts, body)
	if err != nil {
		t.Fatalf("cacheHelper error: %v", err)
	}

	wantConversationID := codexConversationPrefix + "resp_12345678901234567890"
	if got := httpReq.Header.Get("Session_id"); got != wantConversationID {
		t.Fatalf("Session_id = %q, want %q", got, wantConversationID)
	}
	if got := httpReq.Header.Get("Conversation_id"); got != wantConversationID {
		t.Fatalf("Conversation_id = %q, want %q", got, wantConversationID)
	}

	bodyBytes, err := io.ReadAll(httpReq.Body)
	if err != nil {
		t.Fatalf("read request body: %v", err)
	}
	if got := gjson.GetBytes(bodyBytes, "previous_response_id").String(); got != "resp_12345678901234567890" {
		t.Fatalf("previous_response_id = %q, want preserved value", got)
	}
	if got := gjson.GetBytes(bodyBytes, "prompt_cache_key").String(); got != wantConversationID {
		t.Fatalf("prompt_cache_key = %q, want %q", got, wantConversationID)
	}
}

func TestCodexExecutePreservesPreviousResponseIDForUpstreamRequest(t *testing.T) {
	var gotBody []byte
	var gotSessionID string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSessionID = r.Header.Get("Session_id")
		body, _ := io.ReadAll(r.Body)
		gotBody = body
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_done\",\"output\":[]},\"usage\":{\"input_tokens\":1,\"output_tokens\":1,\"total_tokens\":2}}\n\n"))
	}))
	defer server.Close()

	token := fakeCodexJWT(t, "acct-123456")
	exec := NewCodexExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{
		Provider: "codex",
		Metadata: map[string]any{
			"access_token": token,
		},
		Attributes: map[string]string{
			"base_url": server.URL,
		},
	}

	payload := []byte(`{"model":"gpt-5-codex","input":"hi","previous_response_id":"resp_12345678901234567890"}`)
	_, err := exec.Execute(context.Background(), auth, cliproxyexecutor.Request{
		Model:   "gpt-5-codex",
		Payload: payload,
	}, cliproxyexecutor.Options{
		SourceFormat: sdktranslator.FromString("openai-response"),
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	wantConversationID := codexConversationPrefix + "resp_12345678901234567890"
	if got := gjson.GetBytes(gotBody, "previous_response_id").String(); got != "resp_12345678901234567890" {
		t.Fatalf("upstream previous_response_id = %q, want preserved value", got)
	}
	if got := gjson.GetBytes(gotBody, "prompt_cache_key").String(); got != wantConversationID {
		t.Fatalf("upstream prompt_cache_key = %q, want %q", got, wantConversationID)
	}
	if gotSessionID != wantConversationID {
		t.Fatalf("upstream Session_id = %q, want %q", gotSessionID, wantConversationID)
	}
}

func fakeCodexJWT(t *testing.T, accountID string) string {
	t.Helper()

	header, err := json.Marshal(map[string]any{"alg": "RS256", "typ": "JWT"})
	if err != nil {
		t.Fatalf("marshal header: %v", err)
	}
	payload, err := json.Marshal(map[string]any{
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": accountID,
		},
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	return base64.RawURLEncoding.EncodeToString(header) + "." +
		base64.RawURLEncoding.EncodeToString(payload) + ".sig"
}
