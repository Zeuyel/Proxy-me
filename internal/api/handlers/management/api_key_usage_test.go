package management

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
)

func TestGetAPIKeyUsageAggregatesRuntimeAPIKeyStats(t *testing.T) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	store := &memoryAuthStore{}
	manager := coreauth.NewManager(store, nil, nil)
	auth := &coreauth.Auth{
		ID:       "api-key-auth",
		Provider: "codex",
		Status:   coreauth.StatusActive,
		Attributes: map[string]string{
			"api_key":  "sk-test",
			"base_url": "https://example.test",
		},
	}
	if _, err := manager.Register(context.Background(), auth); err != nil {
		t.Fatalf("register auth: %v", err)
	}
	manager.MarkResult(context.Background(), coreauth.Result{AuthID: auth.ID, Provider: "codex", Model: "gpt-5", Success: true})
	manager.MarkResult(context.Background(), coreauth.Result{AuthID: auth.ID, Provider: "codex", Model: "gpt-5", Success: false, Error: &coreauth.Error{Message: "boom", HTTPStatus: http.StatusBadGateway}})

	h := &Handler{authManager: manager}
	r := gin.New()
	r.GET("/api-key-usage", h.GetAPIKeyUsage)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api-key-usage", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}

	var payload map[string]map[string]struct {
		Success        int64                          `json:"success"`
		Failed         int64                          `json:"failed"`
		RecentRequests []coreauth.RecentRequestBucket `json:"recent_requests"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	entry, ok := payload["codex"]["https://example.test|sk-test"]
	if !ok {
		t.Fatalf("expected codex api key usage, got %#v", payload)
	}
	if entry.Success != 1 || entry.Failed != 1 {
		t.Fatalf("usage = success %d failed %d", entry.Success, entry.Failed)
	}
	if len(entry.RecentRequests) != 20 {
		t.Fatalf("recent_requests len = %d", len(entry.RecentRequests))
	}
}
