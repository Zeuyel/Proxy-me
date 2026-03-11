package executor

import (
	"context"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/registry"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
)

var codexFreeOAuthBlockedModelPrefixes = [...]string{"gpt-5.3", "gpt-5.4"}

// FetchCodexModels returns the static Codex model list.
// Codex model availability is hardcoded locally instead of being fetched from upstream.
func FetchCodexModels(ctx context.Context, auth *cliproxyauth.Auth, cfg *config.Config) []*registry.ModelInfo {
	_ = ctx
	_ = cfg
	return FilterCodexModelsForAuth(auth, registry.GetOpenAIModels())
}

// FilterCodexModelsForAuth removes models that should not be exposed for a given Codex auth.
func FilterCodexModelsForAuth(auth *cliproxyauth.Auth, models []*registry.ModelInfo) []*registry.ModelInfo {
	if len(models) == 0 || !isCodexOAuthFreeAuth(auth) {
		return models
	}

	filtered := make([]*registry.ModelInfo, 0, len(models))
	for _, model := range models {
		if model == nil {
			continue
		}
		modelID := strings.ToLower(strings.TrimSpace(model.ID))
		blocked := false
		for _, prefix := range codexFreeOAuthBlockedModelPrefixes {
			if strings.HasPrefix(modelID, prefix) {
				blocked = true
				break
			}
		}
		if !blocked {
			filtered = append(filtered, model)
		}
	}
	return filtered
}

func isCodexOAuthFreeAuth(auth *cliproxyauth.Auth) bool {
	if auth == nil || !strings.EqualFold(strings.TrimSpace(auth.Provider), "codex") {
		return false
	}

	authKind := ""
	if auth.Attributes != nil {
		authKind = strings.ToLower(strings.TrimSpace(auth.Attributes["auth_kind"]))
	}
	if authKind == "" {
		if kind, _ := auth.AccountInfo(); kind != "" {
			authKind = strings.ToLower(strings.TrimSpace(kind))
		}
	}
	if authKind == "api_key" || authKind == "apikey" {
		return false
	}

	for _, candidate := range []string{auth.FileName, auth.ID} {
		if codexCredentialLooksFree(candidate) {
			return true
		}
	}
	return false
}

func codexCredentialLooksFree(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return false
	}
	return strings.Contains(name, "-free.json") || strings.HasSuffix(name, "-free")
}
