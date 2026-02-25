package management

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	log "github.com/sirupsen/logrus"
)

// GetReverseProxies retrieves all reverse proxy configurations.
func (h *Handler) GetReverseProxies(c *gin.Context) {
	h.mu.Lock()
	defer h.mu.Unlock()

	proxies := h.cfg.ReverseProxies
	if proxies == nil {
		proxies = []config.ReverseProxy{}
	}

	c.JSON(http.StatusOK, gin.H{
		"reverse-proxies": proxies,
	})
}

// CreateReverseProxy creates a new reverse proxy configuration.
func (h *Handler) CreateReverseProxy(c *gin.Context) {
	var req config.ReverseProxy
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Validate required fields
	if req.Name == "" || req.BaseURL == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name and base-url are required"})
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	// Generate unique ID
	req.ID = uuid.New().String()
	req.CreatedAt = time.Now().Format(time.RFC3339)

	// Default to enabled if not specified
	if !req.Enabled {
		req.Enabled = true
	}

	// Add to configuration
	h.cfg.ReverseProxies = append(h.cfg.ReverseProxies, req)

	// Save configuration
	if err := config.SaveConfigPreserveComments(h.configFilePath, h.cfg); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to save config: %v", err)})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "reverse proxy created",
		"proxy":   req,
	})
}

// UpdateReverseProxy updates an existing reverse proxy configuration.
func (h *Handler) UpdateReverseProxy(c *gin.Context) {
	proxyID := c.Param("id")
	var req config.ReverseProxy
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	// Find and update
	found := false
	for i, proxy := range h.cfg.ReverseProxies {
		if proxy.ID == proxyID {
			// Preserve original ID and creation time
			req.ID = proxy.ID
			req.CreatedAt = proxy.CreatedAt
			h.cfg.ReverseProxies[i] = req
			found = true
			break
		}
	}

	if !found {
		c.JSON(http.StatusNotFound, gin.H{"error": "proxy not found"})
		return
	}

	// Save configuration
	if err := config.SaveConfigPreserveComments(h.configFilePath, h.cfg); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to save config: %v", err)})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "reverse proxy updated",
		"proxy":   req,
	})
}

// DeleteReverseProxy deletes a reverse proxy configuration.
func (h *Handler) DeleteReverseProxy(c *gin.Context) {
	proxyID := c.Param("id")

	h.mu.Lock()
	defer h.mu.Unlock()

	// Find and delete
	found := false
	for i, proxy := range h.cfg.ReverseProxies {
		if proxy.ID == proxyID {
			h.cfg.ReverseProxies = append(h.cfg.ReverseProxies[:i], h.cfg.ReverseProxies[i+1:]...)
			found = true
			break
		}
	}

	if !found {
		c.JSON(http.StatusNotFound, gin.H{"error": "proxy not found"})
		return
	}

	// Cleanup auth-level routing entries pointing to the deleted proxy
	if len(h.cfg.ProxyRoutingAuth) > 0 {
		for key, value := range h.cfg.ProxyRoutingAuth {
			if value == proxyID {
				delete(h.cfg.ProxyRoutingAuth, key)
			}
		}
	}

	// Cleanup provider-level routing entries pointing to the deleted proxy
	if h.cfg.ProxyRouting.Codex == proxyID {
		h.cfg.ProxyRouting.Codex = ""
	}
	if h.cfg.ProxyRouting.Antigravity == proxyID {
		h.cfg.ProxyRouting.Antigravity = ""
	}
	if h.cfg.ProxyRouting.Claude == proxyID {
		h.cfg.ProxyRouting.Claude = ""
	}
	if h.cfg.ProxyRouting.Gemini == proxyID {
		h.cfg.ProxyRouting.Gemini = ""
	}
	if h.cfg.ProxyRouting.GeminiCLI == proxyID {
		h.cfg.ProxyRouting.GeminiCLI = ""
	}
	if h.cfg.ProxyRouting.Vertex == proxyID {
		h.cfg.ProxyRouting.Vertex = ""
	}
	if h.cfg.ProxyRouting.AIStudio == proxyID {
		h.cfg.ProxyRouting.AIStudio = ""
	}
	if h.cfg.ProxyRouting.Qwen == proxyID {
		h.cfg.ProxyRouting.Qwen = ""
	}
	if h.cfg.ProxyRouting.IFlow == proxyID {
		h.cfg.ProxyRouting.IFlow = ""
	}

	// Save configuration
	if err := config.SaveConfigPreserveComments(h.configFilePath, h.cfg); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to save config: %v", err)})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "reverse proxy deleted"})
}

