package executor

import (
	"net/http"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
)

func resetReverseProxyBanState() {
	reverseProxyBanState.mu.Lock()
	reverseProxyBanState.bannedTill = make(map[string]time.Time)
	reverseProxyBanState.mu.Unlock()
}

func TestResolveReverseProxyURLWithID_UsesWorkerBridge(t *testing.T) {
	resetReverseProxyBanState()
	cfg := &config.Config{
		ReverseProxyWorkerURL: "https://cpa-deno-bridge.mengcenfay.workers.dev",
		ReverseProxies: []config.ReverseProxy{
			{
				ID:      "deno-1",
				Name:    "deno-1",
				BaseURL: "https://funny-starfish-28.lauracadano-max.deno.net",
				Enabled: true,
			},
		},
	}

	got := resolveReverseProxyURLWithID(
		cfg,
		"deno-1",
		"codex",
		"https://chatgpt.com/backend-api/codex/responses?stream=true",
	)
	want := "https://cpa-deno-bridge.mengcenfay.workers.dev/codex/backend-api/codex/responses/funny-starfish-28.lauracadano-max.deno.net?stream=true"
	if got != want {
		t.Fatalf("unexpected worker routing url:\n got: %s\nwant: %s", got, want)
	}
}

func TestResolveReverseProxyURLWithID_FallsBackToClassicRewriteWithoutWorker(t *testing.T) {
	resetReverseProxyBanState()
	cfg := &config.Config{
		ReverseProxies: []config.ReverseProxy{
			{
				ID:      "deno-1",
				Name:    "deno-1",
				BaseURL: "https://funny-starfish-28.lauracadano-max.deno.net",
				Enabled: true,
			},
		},
	}

	got := resolveReverseProxyURLWithID(
		cfg,
		"deno-1",
		"codex",
		"https://chatgpt.com/backend-api/codex/responses",
	)
	want := "https://funny-starfish-28.lauracadano-max.deno.net/codex/backend-api/codex/responses"
	if got != want {
		t.Fatalf("unexpected classic routing url:\n got: %s\nwant: %s", got, want)
	}
}

func TestResolveReverseProxyURLWithID_AvoidsWorkerRecursion(t *testing.T) {
	resetReverseProxyBanState()
	cfg := &config.Config{
		ReverseProxyWorkerURL: "https://cpa-deno-bridge.mengcenfay.workers.dev",
		ReverseProxies: []config.ReverseProxy{
			{
				ID:      "worker",
				Name:    "worker",
				BaseURL: "https://cpa-deno-bridge.mengcenfay.workers.dev",
				Enabled: true,
			},
		},
	}

	got := resolveReverseProxyURLWithID(
		cfg,
		"worker",
		"codex",
		"https://chatgpt.com/backend-api/codex/responses",
	)
	want := "https://cpa-deno-bridge.mengcenfay.workers.dev/codex/backend-api/codex/responses"
	if got != want {
		t.Fatalf("unexpected recursion fallback url:\n got: %s\nwant: %s", got, want)
	}
}

func TestApplyReverseProxyHeaders_InjectsConfiguredHeaders(t *testing.T) {
	resetReverseProxyBanState()
	cfg := &config.Config{
		ProxyRouting: config.ProxyRouting{Codex: "deno-1"},
		ReverseProxies: []config.ReverseProxy{
			{
				ID:      "deno-1",
				Name:    "deno-1",
				BaseURL: "https://funny-starfish-28.lauracadano-max.deno.net",
				Enabled: true,
				Headers: map[string]string{
					"x-worker-token": "worker-secret",
				},
			},
		},
	}

	req, err := http.NewRequest(http.MethodPost, "https://example.com", nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}

	applyReverseProxyHeaders(req, cfg, nil, "codex")
	if got := req.Header.Get("x-worker-token"); got != "worker-secret" {
		t.Fatalf("unexpected x-worker-token header, got %q", got)
	}
}

