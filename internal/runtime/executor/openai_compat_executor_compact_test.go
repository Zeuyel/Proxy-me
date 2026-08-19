package executor

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v6/sdk/translator"
	"github.com/tidwall/gjson"
)

func TestOpenAICompatExecutorCompactPassthrough(t *testing.T) {
	var gotPath string
	var gotBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		gotBody = body
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"resp_1","object":"response.compaction","usage":{"input_tokens":1,"output_tokens":2,"total_tokens":3}}`))
	}))
	defer server.Close()

	executor := NewOpenAICompatExecutor("openai-compatibility", &config.Config{})
	auth := &cliproxyauth.Auth{Attributes: map[string]string{
		"base_url": server.URL + "/v1",
		"api_key":  "test",
	}}
	payload := []byte(`{"model":"gpt-5.1-codex-max","input":[{"role":"user","content":"hi"}]}`)
	resp, err := executor.Execute(context.Background(), auth, cliproxyexecutor.Request{
		Model:   "gpt-5.1-codex-max",
		Payload: payload,
	}, cliproxyexecutor.Options{
		SourceFormat: sdktranslator.FromString("openai-response"),
		Alt:          "responses/compact",
		Stream:       false,
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if gotPath != "/v1/responses/compact" {
		t.Fatalf("path = %q, want %q", gotPath, "/v1/responses/compact")
	}
	if !gjson.GetBytes(gotBody, "input").Exists() {
		t.Fatalf("expected input in body")
	}
	if gjson.GetBytes(gotBody, "messages").Exists() {
		t.Fatalf("unexpected messages in body")
	}
	if string(resp.Payload) != `{"id":"resp_1","object":"response.compaction","usage":{"input_tokens":1,"output_tokens":2,"total_tokens":3}}` {
		t.Fatalf("payload = %s", string(resp.Payload))
	}
}

func TestSanitizeOpenAICompatPayloadKeepsUserContentAndDropsClientIdentity(t *testing.T) {
	raw := []byte(`{"model":"gpt-5.1","user":"tenant-user","client_metadata":{"email":"client@example.com"},"metadata":{"trace_id":"trace-123","tenant":"provider-value"},"messages":[{"role":"user","content":"hello","user":"message-content"}]}`)
	got := sanitizeOpenAICompatPayload(raw)
	if gjson.GetBytes(got, "user").String() != "tenant-user" {
		t.Fatalf("top-level user = %q, want preserved provider field", gjson.GetBytes(got, "user").String())
	}
	if gjson.GetBytes(got, "messages.0.user").String() != "message-content" {
		t.Fatalf("message user = %q, want preserved content", gjson.GetBytes(got, "messages.0.user").String())
	}
	if gjson.GetBytes(got, "client_metadata").Exists() {
		t.Fatalf("client_metadata should be removed: %s", got)
	}
	if gjson.GetBytes(got, "metadata.trace_id").Exists() {
		t.Fatalf("metadata.trace_id should be removed: %s", got)
	}
	if gjson.GetBytes(got, "metadata.tenant").String() != "provider-value" {
		t.Fatalf("provider metadata was removed: %s", got)
	}
}
