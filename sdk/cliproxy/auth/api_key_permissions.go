package auth

import (
	"fmt"
	"strings"

	internalconfig "github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/executor"
)

func clientAPIKeyFromOptions(opts cliproxyexecutor.Options) string {
	if len(opts.Metadata) == 0 {
		return ""
	}
	raw, ok := opts.Metadata[cliproxyexecutor.ClientAPIKeyMetadataKey]
	if !ok || raw == nil {
		return ""
	}
	switch v := raw.(type) {
	case string:
		return strings.TrimSpace(v)
	case []byte:
		return strings.TrimSpace(string(v))
	case fmt.Stringer:
		return strings.TrimSpace(v.String())
	default:
		return strings.TrimSpace(fmt.Sprint(v))
	}
}

func (m *Manager) allowedAuthRefsForClientKey(clientKey string) (map[string]struct{}, bool) {
	if m == nil {
		return nil, false
	}
	clientKey = strings.TrimSpace(clientKey)
	if clientKey == "" {
		return nil, false
	}
	cfg, _ := m.runtimeConfig.Load().(*internalconfig.Config)
	if cfg == nil || len(cfg.APIKeyAuth) == 0 {
		return nil, false
	}
	refs, ok := cfg.APIKeyAuth[clientKey]
	if !ok {
		return nil, false
	}
	allowed := make(map[string]struct{}, len(refs))
	for _, ref := range refs {
		ref = strings.TrimSpace(ref)
		if ref == "" {
			continue
		}
		allowed[ref] = struct{}{}
	}
	return allowed, true
}

// AllowedAuthIDsForClientKey resolves the auth IDs permitted for a client API key.
// When the client key is not listed in api-key-auth, restricted is false.
// When restricted is true but the returned map is empty, the client has no allowed accounts.
func (m *Manager) AllowedAuthIDsForClientKey(clientKey string) (allowed map[string]struct{}, restricted bool) {
	allowedRefs, restricted := m.allowedAuthRefsForClientKey(clientKey)
	if !restricted {
		return nil, false
	}
	if len(allowedRefs) == 0 {
		return map[string]struct{}{}, true
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make(map[string]struct{})
	for _, auth := range m.auths {
		if authMatchesAllowedRefs(auth, allowedRefs) {
			out[auth.ID] = struct{}{}
		}
	}
	return out, true
}

func authMatchesAllowedRefs(auth *Auth, allowed map[string]struct{}) bool {
	if auth == nil || len(allowed) == 0 {
		return false
	}
	if id := strings.TrimSpace(auth.ID); id != "" {
		if _, ok := allowed[id]; ok {
			return true
		}
	}
	if idx := authIndexForMatch(auth); idx != "" {
		if _, ok := allowed[idx]; ok {
			return true
		}
	}
	if name := strings.TrimSpace(auth.FileName); name != "" {
		if _, ok := allowed[name]; ok {
			return true
		}
	}
	return false
}

func authIndexForMatch(auth *Auth) string {
	if auth == nil {
		return ""
	}
	if idx := strings.TrimSpace(auth.Index); idx != "" {
		return idx
	}
	seed := strings.TrimSpace(auth.FileName)
	if seed != "" {
		seed = "file:" + seed
	} else if auth.Attributes != nil {
		if apiKey := strings.TrimSpace(auth.Attributes["api_key"]); apiKey != "" {
			seed = "api_key:" + apiKey
		}
	}
	if seed == "" {
		if id := strings.TrimSpace(auth.ID); id != "" {
			seed = "id:" + id
		} else {
			return ""
		}
	}
	return stableAuthIndex(seed)
}
