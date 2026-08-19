package executor

import (
	"context"
	"net/http"
	"sync"
	"testing"

	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v6/sdk/translator"
	"github.com/tidwall/gjson"
)

func TestCodexThreadNamespaceSeparatesAuthsAndPreservesSameAuth(t *testing.T) {
	authA := threadIsolationTestAuth("auth-a", "account-a")
	authB := threadIsolationTestAuth("auth-b", "account-b")
	opts := cliproxyexecutor.Options{
		SourceFormat: sdktranslator.FromString("openai-response"),
		Metadata:     map[string]any{cliproxyexecutor.ClientAPIKeyMetadataKey: "tenant-a"},
	}
	req := cliproxyexecutor.Request{
		Model:   "gpt-5-codex",
		Payload: []byte(`{"model":"gpt-5-codex","input":"hi","prompt_cache_key":"shared-client-thread-1234567890"}`),
	}
	body := req.Payload
	exec := NewCodexExecutor(nil)

	_, bodyA, stateA, err := exec.cacheHelper(context.Background(), opts.SourceFormat, "https://example.com/responses", authA, req, opts, req.Payload, body)
	if err != nil {
		t.Fatalf("auth A cacheHelper error: %v", err)
	}
	_, bodyB, stateB, err := exec.cacheHelper(context.Background(), opts.SourceFormat, "https://example.com/responses", authB, req, opts, req.Payload, body)
	if err != nil {
		t.Fatalf("auth B cacheHelper error: %v", err)
	}
	if stateA.threadIsolation.canonicalPromptCacheKey == stateB.threadIsolation.canonicalPromptCacheKey {
		t.Fatalf("auth namespaces collided: %q", stateA.threadIsolation.canonicalPromptCacheKey)
	}
	if got := gjson.GetBytes(bodyA, "prompt_cache_key").String(); got != stateA.threadIsolation.canonicalPromptCacheKey {
		t.Fatalf("auth A prompt_cache_key = %q, want %q", got, stateA.threadIsolation.canonicalPromptCacheKey)
	}
	if got := gjson.GetBytes(bodyB, "prompt_cache_key").String(); got != stateB.threadIsolation.canonicalPromptCacheKey {
		t.Fatalf("auth B prompt_cache_key = %q, want %q", got, stateB.threadIsolation.canonicalPromptCacheKey)
	}

	_, bodyASecond, stateASecond, err := exec.cacheHelper(context.Background(), opts.SourceFormat, "https://example.com/responses", authA, req, opts, req.Payload, body)
	if err != nil {
		t.Fatalf("same auth cacheHelper error: %v", err)
	}
	if stateA.threadIsolation.canonicalPromptCacheKey != stateASecond.threadIsolation.canonicalPromptCacheKey {
		t.Fatalf("same auth thread changed: %q != %q", stateA.threadIsolation.canonicalPromptCacheKey, stateASecond.threadIsolation.canonicalPromptCacheKey)
	}
	if gjson.GetBytes(bodyA, "prompt_cache_key").String() != gjson.GetBytes(bodyASecond, "prompt_cache_key").String() {
		t.Fatal("same auth prompt cache key was not stable")
	}
}

