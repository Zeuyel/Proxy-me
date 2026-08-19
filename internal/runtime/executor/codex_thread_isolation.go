package executor

import (
	"bytes"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/executor"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const codexThreadIdentityTTL = 24 * time.Hour

type codexThreadIsolationState struct {
	enabled                 bool
	rejectProviderThread    bool
	authScope               string
	model                   string
	clientPromptCacheKey    string
	canonicalPromptCacheKey string
}

type codexResponseBinding struct {
	rawID     string
	authScope string
	expires   time.Time
}

var codexResponseBindings = struct {
	sync.RWMutex
	byID map[string]codexResponseBinding
}{byID: make(map[string]codexResponseBinding)}

func newCodexThreadIsolationState(auth *cliproxyauth.Auth, model, clientPromptCacheKey string, opts cliproxyexecutor.Options) codexThreadIsolationState {
	clientPromptCacheKey = strings.TrimSpace(clientPromptCacheKey)
	scope := codexAuthScope(auth, model, codexClientTenant(opts))
	if scope == "" || clientPromptCacheKey == "" {
		return codexThreadIsolationState{}
	}
	return codexThreadIsolationState{
		enabled:                 true,
		authScope:               scope,
		model:                   strings.TrimSpace(model),
		clientPromptCacheKey:    clientPromptCacheKey,
		canonicalPromptCacheKey: codexCanonicalThreadID(scope, clientPromptCacheKey),
	}
}

func codexAuthScope(auth *cliproxyauth.Auth, model, clientTenant string) string {
	if auth == nil {
		return ""
	}
	authID := strings.TrimSpace(auth.ID)
	authIndex := strings.TrimSpace(auth.EnsureIndex())
	accountID := strings.TrimSpace(resolveCodexAccountID(auth))
	fileName := strings.TrimSpace(auth.FileName)
	if authID == "" && authIndex == "" && accountID == "" && fileName == "" {
		return ""
	}
	provider := strings.ToLower(strings.TrimSpace(auth.Provider))
	model = strings.TrimSpace(model)
	clientTenant = strings.TrimSpace(clientTenant)
	if clientTenant != "" {
		clientTenant = uuid.NewSHA1(uuid.NameSpaceOID, []byte("cli-proxy-api:codex-tenant:\x00"+clientTenant)).String()
	}
	return strings.Join([]string{
		"provider=" + provider,
		"model=" + model,
		"tenant=" + accountID,
		"client=" + clientTenant,
		"auth_id=" + authID,
		"auth_index=" + authIndex,
		"file=" + fileName,
	}, "\x00")
}

func codexClientTenant(opts cliproxyexecutor.Options) string {
	if opts.Metadata == nil {
		return ""
	}
	if raw, ok := opts.Metadata[cliproxyexecutor.ClientAPIKeyMetadataKey]; ok {
		switch value := raw.(type) {
		case string:
			return strings.TrimSpace(value)
		case []byte:
			return strings.TrimSpace(string(value))
		}
	}
	return ""
}

func codexCanonicalThreadID(scope, clientPromptCacheKey string) string {
	return "codex_thread_" + uuid.NewSHA1(uuid.NameSpaceOID, []byte("cli-proxy-api:codex-thread:\x00"+scope+"\x00"+strings.TrimSpace(clientPromptCacheKey))).String()
}

func applyCodexThreadIsolationBody(rawJSON []byte, state codexThreadIsolationState) []byte {
	if !state.enabled || len(rawJSON) == 0 {
		return rawJSON
	}
	rawJSON, _ = sjson.SetBytes(rawJSON, "prompt_cache_key", state.canonicalPromptCacheKey)
	if gjson.GetBytes(rawJSON, "metadata.session_id").Exists() {
		rawJSON, _ = sjson.SetBytes(rawJSON, "metadata.session_id", state.canonicalPromptCacheKey)
	}
	if previous := strings.TrimSpace(gjson.GetBytes(rawJSON, "previous_response_id").String()); previous != "" {
		if resolved := resolveCodexPreviousResponseID(previous, state.authScope); resolved != "" {
			rawJSON, _ = sjson.SetBytes(rawJSON, "previous_response_id", resolved)
		} else {
			rawJSON, _ = sjson.DeleteBytes(rawJSON, "previous_response_id")
		}
	}
	if windowID := strings.TrimSpace(gjson.GetBytes(rawJSON, "client_metadata.x-codex-window-id").String()); windowID != "" {
		rawJSON, _ = sjson.SetBytes(rawJSON, "client_metadata.x-codex-window-id", state.canonicalPromptCacheKey+":0")
	}
	if turnMetadata := strings.TrimSpace(gjson.GetBytes(rawJSON, "client_metadata.x-codex-turn-metadata").String()); turnMetadata != "" {
		rawJSON, _ = sjson.SetBytes(rawJSON, "client_metadata.x-codex-turn-metadata", applyCodexThreadIsolationTurnMetadata(turnMetadata, state))
	}
	return rawJSON
}

func stripCodexThreadIdentifiers(rawJSON []byte) []byte {
	if len(rawJSON) == 0 {
		return rawJSON
	}
	for _, path := range []string{
		"prompt_cache_key",
		"previous_response_id",
		"metadata.session_id",
		"client_metadata.x-codex-window-id",
		"client_metadata.x-codex-turn-metadata",
	} {
		rawJSON, _ = sjson.DeleteBytes(rawJSON, path)
	}
	return rawJSON
}

func applyCodexThreadIsolationTurnMetadata(rawTurnMetadata string, state codexThreadIsolationState) string {
	if !state.enabled || strings.TrimSpace(rawTurnMetadata) == "" {
		return rawTurnMetadata
	}
	updated := rawTurnMetadata
	if gjson.Get(rawTurnMetadata, "prompt_cache_key").Exists() {
		updated, _ = sjson.Set(updated, "prompt_cache_key", state.canonicalPromptCacheKey)
	}
	if gjson.Get(rawTurnMetadata, "window_id").Exists() {
		updated, _ = sjson.Set(updated, "window_id", state.canonicalPromptCacheKey+":0")
	}
	return updated
}

func applyCodexThreadIsolationHeaders(headers httpHeader, state codexThreadIsolationState) {
	if headers == nil {
		return
	}
	if state.rejectProviderThread {
		requestID := uuid.NewString()
		headers.Set("Session_id", requestID)
		headers.Set("X-Client-Request-Id", requestID)
		headers.Del("X-Codex-Parent-Thread-Id")
		headers.Del("X-Codex-Window-Id")
		headers.Del("Thread-Id")
		headers.Del("Conversation_id")
		headers.Del("X-Codex-Turn-Metadata")
		return
	}
	if !state.enabled {
		return
	}
	headers.Set("Session_id", state.canonicalPromptCacheKey)
	headers.Set("X-Client-Request-Id", state.canonicalPromptCacheKey)
	headers.Set("Thread-Id", state.canonicalPromptCacheKey)
	headers.Set("X-Codex-Window-Id", state.canonicalPromptCacheKey+":0")
	headers.Del("X-Codex-Parent-Thread-Id")
	headers.Del("Conversation_id")
	if turnMetadata := strings.TrimSpace(headers.Get("X-Codex-Turn-Metadata")); turnMetadata != "" {
		headers.Set("X-Codex-Turn-Metadata", applyCodexThreadIsolationTurnMetadata(turnMetadata, state))
	}
}

type httpHeader interface {
	Set(string, string)
	Del(string)
	Get(string) string
}

func resolveCodexPreviousResponseID(value, authScope string) string {
	value = strings.TrimSpace(value)
	if value == "" || authScope == "" {
		return value
	}
	now := time.Now()
	codexResponseBindings.RLock()
	binding, ok := codexResponseBindings.byID[value]
	codexResponseBindings.RUnlock()
	if !ok || binding.expires.Before(now) || binding.authScope != authScope {
		return ""
	}
	return binding.rawID
}

func registerCodexResponseIDs(payload []byte, state codexThreadIsolationState) {
	if !state.enabled || len(payload) == 0 {
		return
	}
	now := time.Now()
	for _, rawID := range codexResponseIDs(payload) {
		alias := codexResponseAlias(state.authScope, rawID)
		binding := codexResponseBinding{rawID: rawID, authScope: state.authScope, expires: now.Add(codexThreadIdentityTTL)}
		codexResponseBindings.Lock()
		for key, existing := range codexResponseBindings.byID {
			if existing.expires.Before(now) {
				delete(codexResponseBindings.byID, key)
			}
		}
		codexResponseBindings.byID[rawID] = binding
		codexResponseBindings.byID[alias] = binding
		codexResponseBindings.Unlock()
	}
}

func codexResponseAlias(scope, rawID string) string {
	return "codex_resp_" + uuid.NewSHA1(uuid.NameSpaceOID, []byte("cli-proxy-api:codex-response:\x00"+scope+"\x00"+strings.TrimSpace(rawID))).String()
}

func codexResponseIDs(payload []byte) []string {
	var ids []string
	for _, line := range bytes.Split(payload, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if bytes.HasPrefix(line, []byte("data:")) {
			line = bytes.TrimSpace(line[len("data:"):])
		}
		if !gjson.ValidBytes(line) {
			continue
		}
		typeName := gjson.GetBytes(line, "type").String()
		if !strings.HasPrefix(typeName, "response.") && !gjson.GetBytes(line, "response.id").Exists() {
			continue
		}
		for _, path := range []string{"response.id", "id"} {
			if id := strings.TrimSpace(gjson.GetBytes(line, path).String()); id != "" {
				ids = append(ids, id)
			}
		}
	}
	return ids
}

func applyCodexThreadIsolationExposeResponsePayload(payload []byte, state codexThreadIsolationState) []byte {
	if !state.enabled || len(payload) == 0 {
		return payload
	}
	for _, rawID := range codexResponseIDs(payload) {
		payload = replaceCodexIdentityResponsePayload(payload, rawID, codexResponseAlias(state.authScope, rawID))
	}
	return replaceCodexIdentityResponsePayload(payload, state.canonicalPromptCacheKey, state.clientPromptCacheKey)
}

func applyCodexClientResponsePayload(payload []byte, identityState codexIdentityConfuseState) []byte {
	registerCodexResponseIDs(payload, identityState.threadIsolation)
	payload = applyCodexIdentityExposeResponsePayload(payload, identityState)
	return applyCodexThreadIsolationExposeResponsePayload(payload, identityState.threadIsolation)
}
