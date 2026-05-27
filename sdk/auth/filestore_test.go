package auth

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFileTokenStoreReadAuthFileHydratesCodexPlanType(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "codex.json")
	token := testFileStoreCodexJWT("store@example.com", "acct_store", "plus")
	if err := os.WriteFile(path, []byte(`{"type":"codex","email":"store@example.com","id_token":`+quoteJSON(token)+`}`), 0o600); err != nil {
		t.Fatalf("write auth file: %v", err)
	}

	store := NewFileTokenStore()
	auth, err := store.readAuthFile(path, dir)
	if err != nil {
		t.Fatalf("readAuthFile returned error: %v", err)
	}
	if auth == nil {
		t.Fatal("expected auth record")
	}
	if got, _ := auth.Metadata["plan_type"].(string); got != "plus" {
		t.Fatalf("metadata plan_type = %q, want plus", got)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read hydrated file: %v", err)
	}
	var persisted map[string]any
	if err := json.Unmarshal(raw, &persisted); err != nil {
		t.Fatalf("decode hydrated file: %v", err)
	}
	if got, _ := persisted["plan_type"].(string); got != "plus" {
		t.Fatalf("persisted plan_type = %q, want plus", got)
	}
}

func TestFileTokenStoreReadAuthFileDoesNotHydrateFreePlanTypeFromAccessToken(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "codex.json")
	token := testFileStoreCodexJWT("store@example.com", "acct_store", "free")
	idTokenJSON := `{"chatgpt_account_id":"acct_store","chatgpt_plan_type":"free"}`
	if err := os.WriteFile(path, []byte(`{"type":"codex","email":"store@example.com","id_token":`+quoteJSON(idTokenJSON)+`,"access_token":`+quoteJSON(token)+`}`), 0o600); err != nil {
		t.Fatalf("write auth file: %v", err)
	}

	store := NewFileTokenStore()
	auth, err := store.readAuthFile(path, dir)
	if err != nil {
		t.Fatalf("readAuthFile returned error: %v", err)
	}
	if auth == nil {
		t.Fatal("expected auth record")
	}
	if got, ok := auth.Metadata["plan_type"].(string); ok && got != "" {
		t.Fatalf("metadata plan_type = %q, want empty", got)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read hydrated file: %v", err)
	}
	var persisted map[string]any
	if err := json.Unmarshal(raw, &persisted); err != nil {
		t.Fatalf("decode hydrated file: %v", err)
	}
	if got, ok := persisted["plan_type"].(string); ok && got != "" {
		t.Fatalf("persisted plan_type = %q, want empty", got)
	}
}

func testFileStoreCodexJWT(email, accountID, planType string) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"email":` + quoteJSON(email) + `,"https://api.openai.com/auth":{"chatgpt_account_id":` + quoteJSON(accountID) + `,"chatgpt_plan_type":` + quoteJSON(planType) + `}}`))
	return header + "." + payload + ".signature"
}

func quoteJSON(value string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	return `"` + replacer.Replace(value) + `"`
}
