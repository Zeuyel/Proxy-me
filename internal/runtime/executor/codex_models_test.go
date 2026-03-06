package executor

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
)

func TestBuildCodexModelEndpoints_V1BaseURL(t *testing.T) {
	endpoints := buildCodexModelEndpoints("https://api.openai.com/v1")
	if len(endpoints) == 0 {
		t.Fatalf("expected endpoints")
	}
	if endpoints[0] != "https://api.openai.com/v1/models" {
		t.Fatalf("expected first endpoint to be /v1/models, got %q", endpoints[0])
	}
}

func TestParseCodexModelPayload_DedupesAndNormalizes(t *testing.T) {
	payload := []byte(`{
		"data": [
			{"id": "gpt-5", "owned_by": "openai"},
			{"id": "gpt-5", "owned_by": "openai"},
			{"name": "gpt-5.1-codex-mini", "displayName": "GPT 5.1 Codex Mini"}
		]
	}`)
	models := parseCodexModelPayload(payload)
	if len(models) != 2 {
		t.Fatalf("expected 2 models after dedupe, got %d", len(models))
	}
	if models[0].ID != "gpt-5" {
		t.Fatalf("unexpected first model id: %q", models[0].ID)
	}
	if models[1].ID != "gpt-5.1-codex-mini" {
		t.Fatalf("unexpected second model id: %q", models[1].ID)
	}
	if models[1].DisplayName != "GPT 5.1 Codex Mini" {
		t.Fatalf("unexpected display name: %q", models[1].DisplayName)
	}
}

func TestFetchCodexModels_FallbackToModelsEndpoint(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"not found"}`))
		case "/models":
			if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
				t.Fatalf("expected bearer authorization header")
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"models":[{"id":"gpt-5.4-codex","owned_by":"openai"}]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()

	auth := &cliproxyauth.Auth{
		Provider: "codex",
		Attributes: map[string]string{
			"api_key":  "test-token",
			"base_url": ts.URL,
		},
	}
	models := FetchCodexModels(context.Background(), auth, nil)
	if len(models) != 1 {
		t.Fatalf("expected 1 model, got %d", len(models))
	}
	if models[0].ID != "gpt-5.4-codex" {
		t.Fatalf("unexpected model id: %q", models[0].ID)
	}
}
