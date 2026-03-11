package cliproxy

import (
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/registry"
	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	"github.com/router-for-me/CLIProxyAPI/v6/sdk/config"
)

func TestRegisterModelsForAuth_CodexFreeFiltersStaticFallback(t *testing.T) {
	svc := &Service{
		cfg: &config.Config{},
	}
	auth := &coreauth.Auth{
		ID:       "codex-free-static-free.json",
		FileName: "codex-free-static-free.json",
		Provider: "codex",
		Metadata: map[string]any{"email": "free@example.com"},
	}

	reg := registry.GetGlobalRegistry()
	defer reg.UnregisterClient(auth.ID)

	svc.registerModelsForAuth(auth)

	models := reg.GetModelsForClient(auth.ID)
	if len(models) == 0 {
		t.Fatalf("expected static fallback models to be registered")
	}

	seen := make(map[string]struct{}, len(models))
	for _, model := range models {
		if model != nil {
			seen[model.ID] = struct{}{}
		}
	}
	if _, ok := seen["gpt-5.2-codex"]; !ok {
		t.Fatalf("expected gpt-5.2-codex to remain available")
	}
	for _, blocked := range []string{"gpt-5.3-codex", "gpt-5.3-codex-spark", "gpt-5.4"} {
		if _, ok := seen[blocked]; ok {
			t.Fatalf("expected %s to be filtered for free codex oauth auth", blocked)
		}
	}
}
