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
	if got := req.Header.Get("Version"); got != codexClientVersion {
		t.Fatalf("Version = %q, want %q", got, codexClientVersion)
	}
	if got := req.Header.Get("Openai-Beta"); got != codexResponsesBeta {
		t.Fatalf("Openai-Beta = %q, want %q", got, codexResponsesBeta)
	}
	if got := req.Header.Get("User-Agent"); got != defaultCodexUserAgent {
		t.Fatalf("User-Agent = %q, want %q", got, defaultCodexUserAgent)
	}
	if got := req.Header.Get("Chatgpt-Account-Id"); got != "" {
		t.Fatalf("Chatgpt-Account-Id = %q, want empty by default", got)
	}
	if got := req.Header.Get("Originator"); got != defaultCodexOriginator {
		t.Fatalf("Originator = %q, want %q", got, defaultCodexOriginator)
	}
	if got := req.Header.Get("Origin"); got != codexWebOrigin {
		t.Fatalf("Origin = %q, want %q", got, codexWebOrigin)
	}
	if got := req.Header.Get("Referer"); got != codexCodexReferer {
		t.Fatalf("Referer = %q, want %q", got, codexCodexReferer)
	}
	if got := req.Header.Get("X-Client-Request-Id"); got != req.Header.Get("Session_id") {
		t.Fatalf("X-Client-Request-Id = %q, want Session_id %q", got, req.Header.Get("Session_id"))
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
	if got := req.Header.Get("Origin"); got != "" {
		t.Fatalf("Origin = %q, want empty for api_key auth", got)
	}
	if got := req.Header.Get("Referer"); got != "" {
		t.Fatalf("Referer = %q, want empty for api_key auth", got)
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
	inboundReq.Header.Set("X-Codex-Installation-Id", "install-123")
	inboundReq.Header.Set("X-Codex-Window-Id", "window-123:0")
	inboundReq.Header.Set("X-Codex-Parent-Thread-Id", "thread-123")
	inboundReq.Header.Set("X-Responsesapi-Include-Timing-Metrics", "true")
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
		"Traceparent":                           "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
		"Tracestate":                            "vendor=value",
		"X-Codex-Installation-Id":               "install-123",
		"X-Codex-Window-Id":                     "window-123:0",
		"X-Codex-Parent-Thread-Id":              "thread-123",
		"X-Codex-Turn-State":                    "turn-state",
		"X-Codex-Turn-Metadata":                 "{\"turn_id\":\"t-1\"}",
		"X-Codex-Beta-Features":                 "beta-a,beta-b",
		"X-Responsesapi-Include-Timing-Metrics": "true",
		"X-Openai-Subagent":                     "planner",
		"X-Openai-Internal-Codex-Residency":     "us",
		"X-Client-Request-Id":                   "client-123",
		"Origin":                                codexWebOrigin,
		"Referer":                               codexCodexReferer,
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

	httpReq, bodyBytes, _, err := exec.cacheHelper(context.Background(), sdktranslator.FromString("openai-response"), "https://example.com/responses", nil, req, opts, opts.OriginalRequest, body)
	if err != nil {
		t.Fatalf("cacheHelper error: %v", err)
	}

	wantConversationID := codexConversationPrefix + "resp_12345678901234567890"
	if got := httpReq.Header.Get("Session_id"); got != wantConversationID {
		t.Fatalf("Session_id = %q, want %q", got, wantConversationID)
	}
	if got := httpReq.Header.Get("Conversation_id"); got != "" {
		t.Fatalf("Conversation_id = %q, want empty", got)
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

func TestCodexIdentityConfuseRemapsRequestIdentity(t *testing.T) {
	exec := NewCodexExecutor(&config.Config{
		Codex: config.CodexConfig{IdentityConfuse: true},
		Routing: config.RoutingConfig{
			Strategy: "session",
		},
	})
	auth := &cliproxyauth.Auth{
		ID:       "auth-codex-plus",
		Provider: "codex",
		Metadata: map[string]any{
			"access_token": fakeCodexJWT(t, "acct-123456"),
		},
	}
	req := cliproxyexecutor.Request{
		Model:   "gpt-5-codex",
		Payload: []byte(`{"model":"gpt-5-codex","input":"hi","prompt_cache_key":"client-cache-1234567890"}`),
	}
	opts := cliproxyexecutor.Options{SourceFormat: sdktranslator.FromString("openai-response")}
	body := []byte(`{"model":"gpt-5-codex","input":"hi","prompt_cache_key":"client-cache-1234567890","client_metadata":{"x-codex-installation-id":"install-123","x-codex-turn-metadata":"{\"prompt_cache_key\":\"client-cache-1234567890\",\"turn_id\":\"turn-123\",\"window_id\":\"client-cache-1234567890:0\"}","x-codex-window-id":"client-cache-1234567890:0"}}`)

	httpReq, upstreamBody, state, err := exec.cacheHelper(context.Background(), sdktranslator.FromString("openai-response"), "https://example.com/responses", auth, req, opts, req.Payload, body)
	if err != nil {
		t.Fatalf("cacheHelper error: %v", err)
	}
	applyCodexHeaders(httpReq, auth, "token", true)
	applyCodexIdentityConfuseHeaders(httpReq.Header, &state)

	confusedCache := gjson.GetBytes(upstreamBody, "prompt_cache_key").String()
	if confusedCache == "" || confusedCache == "client-cache-1234567890" {
		t.Fatalf("prompt_cache_key was not confused: %q; body=%s", confusedCache, upstreamBody)
	}
	if got := httpReq.Header.Get("Session_id"); got != confusedCache {
		t.Fatalf("Session_id = %q, want confused prompt cache %q", got, confusedCache)
	}
	if got := httpReq.Header.Get("X-Client-Request-Id"); got != confusedCache {
		t.Fatalf("X-Client-Request-Id = %q, want confused prompt cache %q", got, confusedCache)
	}
	if got := httpReq.Header.Get("X-Codex-Window-Id"); got != confusedCache+":0" {
		t.Fatalf("X-Codex-Window-Id = %q, want %q", got, confusedCache+":0")
	}
	if got := httpReq.Header.Get("Conversation_id"); got != "" {
		t.Fatalf("Conversation_id = %q, want empty", got)
	}
	if got := httpReq.Header.Get("Chatgpt-Account-Id"); got != "" {
		t.Fatalf("Chatgpt-Account-Id = %q, want empty", got)
	}
	turnMetadata := gjson.GetBytes(upstreamBody, "client_metadata.x-codex-turn-metadata").String()
	if got := gjson.Get(turnMetadata, "prompt_cache_key").String(); got != confusedCache {
		t.Fatalf("turn metadata prompt_cache_key = %q, want %q; metadata=%s", got, confusedCache, turnMetadata)
	}
	if got := gjson.Get(turnMetadata, "turn_id").String(); got == "" || got == "turn-123" {
		t.Fatalf("turn metadata turn_id was not confused: %q; metadata=%s", got, turnMetadata)
	}
	if got := gjson.Get(turnMetadata, "window_id").String(); got != confusedCache+":0" {
		t.Fatalf("turn metadata window_id = %q, want %q; metadata=%s", got, confusedCache+":0", turnMetadata)
	}
	if got := gjson.GetBytes(upstreamBody, "client_metadata.x-codex-installation-id").String(); got == "" || got == "install-123" {
		t.Fatalf("installation id was not confused: %q; body=%s", got, upstreamBody)
	}
}

func TestCodexIdentityExposeResponseRestoresClientIdentity(t *testing.T) {
	state := codexIdentityConfuseState{
		enabled:                true,
		authID:                 "auth-codex-plus",
		originalPromptCacheKey: "client-cache-1234567890",
		promptCacheKey:         codexIdentityConfuseUUID("auth-codex-plus", "prompt-cache", "client-cache-1234567890"),
		turnIDs: []codexIdentityReplacement{{
			original: "turn-123",
			confused: codexIdentityConfuseUUID("auth-codex-plus", "turn", "turn-123"),
		}},
	}

	upstreamPayload := []byte(`{"prompt_cache_key":"` + state.promptCacheKey + `","turn_id":"` + state.turnIDs[0].confused + `"}`)
	got := applyCodexIdentityExposeResponsePayload(upstreamPayload, state)
	if key := gjson.GetBytes(got, "prompt_cache_key").String(); key != "client-cache-1234567890" {
		t.Fatalf("prompt_cache_key = %q, want original; payload=%s", key, got)
	}
	if turnID := gjson.GetBytes(got, "turn_id").String(); turnID != "turn-123" {
		t.Fatalf("turn_id = %q, want original; payload=%s", turnID, got)
	}
}

func TestCodexExecuteInjectsClientMetadataInstallationID(t *testing.T) {
	var gotBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		gotBody = body
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_done\",\"output\":[]},\"usage\":{\"input_tokens\":1,\"output_tokens\":1,\"total_tokens\":2}}\n\n"))
	}))
	defer server.Close()

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	inboundReq, err := http.NewRequest(http.MethodPost, "https://example.com/inbound", nil)
	if err != nil {
		t.Fatalf("new inbound request: %v", err)
	}
	inboundReq.Header.Set("X-Codex-Installation-Id", "install-123")
	ginCtx.Request = inboundReq

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

	payload := []byte(`{"model":"gpt-5.5","input":"hi"}`)
	_, err = exec.Execute(context.WithValue(context.Background(), "gin", ginCtx), auth, cliproxyexecutor.Request{
		Model:   "gpt-5.5",
		Payload: payload,
	}, cliproxyexecutor.Options{
		SourceFormat: sdktranslator.FromString("openai-response"),
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	if got := gjson.GetBytes(gotBody, "client_metadata.x-codex-installation-id").String(); got != "install-123" {
		t.Fatalf("client_metadata.x-codex-installation-id = %q, want %q; body=%s", got, "install-123", gotBody)
	}
}

func TestApplyCodexHeadersSynthesizesWindowIDFromSessionID(t *testing.T) {
	req, err := http.NewRequest(http.MethodPost, "https://example.com/responses", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Session_id", "codex_prev_12345678901234567890")

	applyCodexHeaders(req, &cliproxyauth.Auth{Provider: "codex"}, "token", true)

	if got := req.Header.Get("X-Codex-Window-Id"); got != "codex_prev_12345678901234567890:0" {
		t.Fatalf("X-Codex-Window-Id = %q, want derived session window", got)
	}
}

func TestInjectCodexClientMetadataFallsBackToDeterministicInstallationID(t *testing.T) {
	token := fakeCodexJWT(t, "acct-123456")
	auth := &cliproxyauth.Auth{
		ID:       "codex-plus-test",
		Provider: "codex",
		Metadata: map[string]any{
			"access_token": token,
			"email":        "plus@example.com",
		},
	}

	gotBody := injectCodexClientMetadata([]byte(`{"model":"gpt-5.5","input":"hi"}`), nil, auth)
	want := resolveCodexInstallationID(auth)
	if want == "" {
		t.Fatal("resolveCodexInstallationID returned empty value")
	}
	if got := gjson.GetBytes(gotBody, "client_metadata.x-codex-installation-id").String(); got != want {
		t.Fatalf("client_metadata.x-codex-installation-id = %q, want %q; body=%s", got, want, gotBody)
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
