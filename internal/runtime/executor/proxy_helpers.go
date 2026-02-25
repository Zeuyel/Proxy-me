package executor

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	log "github.com/sirupsen/logrus"
	"golang.org/x/net/proxy"
)

const reverseProxyBanTTL = 5 * time.Minute

type reverseProxyResolution struct {
	URL     string
	ProxyID string
	Proxied bool
}

var reverseProxyBanState = struct {
	mu         sync.Mutex
	bannedTill map[string]time.Time
}{
	bannedTill: make(map[string]time.Time),
}

// newProxyAwareHTTPClient creates an HTTP client with proper proxy configuration priority:
// 1. Use auth.ProxyURL if configured (highest priority)
// 2. Use cfg.ProxyURL if auth proxy is not configured
// 3. Use RoundTripper from context if neither are configured
//
// Parameters:
//   - ctx: The context containing optional RoundTripper
//   - cfg: The application configuration
//   - auth: The authentication information
//   - timeout: The client timeout (0 means no timeout)
//
// Returns:
//   - *http.Client: An HTTP client with configured proxy or transport
func newProxyAwareHTTPClient(ctx context.Context, cfg *config.Config, auth *cliproxyauth.Auth, timeout time.Duration) *http.Client {
	httpClient := &http.Client{}
	if timeout > 0 {
		httpClient.Timeout = timeout
	}

	// Priority 1: Use auth.ProxyURL if configured
	var proxyURL string
	if auth != nil {
		proxyURL = strings.TrimSpace(auth.ProxyURL)
	}

	// Priority 2: Use cfg.ProxyURL if auth proxy is not configured
	if proxyURL == "" && cfg != nil {
		proxyURL = strings.TrimSpace(cfg.ProxyURL)
	}

	// If we have a proxy URL configured, set up the transport
	if proxyURL != "" {
		transport := buildProxyTransport(proxyURL)
		if transport != nil {
			httpClient.Transport = transport
			return httpClient
		}
		// If proxy setup failed, log and fall through to context RoundTripper
		log.Debugf("failed to setup proxy from URL: %s, falling back to context transport", proxyURL)
	}

	// Priority 3: Use RoundTripper from context (typically from RoundTripperFor)
	if rt, ok := ctx.Value("cliproxy.roundtripper").(http.RoundTripper); ok && rt != nil {
		httpClient.Transport = rt
	} else {
		// No proxy configured, use default transport.
		httpClient.Transport = &http.Transport{}
	}

	return httpClient
}

// buildProxyTransport creates an HTTP transport configured for the given proxy URL.
// It supports SOCKS5, HTTP, and HTTPS proxy protocols.
//
// Parameters:
//   - proxyURL: The proxy URL string (e.g., "socks5://user:pass@host:port", "http://host:port")
//
// Returns:
//   - *http.Transport: A configured transport, or nil if the proxy URL is invalid
func buildProxyTransport(proxyURL string) *http.Transport {
	if proxyURL == "" {
		return nil
	}

	parsedURL, errParse := url.Parse(proxyURL)
	if errParse != nil {
		log.Errorf("parse proxy URL failed: %v", errParse)
		return nil
	}

	var transport *http.Transport

	// Handle different proxy schemes
	if parsedURL.Scheme == "socks5" {
		// Configure SOCKS5 proxy with optional authentication
		var proxyAuth *proxy.Auth
		if parsedURL.User != nil {
			username := parsedURL.User.Username()
			password, _ := parsedURL.User.Password()
			proxyAuth = &proxy.Auth{User: username, Password: password}
		}
		dialer, errSOCKS5 := proxy.SOCKS5("tcp", parsedURL.Host, proxyAuth, proxy.Direct)
		if errSOCKS5 != nil {
			log.Errorf("create SOCKS5 dialer failed: %v", errSOCKS5)
			return nil
		}
		// Set up a custom transport using the SOCKS5 dialer
		transport = &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return dialer.Dial(network, addr)
			},
		}
	} else if parsedURL.Scheme == "http" || parsedURL.Scheme == "https" {
		// Configure HTTP or HTTPS proxy
		transport = &http.Transport{
			Proxy: http.ProxyURL(parsedURL),
		}
	} else {
		log.Errorf("unsupported proxy scheme: %s", parsedURL.Scheme)
		return nil
	}

	return transport
}