// GetReverseProxyWorkerURL retrieves the global reverse proxy worker URL.
func (h *Handler) GetReverseProxyWorkerURL(c *gin.Context) {
	h.mu.Lock()
	defer h.mu.Unlock()

	c.JSON(http.StatusOK, gin.H{
		"reverse-proxy-worker-url": h.cfg.ReverseProxyWorkerURL,
	})
}

// PutReverseProxyWorkerURL updates the global reverse proxy worker URL.
func (h *Handler) PutReverseProxyWorkerURL(c *gin.Context) {
	var body struct {
		Value *string `json:"value"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || body.Value == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}

	value := strings.TrimSpace(*body.Value)
	if value != "" {
		parsed, err := url.Parse(value)
		if err != nil || parsed == nil || parsed.Host == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid reverse-proxy-worker-url"})
			return
		}
		if parsed.Scheme != "https" && parsed.Scheme != "http" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "reverse-proxy-worker-url must use http or https"})
			return
		}
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	h.cfg.ReverseProxyWorkerURL = value
	if err := config.SaveConfigPreserveComments(h.configFilePath, h.cfg); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to save config: %v", err)})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message":                  "reverse proxy worker url updated",
		"reverse-proxy-worker-url": value,
	})
}

// DeleteReverseProxyWorkerURL clears the global reverse proxy worker URL.
func (h *Handler) DeleteReverseProxyWorkerURL(c *gin.Context) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.cfg.ReverseProxyWorkerURL = ""
	if err := config.SaveConfigPreserveComments(h.configFilePath, h.cfg); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to save config: %v", err)})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message":                  "reverse proxy worker url deleted",
		"reverse-proxy-worker-url": "",
	})
}

// GetProxyRouting retrieves the proxy routing configuration.
func (h *Handler) GetProxyRouting(c *gin.Context) {
	h.mu.Lock()
	defer h.mu.Unlock()

	c.JSON(http.StatusOK, gin.H{
		"proxy-routing": h.cfg.ProxyRouting,
	})
}

// GetProxyRoutingAuth retrieves the auth-level proxy routing configuration.
func (h *Handler) GetProxyRoutingAuth(c *gin.Context) {
	h.mu.Lock()
	defer h.mu.Unlock()

	routing := h.cfg.ProxyRoutingAuth
	if routing == nil {
		routing = map[string]string{}
	}
	if clean, changed := h.sanitizeProxyRoutingAuthLocked(); changed {
		routing = clean
		if err := config.SaveConfigPreserveComments(h.configFilePath, h.cfg); err != nil {
			log.WithError(err).Warn("failed to persist cleaned proxy-routing-auth")
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"proxy-routing-auth": routing,
	})
}

// UpdateProxyRouting updates the proxy routing configuration.
func (h *Handler) UpdateProxyRouting(c *gin.Context) {
	var req config.ProxyRouting
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	h.cfg.ProxyRouting = req

	// Save configuration
	if err := config.SaveConfigPreserveComments(h.configFilePath, h.cfg); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to save config: %v", err)})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message":       "proxy routing updated",
		"proxy-routing": req,
	})
}

// UpdateProxyRoutingAuth updates the auth-level proxy routing configuration.
func (h *Handler) UpdateProxyRoutingAuth(c *gin.Context) {
	var req map[string]string
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	clean := make(map[string]string, len(req))
	for key, value := range req {
		trimKey := strings.TrimSpace(key)
		trimValue := strings.TrimSpace(value)
		if trimKey == "" || trimValue == "" {
			continue
		}
		clean[trimKey] = trimValue
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	h.cfg.ProxyRoutingAuth = clean

	// Save configuration
	if err := config.SaveConfigPreserveComments(h.configFilePath, h.cfg); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to save config: %v", err)})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message":            "proxy routing auth updated",
		"proxy-routing-auth": clean,
	})
}

func (h *Handler) sanitizeProxyRoutingAuthLocked() (map[string]string, bool) {
	if h == nil || h.cfg == nil {
		return map[string]string{}, false
	}
	if len(h.cfg.ProxyRoutingAuth) == 0 {
		return map[string]string{}, false
	}

	known := map[string]struct{}{}
	if h.authManager != nil {
		for _, auth := range h.authManager.List() {
			if auth == nil {
				continue
			}
			// Keep only auth entries that are still valid for routing.
			// Deleted auth files can remain in authManager memory as "source=memory"
			// records even when backing file is gone; those should not keep stale
			// proxy-routing-auth mappings alive.
			entry := h.buildAuthFileEntry(auth)
			if entry == nil {
				continue
			}
			source := strings.TrimSpace(fmt.Sprint(entry["source"]))
			runtimeOnly, _ := entry["runtime_only"].(bool)
			if strings.EqualFold(source, "memory") && !runtimeOnly {
				continue
			}
			if v := strings.TrimSpace(auth.ID); v != "" {
				known[v] = struct{}{}
				if base := strings.TrimSpace(filepath.Base(v)); base != "" && base != "." {
					known[base] = struct{}{}
				}
			}
			if v := strings.TrimSpace(auth.EnsureIndex()); v != "" {
				known[v] = struct{}{}
			}
			if v := strings.TrimSpace(auth.FileName); v != "" {
				known[v] = struct{}{}
			}
			if path := strings.TrimSpace(authAttribute(auth, "path")); path != "" {
				known[path] = struct{}{}
				if base := strings.TrimSpace(filepath.Base(path)); base != "" && base != "." {
					known[base] = struct{}{}
				}
			}
		}
	}

	authDirScanned := false
	authDirReadErr := false

	// If auth list is currently unavailable, fallback to auth-dir files.
	if len(known) == 0 {
		if h.cfg != nil {
			authDir := strings.TrimSpace(h.cfg.AuthDir)
			if authDir != "" {
				authDirScanned = true
				if entries, err := os.ReadDir(authDir); err == nil {
					for _, entry := range entries {
						if entry == nil || entry.IsDir() {
							continue
						}
						name := strings.TrimSpace(entry.Name())
						if name == "" {
							continue
						}
						known[name] = struct{}{}
						if full := strings.TrimSpace(filepath.Join(authDir, name)); full != "" {
							known[full] = struct{}{}
						}
					}
				} else {
					authDirReadErr = true
				}
			}
		}
	}

	// If still no known refs:
	// - when auth-dir was scanned successfully and found no files, clear stale mapping keys.
	// - otherwise keep existing mappings to avoid accidental destructive cleanup.
	if len(known) == 0 {
		if authDirScanned && !authDirReadErr {
			if len(h.cfg.ProxyRoutingAuth) > 0 {
				h.cfg.ProxyRoutingAuth = nil
				return map[string]string{}, true
			}
			return map[string]string{}, false
		}
		copyMap := make(map[string]string, len(h.cfg.ProxyRoutingAuth))
		for key, value := range h.cfg.ProxyRoutingAuth {
			trimKey := strings.TrimSpace(key)
			trimValue := strings.TrimSpace(value)
			if trimKey == "" || trimValue == "" {
				continue
			}
			copyMap[trimKey] = trimValue
		}
		if len(copyMap) != len(h.cfg.ProxyRoutingAuth) {
			h.cfg.ProxyRoutingAuth = copyMap
			return copyMap, true
		}
		return copyMap, false
	}

	clean := make(map[string]string, len(h.cfg.ProxyRoutingAuth))
	changed := false
	for key, value := range h.cfg.ProxyRoutingAuth {
		trimKey := strings.TrimSpace(key)
		trimValue := strings.TrimSpace(value)
		if trimKey == "" || trimValue == "" {
			changed = true
			continue
		}
		if _, exists := known[trimKey]; !exists {
			changed = true
			continue
		}
		clean[trimKey] = trimValue
	}
	if !changed && len(clean) != len(h.cfg.ProxyRoutingAuth) {
		changed = true
	}
	if changed {
		h.cfg.ProxyRoutingAuth = clean
	}
	return clean, changed
}
