package management

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	coreusage "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/usage"
)

func TestProbeCodexQuotaUsageUsesOfficialHeadersProxyAndStoresSnapshot(t *testing.T) {
	originalURL := codexQuotaSamplerURL
	codexQuotaSamplerURL = "http://chatgpt.com/backend-api/wham/usage"
	t.Cleanup(func() { codexQuotaSamplerURL = originalURL })

	type observed struct {
		method  string
		url     string
		headers http.Header
	}
	seen := make(chan observed, 1)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- observed{method: r.Method, url: r.URL.String(), headers: r.Header.Clone()}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"rate_limit":{"limit_reached":false,"primary_window":{"used_percent":20,"reset_after_seconds":18000}}}`))
	}))
	t.Cleanup(proxy.Close)

	store := &memoryAuthStore{}
	manager := coreauth.NewManager(store, nil, nil)
	auth := &coreauth.Auth{
		ID:       "sampler-codex.json",
		FileName: "sampler-codex.json",
		Provider: "codex",
		ProxyURL: proxy.URL,
		Metadata: map[string]any{"access_token": "secret-token", "account_id": "acct-123", "email": "user@example.com"},
	}
	registered, err := manager.Register(context.Background(), auth)
	if err != nil {
		t.Fatalf("register auth: %v", err)
	}
	before := len(coreusage.DefaultQuotaAuditStore().Export().Snapshots)
	h := &Handler{authManager: manager}
	if err := h.probeCodexQuotaUsage(context.Background(), coreusage.QuotaProbeRequest{AuthID: registered.ID}); err != nil {
		t.Fatalf("probe: %v", err)
	}

	select {
	case request := <-seen:
		if request.method != http.MethodGet {
			t.Fatalf("method = %q, want GET", request.method)
		}
		if !strings.Contains(request.url, "chatgpt.com/backend-api/wham/usage") {
			t.Fatalf("proxy request URL = %q", request.url)
		}
		if got := request.headers.Get("Authorization"); got != "Bearer secret-token" {
			t.Fatalf("authorization = %q", got)
		}
		if got := request.headers.Get("ChatGPT-Account-ID"); got != "acct-123" {
			t.Fatalf("account ID = %q", got)
		}
		if got := request.headers.Get("Accept"); got != "application/json" {
			t.Fatalf("accept = %q", got)
		}
		if got := request.headers.Get("User-Agent"); got != codexQuotaSamplerUserAgent || !strings.Contains(got, "0.147.0") {
			t.Fatalf("user agent = %q", got)
		}
	default:
		t.Fatal("proxy did not observe quota request")
	}

	after := len(coreusage.DefaultQuotaAuditStore().Export().Snapshots)
	if after != before+1 {
		t.Fatalf("stored snapshots = %d, want %d", after, before+1)
	}
	if body, _ := json.Marshal(coreusage.DefaultQuotaAuditStore().Export()); strings.Contains(string(body), "secret-token") {
		t.Fatal("quota audit export contains bearer token")
	}
}

func TestProbeCodexQuotaUsageRejectsUnrecognizedJSON(t *testing.T) {
	originalURL := codexQuotaSamplerURL
	codexQuotaSamplerURL = "http://chatgpt.com/backend-api/wham/usage"
	t.Cleanup(func() { codexQuotaSamplerURL = originalURL })
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(proxy.Close)
	manager := coreauth.NewManager(&memoryAuthStore{}, nil, nil)
	auth := &coreauth.Auth{ID: "unrecognized-codex.json", FileName: "unrecognized-codex.json", Provider: "codex", ProxyURL: proxy.URL, Metadata: map[string]any{"access_token": "token"}}
	registered, err := manager.Register(context.Background(), auth)
	if err != nil {
		t.Fatalf("register auth: %v", err)
	}
	h := &Handler{authManager: manager}
	if err := h.probeCodexQuotaUsage(context.Background(), coreusage.QuotaProbeRequest{AuthID: registered.ID}); err == nil {
		t.Fatal("expected unrecognized payload error")
	}
}
