package executor

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v6/sdk/translator"
)

func newForwardProxyServer(t *testing.T, hits *atomic.Int32) *httptest.Server {
	t.Helper()

	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL == nil || r.URL.Scheme == "" || r.URL.Host == "" {
			http.Error(w, "expected absolute proxy request URL", http.StatusBadRequest)
			return
		}

		req := r.Clone(r.Context())
		req.RequestURI = ""

		resp, err := transport.RoundTrip(req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()

		for key, values := range resp.Header {
			for _, value := range values {
				w.Header().Add(key, value)
			}
		}
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	}))
}

func executeOpenAICompatRequest(t *testing.T, cfg *config.Config, auth *cliproxyauth.Auth) {
	t.Helper()

	exec := NewOpenAICompatExecutor("openai-compatibility", cfg)
	_, err := exec.Execute(context.Background(), auth, cliproxyexecutor.Request{
		Model:   "gpt-5.1",
		Payload: []byte(`{"model":"gpt-5.1","input":"hi"}`),
	}, cliproxyexecutor.Options{
		SourceFormat: sdktranslator.FromString("openai-chat"),
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
}

func TestOpenAICompatExecutorExecute_UsesGlobalProxyURL(t *testing.T) {
	var upstreamHits atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamHits.Add(1)
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("unexpected upstream path %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl-1","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))
	}))
	defer upstream.Close()

	var proxyHits atomic.Int32
	proxy := newForwardProxyServer(t, &proxyHits)
	defer proxy.Close()

	cfg := &config.Config{SDKConfig: config.SDKConfig{ProxyURL: proxy.URL}}
	auth := &cliproxyauth.Auth{
		Attributes: map[string]string{
			"base_url": upstream.URL + "/v1",
			"api_key":  "test",
		},
	}

	executeOpenAICompatRequest(t, cfg, auth)

	if got := proxyHits.Load(); got != 1 {
		t.Fatalf("expected request to pass through global proxy once, got %d", got)
	}
	if got := upstreamHits.Load(); got != 1 {
		t.Fatalf("expected upstream to receive one request, got %d", got)
	}
}

func TestOpenAICompatExecutorExecute_PrefersAuthProxyURLOverGlobal(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl-1","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))
	}))
	defer upstream.Close()

	var globalProxyHits atomic.Int32
	globalProxy := newForwardProxyServer(t, &globalProxyHits)
	defer globalProxy.Close()

	var authProxyHits atomic.Int32
	authProxy := newForwardProxyServer(t, &authProxyHits)
	defer authProxy.Close()

	cfg := &config.Config{SDKConfig: config.SDKConfig{ProxyURL: globalProxy.URL}}
	auth := &cliproxyauth.Auth{
		ProxyURL: authProxy.URL,
		Attributes: map[string]string{
			"base_url": upstream.URL + "/v1",
			"api_key":  "test",
		},
	}

	executeOpenAICompatRequest(t, cfg, auth)

	if got := authProxyHits.Load(); got != 1 {
		t.Fatalf("expected request to use auth proxy once, got %d", got)
	}
	if got := globalProxyHits.Load(); got != 0 {
		t.Fatalf("expected global proxy to be bypassed, got %d hits", got)
	}
}

func TestOpenAICompatExecutorExecute_UsesUpdatedGlobalProxyURLAtRuntime(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl-1","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))
	}))
	defer upstream.Close()

	var proxyOneHits atomic.Int32
	proxyOne := newForwardProxyServer(t, &proxyOneHits)
	defer proxyOne.Close()

	var proxyTwoHits atomic.Int32
	proxyTwo := newForwardProxyServer(t, &proxyTwoHits)
	defer proxyTwo.Close()

	cfg := &config.Config{SDKConfig: config.SDKConfig{ProxyURL: proxyOne.URL}}
	auth := &cliproxyauth.Auth{
		Attributes: map[string]string{
			"base_url": upstream.URL + "/v1",
			"api_key":  "test",
		},
	}

	executeOpenAICompatRequest(t, cfg, auth)

	cfg.ProxyURL = proxyTwo.URL
	executeOpenAICompatRequest(t, cfg, auth)

	if got := proxyOneHits.Load(); got != 1 {
		t.Fatalf("expected first proxy to receive exactly one request, got %d", got)
	}
	if got := proxyTwoHits.Load(); got != 1 {
		t.Fatalf("expected updated proxy to receive exactly one request, got %d", got)
	}
}
