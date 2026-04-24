package executor

import (
	"context"
	"strings"

	codexauth "github.com/router-for-me/CLIProxyAPI/v6/internal/auth/codex"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/registry"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
)

var (
	codexFreeOAuthBlockedModelIDs = map[string]struct{}{
		"gpt-5.3-codex":       {},
		"gpt-5.3-codex-spark": {},
		"gpt-5.4":             {},
		"gpt-5.5":             {},
	}
	codexTeamOAuthAllowedModelIDs = map[string]struct{}{
		"gpt-5.3-codex": {},
		"gpt-5.4":       {},
		"gpt-5.5":       {},
		"gpt-image-2":   {},
	}
)

// FetchCodexModels returns the static Codex model list.
// Codex model availability is hardcoded locally instead of being fetched from upstream.
func FetchCodexModels(ctx context.Context, auth *cliproxyauth.Auth, cfg *config.Config) []*registry.ModelInfo {
	_ = ctx
	_ = cfg
	return FilterCodexModelsForAuth(auth, registry.WithCodexBuiltins(registry.GetOpenAIModels()))
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
	codexOAuthAccessPaid    codexOAuthAccess = "paid"
	codexOAuthAccessTeam    codexOAuthAccess = "team"
)

func codexModelAllowedForAccessLevel(accessLevel codexOAuthAccess, modelID string) bool {
	switch accessLevel {
	case codexOAuthAccessFree:
		_, blocked := codexFreeOAuthBlockedModelIDs[modelID]
		return !blocked
	case codexOAuthAccessPaid:
		return true
	case codexOAuthAccessTeam:
		_, allowed := codexTeamOAuthAllowedModelIDs[modelID]
		return allowed
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
	if accessLevel, ok := codexOAuthAccessLevelFromValue(authKind); ok {
		return accessLevel
	}
	if planType := codexPlanType(auth); planType != "" {
		if accessLevel, ok := codexOAuthAccessLevelFromValue(planType); ok {
			return accessLevel
		}
	}

	for _, candidate := range []string{auth.FileName, auth.ID} {
		if codexCredentialLooksTeam(candidate) {
			return codexOAuthAccessTeam
		}
		if codexCredentialLooksPaid(candidate) {
			return codexOAuthAccessPaid
		}
		if codexCredentialLooksFree(candidate) {
			return codexOAuthAccessFree
		}
	}
	return codexOAuthAccessDefault
}

func codexOAuthAccessLevelFromValue(value string) (codexOAuthAccess, bool) {
	switch normalizeCodexAccessValue(value) {
	case "free":
		return codexOAuthAccessFree, true
	case "plus", "pro", "business", "enterprise", "edu", "education", "paid":
		return codexOAuthAccessPaid, true
	case "team":
		return codexOAuthAccessTeam, true
	default:
		return codexOAuthAccessDefault, false
	}
}

func normalizeCodexAccessValue(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return ""
	}
	replacer := strings.NewReplacer("_", "-", " ", "-", "/", "-")
	value = replacer.Replace(value)
	for strings.Contains(value, "--") {
		value = strings.ReplaceAll(value, "--", "-")
	}
	return strings.Trim(value, "-")
}

func codexPlanType(auth *cliproxyauth.Auth) string {
	if auth == nil {
		return ""
	}
	if auth.Attributes != nil {
		if planType := normalizeCodexAccessValue(auth.Attributes["plan_type"]); planType != "" {
			return planType
		}
	}
	if auth.Metadata != nil {
		if planType, ok := auth.Metadata["plan_type"].(string); ok {
			if normalized := normalizeCodexAccessValue(planType); normalized != "" {
				return normalized
			}
		}
		for _, key := range []string{"id_token", "access_token", "token"} {
			raw, ok := auth.Metadata[key].(string)
			if !ok || strings.TrimSpace(raw) == "" {
				continue
			}
			claims, err := codexauth.ParseJWTToken(strings.TrimSpace(raw))
			if err != nil || claims == nil {
				continue
			}
			if planType := normalizeCodexAccessValue(claims.CodexAuthInfo.ChatgptPlanType); planType != "" {
				return planType
			}
		}
	}
	return ""
}

func codexCredentialLooksTeam(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return false
	}
	return strings.Contains(name, "-team.json") || strings.HasSuffix(name, "-team")
}

func codexCredentialLooksPaid(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return false
	}
	for _, marker := range []string{"-plus", "-pro", "-business", "-enterprise", "-edu", "-education", "-paid"} {
		if strings.Contains(name, marker+".json") || strings.HasSuffix(name, marker) {
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