func TestCodexThreadIsolationStripsForeignProviderThreadHeaders(t *testing.T) {
	exec := NewCodexExecutor(nil)
	opts := cliproxyexecutor.Options{SourceFormat: sdktranslator.FromString("openai-response")}
	req := cliproxyexecutor.Request{
		Model:   "gpt-5-codex",
		Payload: []byte(`{"model":"gpt-5-codex","input":"hi","prompt_cache_key":"shared-client-thread-1234567890"}`),
	}
	authA := threadIsolationTestAuth("auth-a", "account-a")
	authB := threadIsolationTestAuth("auth-b", "account-b")

	makeRequest := func(auth *cliproxyauth.Auth) *http.Request {
		reqHTTP, _, state, err := exec.cacheHelper(context.Background(), opts.SourceFormat, "https://example.com/responses", auth, req, opts, req.Payload, req.Payload)
		if err != nil {
			t.Fatalf("cacheHelper error: %v", err)
		}
		reqHTTP.Header.Set("X-Codex-Parent-Thread-Id", "provider-thread-from-another-account")
		reqHTTP.Header.Set("X-Codex-Window-Id", "provider-window-from-another-account:0")
		applyCodexHeaders(reqHTTP, auth, "token", true)
		applyCodexIdentityConfuseHeaders(reqHTTP.Header, &state)
		return reqHTTP
	}

	requestA := makeRequest(authA)
	requestB := makeRequest(authB)
	if requestA.Header.Get("X-Codex-Parent-Thread-Id") != "" || requestB.Header.Get("X-Codex-Parent-Thread-Id") != "" {
		t.Fatal("foreign parent thread header was forwarded")
	}
	if requestA.Header.Get("X-Codex-Window-Id") == requestB.Header.Get("X-Codex-Window-Id") {
		t.Fatal("window header was shared across auths")
	}
	if requestA.Header.Get("Thread-Id") == requestB.Header.Get("Thread-Id") {
		t.Fatal("thread header was shared across auths")
	}
}

func TestCodexThreadIsolationOwnsPreviousResponseIDs(t *testing.T) {
	exec := NewCodexExecutor(nil)
	opts := cliproxyexecutor.Options{SourceFormat: sdktranslator.FromString("openai-response")}
	authA := threadIsolationTestAuth("auth-a", "account-a")
	authB := threadIsolationTestAuth("auth-b", "account-b")
	stateA := newCodexThreadIsolationState(authA, "gpt-5-codex", "shared-client-thread-1234567890", opts)
	identityA := codexIdentityConfuseState{threadIsolation: stateA}
	upstream := []byte(`data: {"type":"response.completed","response":{"id":"resp-account-a-1234567890"}}`)
	clientPayload := applyCodexClientResponsePayload(upstream, identityA)
	clientResponseID := gjson.GetBytes(clientPayload[len("data:"):], "response.id").String()
	if clientResponseID == "" || clientResponseID == "resp-account-a-1234567890" {
		t.Fatalf("provider response id was not made opaque: %q", clientResponseID)
	}

	makeBody := func(auth *cliproxyauth.Auth) []byte {
		req := cliproxyexecutor.Request{Model: "gpt-5-codex", Payload: []byte(`{"model":"gpt-5-codex","input":"next","prompt_cache_key":"shared-client-thread-1234567890","previous_response_id":"` + clientResponseID + `"}`)}
		_, body, _, err := exec.cacheHelper(context.Background(), opts.SourceFormat, "https://example.com/responses", auth, req, opts, req.Payload, req.Payload)
		if err != nil {
			t.Fatalf("cacheHelper error: %v", err)
		}
		return body
	}

	bodyA := makeBody(authA)
	if got := gjson.GetBytes(bodyA, "previous_response_id").String(); got != "resp-account-a-1234567890" {
		t.Fatalf("same auth previous_response_id = %q, want provider id", got)
	}
	bodyB := makeBody(authB)
	if gjson.GetBytes(bodyB, "previous_response_id").Exists() {
		t.Fatalf("foreign previous_response_id was forwarded: %s", bodyB)
	}
}

func TestCodexThreadNamespaceIsStableConcurrently(t *testing.T) {
	auth := threadIsolationTestAuth("auth-concurrent", "account-concurrent")
	auth.EnsureIndex()
	exec := NewCodexExecutor(nil)
	opts := cliproxyexecutor.Options{SourceFormat: sdktranslator.FromString("openai-response")}
	req := cliproxyexecutor.Request{Model: "gpt-5-codex", Payload: []byte(`{"model":"gpt-5-codex","input":"hi","prompt_cache_key":"shared-client-thread-1234567890"}`)}
	const workers = 16
	keys := make(chan string, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, body, _, err := exec.cacheHelper(context.Background(), opts.SourceFormat, "https://example.com/responses", auth, req, opts, req.Payload, req.Payload)
			if err != nil {
				t.Errorf("cacheHelper error: %v", err)
				return
			}
			keys <- gjson.GetBytes(body, "prompt_cache_key").String()
		}()
	}
	wg.Wait()
	close(keys)
	var want string
	for key := range keys {
		if want == "" {
			want = key
		}
		if key != want {
			t.Fatalf("concurrent namespace changed: %q != %q", key, want)
		}
	}
}

