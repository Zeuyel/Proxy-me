package auth

import (
	"strings"
	"sync"
	"time"
)

const sessionAliasTTL = 24 * time.Hour

type sessionAliasBinding struct {
	sessionID string
	authKey   string
	expires   time.Time
}

var sessionAliases = struct {
	sync.RWMutex
	byAlias map[string]sessionAliasBinding
}{byAlias: make(map[string]sessionAliasBinding)}

// RegisterSessionAlias records the client session represented by an opaque response alias.
func RegisterSessionAlias(alias, sessionID, authKey string) {
	alias = strings.TrimSpace(alias)
	sessionID = strings.TrimSpace(sessionID)
	if alias == "" || sessionID == "" {
		return
	}
	now := time.Now()
	sessionAliases.Lock()
	for key, binding := range sessionAliases.byAlias {
		if binding.expires.Before(now) {
			delete(sessionAliases.byAlias, key)
		}
	}
	sessionAliases.byAlias[alias] = sessionAliasBinding{
		sessionID: sessionID,
		authKey:   strings.TrimSpace(authKey),
		expires:   now.Add(sessionAliasTTL),
	}
	sessionAliases.Unlock()
}

// ResolveSessionAlias returns the client session and original auth key for an opaque response alias.
func ResolveSessionAlias(alias string) (sessionID, authKey string, ok bool) {
	alias = strings.TrimSpace(alias)
	if alias == "" {
		return "", "", false
	}
	now := time.Now()
	sessionAliases.RLock()
	binding, found := sessionAliases.byAlias[alias]
	if found && binding.expires.Before(now) {
		found = false
	}
	sessionAliases.RUnlock()
	if !found {
		if strings.HasPrefix(alias, "codex_prev_") {
			return ResolveSessionAlias(strings.TrimPrefix(alias, "codex_prev_"))
		}
		return "", "", false
	}
	return binding.sessionID, binding.authKey, true
}
