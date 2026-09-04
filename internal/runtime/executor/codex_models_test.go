package executor

import (
	"context"
	"encoding/base64"
	"fmt"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/registry"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
)

func TestFetchCodexModels_UsesStaticDefinitions(t *testing.T) {
	auth := &cliproxyauth.Auth{
		ID:       "codex-demo-plus.json",
		FileName: "codex-demo-plus.json",
		Provider: "codex",
	}
	models := FetchCodexModels(context.Background(), auth, nil)
	if len(models) == 0 {
		t.Fatalf("expected static codex models")
	}
	seen54 := false
	seen54Mini := false
	seen55 := false
	seen56Luna := false
	seen6Astra := false
	seenAutoReview := false
	seenImage2 := false
	for _, model := range models {
		if model == nil {
			continue
		}
		switch model.ID {
		case "gpt-5.4":
			seen54 = true
		case "gpt-5.4-mini":
			seen54Mini = true
		case "gpt-5.5":
			seen55 = true
		case "gpt-5.6-luna":
			seen56Luna = true
		case "gpt-6-astra":
			seen6Astra = true
		case "codex-auto-review":
			seenAutoReview = true
		case "gpt-image-2":
			seenImage2 = true
		}
	}
	if !seen54 {
		t.Fatalf("expected gpt-5.4 in static codex model list")
	}
	if !seen54Mini {
		t.Fatalf("expected gpt-5.4-mini in static codex model list")
	}
	if !seen55 {
		t.Fatalf("expected gpt-5.5 in static codex model list")
	}
	if !seen56Luna {
		t.Fatalf("expected gpt-5.6-luna in static codex model list")
	}
	if !seen6Astra {
		t.Fatalf("expected gpt-6-astra in static codex model list")
	}
	if !seenAutoReview {
		t.Fatalf("expected codex-auto-review in static codex model list")
	}
	if !seenImage2 {
		t.Fatalf("expected gpt-image-2 in static codex model list")
	}
}

func TestFilterCodexModelsForAuth_FreeOAuthRemovesOnlyUpstreamRestrictedModels(t *testing.T) {
	auth := &cliproxyauth.Auth{
		ID:       "codex-free@example.com-free.json",
		FileName: "codex-free@example.com-free.json",
		Provider: "codex",
	}
	models := []*registry.ModelInfo{
		{ID: "gpt-5.2-codex"},
		{ID: "gpt-5.3-codex"},
		{ID: "gpt-5.3-codex-spark"},
		{ID: "gpt-5.4"},
		{ID: "gpt-5.4-mini"},
		{ID: "gpt-5.5"},
		{ID: "gpt-5.6-sol"},
		{ID: "gpt-5.6-terra"},
		{ID: "gpt-5.6-luna"},
		{ID: "gpt-6-astra"},
		{ID: "codex-auto-review"},
		{ID: "gpt-image-2"},
	}

	filtered := FilterCodexModelsForAuth(auth, models)
	want := []string{"gpt-5.4-mini", "gpt-5.5", "gpt-5.6-terra", "gpt-5.6-luna", "codex-auto-review", "gpt-image-2"}
	if len(filtered) != len(want) {
		t.Fatalf("expected %d models after filtering, got %d", len(want), len(filtered))
	}
	for i, model := range filtered {
		if model == nil || model.ID != want[i] {
			t.Fatalf("unexpected free model at %d: got %#v want %q", i, model, want[i])
		}
	}
}

func TestFilterCodexModelsForAuth_PaidOAuthKeepsModels(t *testing.T) {
	auth := &cliproxyauth.Auth{
		ID:       "codex-plus@example.com-plus.json",
		FileName: "codex-plus@example.com-plus.json",
		Provider: "codex",
	}
	models := []*registry.ModelInfo{
		{ID: "gpt-5.2-codex"},
		{ID: "gpt-5.3-codex"},
		{ID: "gpt-5.4"},
		{ID: "gpt-5.5"},
		{ID: "gpt-image-2"},
	}

	filtered := FilterCodexModelsForAuth(auth, models)
	if len(filtered) != len(models) {
		t.Fatalf("expected paid OAuth auth to keep all models, got %d", len(filtered))
	}
}

func TestFilterCodexModelsForAuth_FreeOAuthFromIDTokenRemovesRestrictedModels(t *testing.T) {
	auth := &cliproxyauth.Auth{
		ID:       "codex-free@example.com.json",
		FileName: "codex-free@example.com.json",
		Provider: "codex",
		Metadata: map[string]any{
			"id_token": testCodexIDToken("free"),
		},
	}
	models := []*registry.ModelInfo{
		{ID: "gpt-5.2-codex"},
		{ID: "gpt-5.5"},
		{ID: "gpt-5.6-luna"},
		{ID: "gpt-image-2"},
	}

	filtered := FilterCodexModelsForAuth(auth, models)
	want := []string{"gpt-5.5", "gpt-5.6-luna", "gpt-image-2"}
	if len(filtered) != len(want) {
		t.Fatalf("expected free auth from id_token to keep %d models, got %d", len(want), len(filtered))
	}
	for i, model := range filtered {
		if model == nil || model.ID != want[i] {
			t.Fatalf("unexpected free id_token model at %d: got %#v want %q", i, model, want[i])
		}
	}
}

func TestFilterCodexModelsForAuth_PlusOAuthFromIDTokenKeepsModels(t *testing.T) {
	auth := &cliproxyauth.Auth{
		ID:       "codex-plus@example.com.json",
		FileName: "codex-plus@example.com.json",
		Provider: "codex",
		Metadata: map[string]any{
			"id_token": testCodexIDToken("plus"),
		},
	}
	models := []*registry.ModelInfo{
		{ID: "gpt-5.2-codex"},
		{ID: "gpt-5.5"},
		{ID: "gpt-image-2"},
	}

	filtered := FilterCodexModelsForAuth(auth, models)
	if len(filtered) != len(models) {
		t.Fatalf("expected plus auth from id_token to keep all models, got %d", len(filtered))
	}
}

func TestFilterCodexModelsForAuth_TeamOAuthKeepsOnlyUpstreamTeamModels(t *testing.T) {
	auth := &cliproxyauth.Auth{
		ID:       "codex-team@example.com-team.json",
		FileName: "codex-team@example.com-team.json",
		Provider: "codex",
	}
	models := []*registry.ModelInfo{
		{ID: "gpt-5.2-codex"},
		{ID: "gpt-5.3-codex"},
		{ID: "gpt-5.3-codex-spark"},
		{ID: "gpt-5.4"},
		{ID: "gpt-5.4-mini"},
		{ID: "gpt-5.5"},
		{ID: "gpt-5.6-sol"},
		{ID: "gpt-5.6-terra"},
		{ID: "gpt-5.6-luna"},
		{ID: "gpt-6-astra"},
		{ID: "codex-auto-review"},
		{ID: "gpt-image-2"},
		{ID: "gpt-4o"},
	}

	filtered := FilterCodexModelsForAuth(auth, models)
	if len(filtered) != 8 {
		t.Fatalf("expected team OAuth auth to keep only upstream team models, got %d", len(filtered))
	}
	want := []string{"gpt-5.4", "gpt-5.4-mini", "gpt-5.5", "gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna", "codex-auto-review", "gpt-image-2"}
	for i, model := range filtered {
		if model == nil || model.ID != want[i] {
			t.Fatalf("unexpected team model at %d: got %#v want %q", i, model, want[i])
		}
	}
}

func testCodexIDToken(planType string) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf(`{"https://api.openai.com/auth":{"chatgpt_plan_type":%q}}`, planType)))
	return header + "." + payload + ".signature"
}
