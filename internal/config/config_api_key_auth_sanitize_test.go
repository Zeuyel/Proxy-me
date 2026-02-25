package config

import "testing"

func TestNormalizeAPIKeyAuthForKnownKeys_FiltersUnknownAPIKeys(t *testing.T) {
	in := map[string][]string{
		"key-1": {"auth-a", "auth-a", "  "},
		"key-2": {"auth-b"},
		"junk":  {"[object Object]"},
	}

	got := NormalizeAPIKeyAuthForKnownKeys(in, []string{"key-1", "key-2"})
	if len(got) != 2 {
		t.Fatalf("expected 2 keys after filtering, got %d", len(got))
	}
	if _, ok := got["junk"]; ok {
		t.Fatalf("unexpected unknown key kept in result")
	}
	if len(got["key-1"]) != 1 || got["key-1"][0] != "auth-a" {
		t.Fatalf("unexpected normalized refs for key-1: %#v", got["key-1"])
	}
	if len(got["key-2"]) != 1 || got["key-2"][0] != "auth-b" {
		t.Fatalf("unexpected normalized refs for key-2: %#v", got["key-2"])
	}
}

func TestSanitizeAPIKeyAuth_UsesConfiguredAPIKeys(t *testing.T) {
	cfg := &Config{
		SDKConfig: SDKConfig{
			APIKeys: []string{"1"},
		},
		APIKeyAuth: map[string][]string{
			"1":              {"codex-a.json"},
			"api-keys":       {"1", "2"},
			"proxy-routing":  {"[object Object]"},
			"request-retry":  {"3"},
			"unknown-client": {"auth-x"},
		},
	}

	cfg.SanitizeAPIKeyAuth()
	if len(cfg.APIKeyAuth) != 1 {
		t.Fatalf("expected only one api-key-auth entry after sanitize, got %d", len(cfg.APIKeyAuth))
	}
	refs, ok := cfg.APIKeyAuth["1"]
	if !ok {
		t.Fatalf("expected api-key-auth for key '1' to remain")
	}
	if len(refs) != 1 || refs[0] != "codex-a.json" {
		t.Fatalf("unexpected refs for key '1': %#v", refs)
	}
}
