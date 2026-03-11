package executor

import (
	"context"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/registry"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
)

var (
	codexFreeOAuthBlockedModelPrefixes = [...]string{"gpt-5.3", "gpt-5.4"}
	codexTeamOAuthAllowedModelPrefixes = [...]string{"gpt-5.3", "gpt-5.4"}
)

// FetchCodexModels returns the static Codex model list.
// Codex model availability is hardcoded locally instead of being fetched from upstream.
func FetchCodexModels(ctx context.Context, auth *cliproxyauth.Auth, cfg *config.Config) []*registry.ModelInfo {
	_ = ctx
	_ = cfg
	return FilterCodexModelsForAuth(auth, registry.GetOpenAIModels())
}

// FilterCodexModelsForAuth removes models that should not be exposed for a given Codex auth.
func FilterCodexModelsForAuth(auth *cliproxyauth.Auth, models []*registry.ModelInfo) []*registry.ModelInfo {
	if len(models) == 0 {
		return models
	}

	accessLevel := codexOAuthAccessLevel(auth)
	if accessLevel == codexOAuthAccessDefault {
		return models
	}

	filtered := make([]*registry.ModelInfo, 0, len(models))
	for _, model := range models {
		if model == nil {
			continue
		}
		modelID := strings.ToLower(strings.TrimSpace(model.ID))
		if codexModelAllowedForAccessLevel(accessLevel, modelID) {
			filtered = append(filtered, model)
		}
	}
	return filtered
}

type codexOAuthAccess string

const (
	codexOAuthAccessDefault codexOAuthAccess = ""
	codexOAuthAccessFree    codexOAuthAccess = "free"
	codexOAuthAccessTeam    codexOAuthAccess = "team"
)

func codexModelAllowedForAccessLevel(accessLevel codexOAuthAccess, modelID string) bool {
	switch accessLevel {
	case codexOAuthAccessFree:
		for _, prefix := range codexFreeOAuthBlockedModelPrefixes {
			if strings.HasPrefix(modelID, prefix) {
				return false
			}
		}
		return true
	case codexOAuthAccessTeam:
		for _, prefix := range codexTeamOAuthAllowedModelPrefixes {
			if strings.HasPrefix(modelID, prefix) {
				return true
			}
		}
		return false
	default:
		return true
	}
}

func codexOAuthAccessLevel(auth *cliproxyauth.Auth) codexOAuthAccess {
	if auth == nil || !strings.EqualFold(strings.TrimSpace(auth.Provider), "codex") {
		return codexOAuthAccessDefault
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
		return codexOAuthAccessDefault
	}
	if authKind == string(codexOAuthAccessTeam) {
		return codexOAuthAccessTeam
	}
	if authKind == string(codexOAuthAccessFree) {
		return codexOAuthAccessFree
	}

	for _, candidate := range []string{auth.FileName, auth.ID} {
		if codexCredentialLooksTeam(candidate) {
			return codexOAuthAccessTeam
		}
		if codexCredentialLooksFree(candidate) {
			return codexOAuthAccessFree
		}
	}
	return codexOAuthAccessDefault
}

func codexCredentialLooksTeam(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return false
	}
	return strings.Contains(name, "-team.json") || strings.HasSuffix(name, "-team")
}

func codexCredentialLooksFree(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return false
	}
	return strings.Contains(name, "-free.json") || strings.HasSuffix(name, "-free")
}
