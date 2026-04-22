package management

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/watcher/synthesizer"
	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
)

func registerSynthesizedConfigAuths(t *testing.T, manager *coreauth.Manager, cfg *config.Config) []*coreauth.Auth {
	t.Helper()

	auths, err := synthesizer.NewConfigSynthesizer().Synthesize(&synthesizer.SynthesisContext{
		Config:      cfg,
		Now:         time.Now(),
		IDGenerator: synthesizer.NewStableIDGenerator(),
	})
	if err != nil {
		t.Fatalf("synthesize config auths: %v", err)
	}
	for _, auth := range auths {
		if auth == nil {
			continue
		}
		if _, errRegister := manager.Register(context.Background(), auth); errRegister != nil {
			t.Fatalf("register auth %q: %v", auth.ID, errRegister)
		}
	}
	return auths
}

func TestGetGeminiKeys_ExposesAuthIndexForLiveConfigAuth(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{
		GeminiKey: []config.GeminiKey{
			{APIKey: "gemini-key-1", BaseURL: "https://example.com"},
		},
	}
	manager := coreauth.NewManager(&memoryAuthStore{}, nil, nil)
	auths := registerSynthesizedConfigAuths(t, manager, cfg)
	if len(auths) != 1 || auths[0] == nil {
		t.Fatalf("expected one synthesized auth, got %#v", auths)
	}

	h := &Handler{cfg: cfg, authManager: manager}
	r := gin.New()
	r.GET("/gemini", h.GetGeminiKeys)

	req := httptest.NewRequest(http.MethodGet, "/gemini", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	var payload struct {
		Items []struct {
			APIKey    string `json:"api-key"`
			AuthIndex string `json:"auth-index"`
		} `json:"gemini-api-key"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(payload.Items) != 1 {
		t.Fatalf("expected one gemini item, got %d", len(payload.Items))
	}
	if payload.Items[0].AuthIndex != auths[0].EnsureIndex() {
		t.Fatalf("auth-index = %q, want %q", payload.Items[0].AuthIndex, auths[0].EnsureIndex())
	}
}

func TestGetOpenAICompat_ExposesEntryAuthIndexesForLiveConfigAuths(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{
		OpenAICompatibility: []config.OpenAICompatibility{
			{
				Name:    "compat-a",
				BaseURL: "https://compat.example.com/v1",
				APIKeyEntries: []config.OpenAICompatibilityAPIKey{
					{APIKey: "compat-key-1"},
					{APIKey: "compat-key-2"},
				},
			},
		},
	}
	manager := coreauth.NewManager(&memoryAuthStore{}, nil, nil)
	auths := registerSynthesizedConfigAuths(t, manager, cfg)
	if len(auths) != 2 {
		t.Fatalf("expected two synthesized auths, got %d", len(auths))
	}

	h := &Handler{cfg: cfg, authManager: manager}
	r := gin.New()
	r.GET("/openai-compat", h.GetOpenAICompat)

	req := httptest.NewRequest(http.MethodGet, "/openai-compat", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	var payload struct {
		Items []struct {
			APIKeyEntries []struct {
				APIKey    string `json:"api-key"`
				AuthIndex string `json:"auth-index"`
			} `json:"api-key-entries"`
		} `json:"openai-compatibility"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(payload.Items) != 1 || len(payload.Items[0].APIKeyEntries) != 2 {
		t.Fatalf("unexpected response payload: %s", w.Body.String())
	}

	for i := range payload.Items[0].APIKeyEntries {
		if payload.Items[0].APIKeyEntries[i].AuthIndex != auths[i].EnsureIndex() {
			t.Fatalf("entry %d auth-index = %q, want %q", i, payload.Items[0].APIKeyEntries[i].AuthIndex, auths[i].EnsureIndex())
		}
	}
}