// resolveReverseProxyURL resolves the final URL based on reverse proxy configuration.
// If a reverse proxy is configured for the given provider, it rewrites the URL to route
// through the proxy endpoint.
//
// Parameters:
//   - cfg: The application configuration containing reverse proxy settings
//   - provider: The provider name (e.g., "codex", "antigravity", "claude")
//   - originalURL: The original target URL
//
// Returns:
//   - string: The resolved URL (either proxied or original)
func resolveReverseProxyURL(cfg *config.Config, provider string, originalURL string) string {
	return resolveReverseProxyRoute(cfg, provider, originalURL).URL
}

// resolveReverseProxyURLForAuth resolves the reverse proxy URL using per-auth routing when available.
// It falls back to provider routing when no auth-specific proxy is configured.
func resolveReverseProxyURLForAuth(cfg *config.Config, auth *cliproxyauth.Auth, provider string, originalURL string) string {
	return resolveReverseProxyRouteForAuth(cfg, auth, provider, originalURL).URL
}

func resolveReverseProxyRoute(cfg *config.Config, provider string, originalURL string) reverseProxyResolution {
	proxyID := resolveProxyIDForProvider(cfg, provider)
	return resolveReverseProxyRouteWithID(cfg, proxyID, provider, originalURL)
}

func resolveReverseProxyRouteForAuth(cfg *config.Config, auth *cliproxyauth.Auth, provider string, originalURL string) reverseProxyResolution {
	proxyID := resolveProxyIDForAuth(cfg, auth)
	if proxyID == "" {
		proxyID = resolveProxyIDForProvider(cfg, provider)
	}
	return resolveReverseProxyRouteWithID(cfg, proxyID, provider, originalURL)
}

func resolveReverseProxyRouteWithID(cfg *config.Config, proxyID string, provider string, originalURL string) reverseProxyResolution {
	result := reverseProxyResolution{
		URL:     originalURL,
		ProxyID: strings.TrimSpace(proxyID),
		Proxied: false,
	}
	if result.ProxyID == "" {
		return result
	}
	if isReverseProxyTemporarilyBanned(result.ProxyID) {
		log.Warnf("reverse proxy %s temporarily banned, fallback to direct for provider %s", result.ProxyID, provider)
		return result
	}
	result.URL = resolveReverseProxyURLWithID(cfg, result.ProxyID, provider, originalURL)
	result.Proxied = result.URL != originalURL
	return result
}

func resolveProxyIDForProvider(cfg *config.Config, provider string) string {
	if cfg == nil {
		return ""
	}

	switch provider {
	case "codex":
		return cfg.ProxyRouting.Codex
	case "antigravity":
		return cfg.ProxyRouting.Antigravity
	case "claude":
		return cfg.ProxyRouting.Claude
	case "gemini":
		return cfg.ProxyRouting.Gemini
	case "gemini-cli":
		return cfg.ProxyRouting.GeminiCLI
	case "vertex":
		return cfg.ProxyRouting.Vertex
	case "aistudio":
		return cfg.ProxyRouting.AIStudio
	case "qwen":
		return cfg.ProxyRouting.Qwen
	case "iflow":
		return cfg.ProxyRouting.IFlow
	default:
		return ""
	}
}

func resolveProxyIDForAuth(cfg *config.Config, auth *cliproxyauth.Auth) string {
	if cfg == nil || auth == nil || len(cfg.ProxyRoutingAuth) == 0 {
		return ""
	}

	if id := strings.TrimSpace(auth.ID); id != "" {
		if proxyID := strings.TrimSpace(cfg.ProxyRoutingAuth[id]); proxyID != "" {
			return proxyID
		}
	}

	if idx := strings.TrimSpace(auth.EnsureIndex()); idx != "" {
		if proxyID := strings.TrimSpace(cfg.ProxyRoutingAuth[idx]); proxyID != "" {
			return proxyID
		}
	}

	if name := strings.TrimSpace(auth.FileName); name != "" {
		if proxyID := strings.TrimSpace(cfg.ProxyRoutingAuth[name]); proxyID != "" {
			return proxyID
		}
	}

	return ""
}