func TestApplyReverseProxyHeaders_DoesNotOverrideExistingHeaders(t *testing.T) {
	resetReverseProxyBanState()
	cfg := &config.Config{
		ProxyRouting: config.ProxyRouting{Codex: "deno-1"},
		ReverseProxies: []config.ReverseProxy{
			{
				ID:      "deno-1",
				Name:    "deno-1",
				BaseURL: "https://funny-starfish-28.lauracadano-max.deno.net",
				Enabled: true,
				Headers: map[string]string{
					"x-worker-token": "worker-secret",
				},
			},
		},
	}

	req, err := http.NewRequest(http.MethodPost, "https://example.com", nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	req.Header.Set("x-worker-token", "already-set")

	applyReverseProxyHeaders(req, cfg, nil, "codex")
	if got := req.Header.Get("x-worker-token"); got != "already-set" {
		t.Fatalf("expected existing header to be preserved, got %q", got)
	}
}

func TestApplyReverseProxyHeaders_PrefersAuthRoutingOverProviderRouting(t *testing.T) {
	resetReverseProxyBanState()
	cfg := &config.Config{
		ProxyRouting: config.ProxyRouting{Codex: "deno-provider"},
		ProxyRoutingAuth: map[string]string{
			"auth-1": "deno-auth",
		},
		ReverseProxies: []config.ReverseProxy{
			{
				ID:      "deno-provider",
				Name:    "deno-provider",
				BaseURL: "https://provider-route.deno.dev",
				Enabled: true,
				Headers: map[string]string{
					"x-worker-token": "provider-token",
				},
			},
			{
				ID:      "deno-auth",
				Name:    "deno-auth",
				BaseURL: "https://auth-route.deno.dev",
				Enabled: true,
				Headers: map[string]string{
					"x-worker-token": "auth-token",
				},
			},
		},
	}

	req, err := http.NewRequest(http.MethodPost, "https://example.com", nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}

	auth := &cliproxyauth.Auth{ID: "auth-1"}
	applyReverseProxyHeaders(req, cfg, auth, "codex")
	if got := req.Header.Get("x-worker-token"); got != "auth-token" {
		t.Fatalf("expected auth-routed token, got %q", got)
	}
}

func TestResolveReverseProxyRouteForAuth_SkipsTemporarilyBannedProxy(t *testing.T) {
	resetReverseProxyBanState()
	cfg := &config.Config{
		ProxyRouting: config.ProxyRouting{Codex: "deno-1"},
		ReverseProxies: []config.ReverseProxy{
			{
				ID:      "deno-1",
				Name:    "deno-1",
				BaseURL: "https://funny-starfish-28.lauracadano-max.deno.net",
				Enabled: true,
			},
		},
	}
	originalURL := "https://chatgpt.com/backend-api/codex/responses"
	banReverseProxyTemporarily("deno-1", "codex", http.StatusNotFound, "status 404")

	route := resolveReverseProxyRouteForAuth(cfg, nil, "codex", originalURL)
	if route.URL != originalURL {
		t.Fatalf("expected direct URL when banned, got %q", route.URL)
	}
	if route.ProxyID != "deno-1" {
		t.Fatalf("unexpected proxy id, got %q", route.ProxyID)
	}
	if route.Proxied {
		t.Fatalf("expected proxied=false when proxy is banned")
	}
}

func TestIsReverseProxyTemporarilyBanned_ExpiresAutomatically(t *testing.T) {
	resetReverseProxyBanState()
	reverseProxyBanState.mu.Lock()
	reverseProxyBanState.bannedTill["deno-1"] = time.Now().Add(-time.Second)
	reverseProxyBanState.mu.Unlock()

	if isReverseProxyTemporarilyBanned("deno-1") {
		t.Fatalf("expected expired ban to be treated as inactive")
	}
	reverseProxyBanState.mu.Lock()
	_, ok := reverseProxyBanState.bannedTill["deno-1"]
	reverseProxyBanState.mu.Unlock()
	if ok {
		t.Fatalf("expected expired ban entry to be cleaned up")
	}
}

func TestShouldBanReverseProxyOnError(t *testing.T) {
	if !shouldBanReverseProxyOnError(http.StatusNotFound, "status 404") {
		t.Fatalf("expected 404 to trigger proxy ban")
	}
	if !shouldBanReverseProxyOnError(http.StatusOK, "请求详情") {
		t.Fatalf("expected request-detail marker to trigger proxy ban")
	}
	if shouldBanReverseProxyOnError(http.StatusBadRequest, "invalid model") {
		t.Fatalf("did not expect generic 400 to trigger proxy ban")
	}
}
