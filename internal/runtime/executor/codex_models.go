package executor

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/registry"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	log "github.com/sirupsen/logrus"
	"github.com/tidwall/gjson"
)

const defaultCodexBaseURL = "https://chatgpt.com/backend-api/codex"

// FetchCodexModels retrieves models for a codex credential from upstream /v1/models (or compatible fallback endpoints).
func FetchCodexModels(ctx context.Context, auth *cliproxyauth.Auth, cfg *config.Config) []*registry.ModelInfo {
	token, baseURL := codexCreds(auth)
	if strings.TrimSpace(token) == "" {
		return nil
	}

	if ctx == nil {
		ctx = context.Background()
	}
	httpClient := newProxyAwareHTTPClient(ctx, cfg, auth, 15*time.Second)
	endpoints := buildCodexModelEndpoints(baseURL)

	for _, endpoint := range endpoints {
		httpReq, errReq := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if errReq != nil {
			continue
		}
		applyCodexHeaders(httpReq, auth, token, false)

		httpResp, errDo := httpClient.Do(httpReq)
		if errDo != nil {
			continue
		}
		bodyBytes, errRead := io.ReadAll(httpResp.Body)
		if errClose := httpResp.Body.Close(); errClose != nil {
			log.Debugf("codex model fetch: failed to close response body: %v", errClose)
		}
		if errRead != nil {
			continue
		}
		if httpResp.StatusCode < http.StatusOK || httpResp.StatusCode >= http.StatusMultipleChoices {
			continue
		}

		if models := parseCodexModelPayload(bodyBytes); len(models) > 0 {
			return models
		}
	}

	return nil
}

func buildCodexModelEndpoints(baseURL string) []string {
	trimmed := strings.TrimSpace(baseURL)
	if trimmed == "" {
		trimmed = defaultCodexBaseURL
	}
	trimmed = strings.TrimRight(trimmed, "/")

	endpoints := make([]string, 0, 6)
	seen := make(map[string]struct{}, 6)
	add := func(v string) {
		v = strings.TrimSpace(v)
		if v == "" {
			return
		}
		if _, ok := seen[v]; ok {
			return
		}
		seen[v] = struct{}{}
		endpoints = append(endpoints, v)
	}

	lower := strings.ToLower(trimmed)
	if strings.HasSuffix(lower, "/v1") {
		add(trimmed + "/models")
	} else {
		add(trimmed + "/v1/models")
		add(trimmed + "/models")
	}

	if parsed, errParse := url.Parse(trimmed); errParse == nil && parsed.Scheme != "" && parsed.Host != "" {
		root := parsed.Scheme + "://" + parsed.Host
		add(root + "/v1/models")
		if strings.Contains(strings.ToLower(parsed.Path), "/backend-api/codex") {
			add(root + "/backend-api/codex/v1/models")
			add(root + "/backend-api/codex/models")
		}
	}

	return endpoints
}

func parseCodexModelPayload(body []byte) []*registry.ModelInfo {
	if len(body) == 0 {
		return nil
	}

	root := gjson.ParseBytes(body)
	var list gjson.Result
	switch {
	case root.Get("data").IsArray():
		list = root.Get("data")
	case root.Get("models").IsArray():
		list = root.Get("models")
	case root.Get("items").IsArray():
		list = root.Get("items")
	case root.IsArray():
		list = root
	default:
		return nil
	}

	now := time.Now().Unix()
	seen := make(map[string]struct{}, len(list.Array()))
	out := make([]*registry.ModelInfo, 0, len(list.Array()))
	for _, item := range list.Array() {
		modelID := strings.TrimSpace(item.Get("id").String())
		if modelID == "" {
			modelID = strings.TrimSpace(item.Get("name").String())
		}
		if modelID == "" {
			continue
		}
		key := strings.ToLower(modelID)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}

		ownedBy := strings.TrimSpace(item.Get("owned_by").String())
		if ownedBy == "" {
			ownedBy = strings.TrimSpace(item.Get("ownedBy").String())
		}
		if ownedBy == "" {
			ownedBy = "openai"
		}

		displayName := strings.TrimSpace(item.Get("display_name").String())
		if displayName == "" {
			displayName = strings.TrimSpace(item.Get("displayName").String())
		}
		if displayName == "" {
			displayName = modelID
		}

		created := item.Get("created").Int()
		if created <= 0 {
			created = now
		}

		out = append(out, &registry.ModelInfo{
			ID:          modelID,
			Object:      "model",
			Created:     created,
			OwnedBy:     ownedBy,
			Type:        "openai",
			DisplayName: displayName,
		})
	}

	return out
}