func resolveReverseProxyURLWithID(cfg *config.Config, proxyID string, provider string, originalURL string) string {
	if cfg == nil || len(cfg.ReverseProxies) == 0 || proxyID == "" {
		return originalURL
	}

	proxyConfig := findReverseProxyByID(cfg, proxyID)
	if proxyConfig == nil {
		log.Debugf("reverse proxy %s not found or disabled for provider %s", proxyID, provider)
		return originalURL
	}

	// Parse the original URL
	parsedURL, err := url.Parse(originalURL)
	if err != nil {
		log.Errorf("failed to parse original URL %s: %v", originalURL, err)
		return originalURL
	}

	// Build the new URL using fixed prefix mapping
	// Format: proxyBaseURL/prefix/path?query
	// where prefix is determined by the provider and original host
	//
	// Example:
	//   Original: https://daily-cloudcode-pa.sandbox.googleapis.com/v1internal:streamGenerateContent
	//   Rewritten: https://your-proxy.deno.dev/antigravity-sandbox/v1internal:streamGenerateContent
	proxyBase := strings.TrimSuffix(proxyConfig.BaseURL, "/")

	// Determine the prefix based on provider and host
	var prefix string
	if provider == "antigravity" {
		// Map Antigravity domains to fixed prefixes
		switch parsedURL.Host {
		case "daily-cloudcode-pa.sandbox.googleapis.com":
			prefix = "/antigravity-sandbox"
		case "daily-cloudcode-pa.googleapis.com":
			prefix = "/antigravity-daily"
		case "cloudcode-pa.googleapis.com":
			prefix = "/antigravity-cloudcode"
		default:
			// Fallback to sandbox
			prefix = "/antigravity-sandbox"
		}
	} else if provider == "codex" {
		prefix = "/codex"
	} else {
		// For other providers, use the provider name as prefix
		prefix = "/" + provider
	}

	newPath := parsedURL.Path
	if !strings.HasPrefix(newPath, "/") {
		newPath = "/" + newPath
	}

	workerURL := buildReverseProxyWorkerURL(cfg, proxyConfig.BaseURL, prefix, newPath, parsedURL.RawQuery)
	if workerURL != "" {
		log.Debugf("reverse proxy: %s -> %s (via worker %s, proxy %s)", originalURL, workerURL, cfg.ReverseProxyWorkerURL, proxyConfig.Name)
		return workerURL
	}

	newURL := fmt.Sprintf("%s%s%s", proxyBase, prefix, newPath)
	if parsedURL.RawQuery != "" {
		newURL += "?" + parsedURL.RawQuery
	}

	log.Debugf("reverse proxy: %s -> %s (via %s)", originalURL, newURL, proxyConfig.Name)
	return newURL
}

func findReverseProxyByID(cfg *config.Config, proxyID string) *config.ReverseProxy {
	if cfg == nil || proxyID == "" || len(cfg.ReverseProxies) == 0 {
		return nil
	}
	for i := range cfg.ReverseProxies {
		if cfg.ReverseProxies[i].ID == proxyID && cfg.ReverseProxies[i].Enabled {
			return &cfg.ReverseProxies[i]
		}
	}
	return nil
}

func applyReverseProxyHeaders(req *http.Request, cfg *config.Config, auth *cliproxyauth.Auth, provider string) {
	if req == nil || cfg == nil {
		return
	}

	proxyID := resolveProxyIDForAuth(cfg, auth)
	if proxyID == "" {
		proxyID = resolveProxyIDForProvider(cfg, provider)
	}
	if proxyID == "" {
		return
	}
	if isReverseProxyTemporarilyBanned(proxyID) {
		return
	}

	proxyConfig := findReverseProxyByID(cfg, proxyID)
	if proxyConfig == nil || len(proxyConfig.Headers) == 0 {
		return
	}

	for key, value := range proxyConfig.Headers {
		k := strings.TrimSpace(key)
		v := strings.TrimSpace(value)
		if k == "" || v == "" {
			continue
		}
		if req.Header.Get(k) != "" {
			continue
		}
		req.Header.Set(k, v)
	}
}