func TestCodexThreadIsolationAllowsRequestsWithoutThreadIdentifier(t *testing.T) {
	exec := NewCodexExecutor(nil)
	auth := threadIsolationTestAuth("auth-no-thread", "account-no-thread")
	opts := cliproxyexecutor.Options{SourceFormat: sdktranslator.FromString("openai-response")}
	req := cliproxyexecutor.Request{Model: "gpt-5-codex", Payload: []byte(`{"model":"gpt-5-codex","input":"first request"}`)}
	httpReq, body, state, err := exec.cacheHelper(context.Background(), opts.SourceFormat, "https://example.com/responses", auth, req, opts, req.Payload, req.Payload)
	if err != nil {
		t.Fatalf("cacheHelper error: %v", err)
	}
	if state.threadIsolation.enabled || gjson.GetBytes(body, "prompt_cache_key").Exists() {
		t.Fatalf("threadless request was assigned a persistent thread: %s", body)
	}
	httpReq.Header.Set("Session_id", "client-session-1234567890")
	applyCodexHeaders(httpReq, auth, "token", true)
	applyCodexIdentityConfuseHeaders(httpReq.Header, &state)
	if httpReq.Header.Get("Session_id") == "" {
		t.Fatal("threadless request did not receive a normal request session id")
	}
	if httpReq.Header.Get("Session_id") == "client-session-1234567890" {
		t.Fatal("threadless request reused client Session_id")
	}
}

func TestCodexThreadIsolationUsesStableIndexWhenAuthIDMissing(t *testing.T) {
	exec := NewCodexExecutor(nil)
	opts := cliproxyexecutor.Options{SourceFormat: sdktranslator.FromString("openai-response")}
	req := cliproxyexecutor.Request{Model: "gpt-5-codex", Payload: []byte(`{"model":"gpt-5-codex","input":"hi","prompt_cache_key":"shared-client-thread-1234567890","previous_response_id":"provider-thread-1234567890"}`)}
	authA := &cliproxyauth.Auth{Provider: "codex", FileName: "account-a.json", Metadata: map[string]any{"account_id": "account-a"}}
	authB := &cliproxyauth.Auth{Provider: "codex", FileName: "account-b.json", Metadata: map[string]any{"account_id": "account-b"}}
	_, bodyA, stateA, err := exec.cacheHelper(context.Background(), opts.SourceFormat, "https://example.com/responses", authA, req, opts, req.Payload, req.Payload)
	if err != nil {
		t.Fatalf("auth A cacheHelper error: %v", err)
	}
	_, bodyB, stateB, err := exec.cacheHelper(context.Background(), opts.SourceFormat, "https://example.com/responses", authB, req, opts, req.Payload, req.Payload)
	if err != nil {
		t.Fatalf("auth B cacheHelper error: %v", err)
	}
	if !stateA.threadIsolation.enabled || !stateB.threadIsolation.enabled {
		t.Fatal("stable file index did not enable thread isolation")
	}
	if stateA.threadIsolation.canonicalPromptCacheKey == stateB.threadIsolation.canonicalPromptCacheKey {
		t.Fatal("file-index namespaces collided")
	}
	if gjson.GetBytes(bodyA, "previous_response_id").Exists() || gjson.GetBytes(bodyB, "previous_response_id").Exists() {
		t.Fatal("unowned provider thread was forwarded without auth ID")
	}
}

func threadIsolationTestAuth(id, accountID string) *cliproxyauth.Auth {
	return &cliproxyauth.Auth{
		ID:       id,
		Provider: "codex",
		Metadata: map[string]any{"account_id": accountID},
	}
}
