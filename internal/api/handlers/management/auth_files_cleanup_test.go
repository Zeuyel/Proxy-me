package management

import (
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
)

func TestCleanupAuthMappings_PreservesDenyAllWhenRefsRemoved(t *testing.T) {
	h := &Handler{
		cfg: &config.Config{
			APIKeyAuth: map[string][]string{
				"client-1": {"auth-a.json"},
			},
		},
	}

	h.cleanupAuthMappings("", "", "auth-a.json", "")

	refs, ok := h.cfg.APIKeyAuth["client-1"]
	if !ok {
		t.Fatalf("expected client key to remain with deny-all entry")
	}
	if len(refs) != 0 {
		t.Fatalf("expected empty refs for deny-all semantics, got %#v", refs)
	}
}