func shouldBanReverseProxyOnError(statusCode int, errMsg string) bool {
	switch statusCode {
	case http.StatusNotFound, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	case 520, 521, 522, 523, 524:
		return true
	}

	msg := strings.ToLower(strings.TrimSpace(errMsg))
	if msg == "" {
		return false
	}

	if strings.Contains(msg, "请求详情") {
		return true
	}

	patterns := []string{
		"request detail",
		"status 404",
		"\"status\":404",
		"/v1/v1/messages",
	}
	for _, p := range patterns {
		if strings.Contains(msg, p) {
			return true
		}
	}

	return false
}

func banReverseProxyTemporarily(proxyID string, provider string, statusCode int, errMsg string) {
	id := strings.TrimSpace(proxyID)
	if id == "" {
		return
	}
	until := time.Now().Add(reverseProxyBanTTL)
	reverseProxyBanState.mu.Lock()
	if current, ok := reverseProxyBanState.bannedTill[id]; ok && current.After(until) {
		until = current
	}
	reverseProxyBanState.bannedTill[id] = until
	reverseProxyBanState.mu.Unlock()
	log.Warnf("temporarily banning reverse proxy %s for provider %s until %s due to upstream error status=%d detail=%s", id, provider, until.Format(time.RFC3339), statusCode, shortenBanReason(errMsg))
}

func isReverseProxyTemporarilyBanned(proxyID string) bool {
	id := strings.TrimSpace(proxyID)
	if id == "" {
		return false
	}
	now := time.Now()
	reverseProxyBanState.mu.Lock()
	defer reverseProxyBanState.mu.Unlock()
	until, ok := reverseProxyBanState.bannedTill[id]
	if !ok {
		return false
	}
	if now.After(until) {
		delete(reverseProxyBanState.bannedTill, id)
		return false
	}
	return true
}

func shortenBanReason(msg string) string {
	trimmed := strings.TrimSpace(msg)
	const maxLen = 256
	if len(trimmed) <= maxLen {
		return trimmed
	}
	return trimmed[:maxLen] + "..."
}

func buildReverseProxyWorkerURL(cfg *config.Config, proxyBaseURL string, prefix string, path string, rawQuery string) string {
	if cfg == nil {
		return ""
	}
	workerBase := strings.TrimSpace(cfg.ReverseProxyWorkerURL)
	if workerBase == "" {
		return ""
	}

	workerParsed, err := url.Parse(workerBase)
	if err != nil || workerParsed == nil || workerParsed.Host == "" {
		log.Warnf("invalid reverse-proxy-worker-url %q: %v", workerBase, err)
		return ""
	}

	upstreamParsed, err := url.Parse(strings.TrimSpace(proxyBaseURL))
	if err != nil || upstreamParsed == nil || upstreamParsed.Host == "" {
		log.Warnf("invalid reverse proxy base-url %q for worker routing: %v", proxyBaseURL, err)
		return ""
	}

	upstreamHost := strings.TrimSpace(upstreamParsed.Hostname())
	if upstreamHost == "" {
		return ""
	}

	// Avoid recursive worker->worker routing.
	if strings.EqualFold(strings.TrimSpace(workerParsed.Hostname()), upstreamHost) {
		return ""
	}

	normalizedPrefix := "/" + strings.Trim(strings.TrimSpace(prefix), "/")
	normalizedPath := path
	if normalizedPath == "" {
		normalizedPath = "/"
	}
	if !strings.HasPrefix(normalizedPath, "/") {
		normalizedPath = "/" + normalizedPath
	}

	basePath := normalizedPrefix + normalizedPath
	basePath = strings.TrimSuffix(basePath, "/")
	workerTarget := strings.TrimSuffix(workerBase, "/") + basePath + "/" + upstreamHost
	if rawQuery != "" {
		workerTarget += "?" + rawQuery
	}
	return workerTarget
}
