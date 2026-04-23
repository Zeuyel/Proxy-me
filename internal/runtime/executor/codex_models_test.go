package executor

import (
	"context"
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
	seen55 := false
	seenImage2 := false
	for _, model := range models {
		if model == nil {
			continue
		}
		switch model.ID {
		case "gpt-5.4":
			seen54 = true
		case "gpt-5.5":
			seen55 = true
		case "gpt-image-2":
			seenImage2 = true
		}
	}
	if !seen54 {
		t.Fatalf("expected gpt-5.4 in static codex model list")
	}
	if !seen55 {
		t.Fatalf("expected gpt-5.5 in static codex model list")
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
		{ID: "gpt-5.5"},
		{ID: "gpt-image-2"},
	}

	filtered := FilterCodexModelsForAuth(auth, models)
	if len(filtered) != 2 {
		t.Fatalf("expected 2 models after filtering, got %d", len(filtered))
	}
	if filtered[0].ID != "gpt-5.2-codex" {
		t.Fatalf("expected gpt-5.2-codex to remain, got %q", filtered[0].ID)
	}
	if filtered[1].ID != "gpt-image-2" {
		t.Fatalf("expected gpt-image-2 to remain, got %q", filtered[1].ID)
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
		{ID: "gpt-5.5"},
		{ID: "gpt-image-2"},
		{ID: "gpt-4o"},
	}

	filtered := FilterCodexModelsForAuth(auth, models)
	if len(filtered) != 4 {
		t.Fatalf("expected team OAuth auth to keep only upstream team models, got %d", len(filtered))
	}
	want := []string{"gpt-5.3-codex", "gpt-5.4", "gpt-5.5", "gpt-image-2"}
	for i, model := range filtered {
		if model == nil || model.ID != want[i] {
			t.Fatalf("unexpected team model at %d: got %#v want %q", i, model, want[i])
		}
	}
}
