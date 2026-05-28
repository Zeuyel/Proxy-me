package management

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	fileauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/auth"
	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
)

func newAuthFilesTestHandler(t *testing.T, authDir string) (*Handler, string, *coreauth.Manager) {
	t.Helper()
	configFile := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(configFile, []byte("auth-dir: "+authDir+"\n"), 0o600); err != nil {
		t.Fatalf("write config file: %v", err)
	}
	store := &memoryAuthStore{}
	manager := coreauth.NewManager(store, nil, nil)
	h := &Handler{
		cfg:            &config.Config{AuthDir: authDir},
		configFilePath: configFile,
		authManager:    manager,
	}
	return h, configFile, manager
}

func TestUploadAuthFileRecordsImportMetadata(t *testing.T) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	authDir := t.TempDir()
	h, configFile, manager := newAuthFilesTestHandler(t, authDir)

	r := gin.New()
	r.POST("/auth-files", h.UploadAuthFile)
	r.PATCH("/auth-files/metadata", h.PatchAuthFileMetadata)
	r.GET("/auth-files", h.ListAuthFiles)

	req := httptest.NewRequest(http.MethodPost, "/auth-files?name=alpha.json", bytes.NewBufferString(`{"type":"claude","email":"alpha@example.com"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("upload status = %d, body=%s", w.Code, w.Body.String())
	}

	meta := h.cfg.AuthFileMetadata["alpha.json"]
	if strings.TrimSpace(meta.ImportedAt) == "" {
		t.Fatalf("expected imported_at to be recorded, got %+v", meta)
	}

	updateReq := httptest.NewRequest(http.MethodPatch, "/auth-files/metadata", bytes.NewBufferString(`{"name":"alpha.json","display_name":"Alpha Team","tags":["prod","shared","prod"]}`))
	updateReq.Header.Set("Content-Type", "application/json")
	updateW := httptest.NewRecorder()
	r.ServeHTTP(updateW, updateReq)
	if updateW.Code != http.StatusOK {
		t.Fatalf("metadata status = %d, body=%s", updateW.Code, updateW.Body.String())
	}

	listReq := httptest.NewRequest(http.MethodGet, "/auth-files", nil)
	listW := httptest.NewRecorder()
	r.ServeHTTP(listW, listReq)
	if listW.Code != http.StatusOK {
		t.Fatalf("list status = %d, body=%s", listW.Code, listW.Body.String())
	}

	var payload struct {
		Files []map[string]any `json:"files"`
	}
	if err := json.Unmarshal(listW.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(payload.Files) != 1 {
		t.Fatalf("expected 1 file, got %+v", payload.Files)
	}
	file := payload.Files[0]
	if got := strings.TrimSpace(file["name"].(string)); got != "alpha.json" {
		t.Fatalf("name = %q", got)
	}
	if got := strings.TrimSpace(file["display_name"].(string)); got != "Alpha Team" {
		t.Fatalf("display_name = %q", got)
	}
	tagsValue, ok := file["tags"].([]any)
	if !ok || len(tagsValue) != 2 {
		t.Fatalf("unexpected tags: %#v", file["tags"])
	}
	if importedAt := strings.TrimSpace(file["imported_at"].(string)); importedAt == "" {
		t.Fatalf("expected imported_at in list payload, got %#v", file["imported_at"])
	}

	rawConfig, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatalf("read config file: %v", err)
	}
	if !bytes.Contains(rawConfig, []byte("auth-file-metadata")) {
		t.Fatalf("expected config file to persist auth-file-metadata, got:\n%s", string(rawConfig))
	}

	if auth, ok := manager.GetByID("alpha.json"); !ok || auth == nil {
		t.Fatalf("expected uploaded auth to be registered")
	}
}

func TestListAuthFilesIncludesUpstreamRuntimeFields(t *testing.T) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	authDir := t.TempDir()
	h, _, manager := newAuthFilesTestHandler(t, authDir)
	authPath := filepath.Join(authDir, "alpha.json")
	if err := os.WriteFile(authPath, []byte(`{"type":"codex","email":"alpha@example.com","project_id":"proj-1","priority":7,"note":"primary","websockets":true}`), 0o600); err != nil {
		t.Fatalf("write auth file: %v", err)
	}

	auth := &coreauth.Auth{
		ID:         "alpha.json",
		FileName:   "alpha.json",
		Provider:   "codex",
		Status:     coreauth.StatusActive,
		Metadata:   map[string]any{"type": "codex", "email": "alpha@example.com", "project_id": "proj-1", "priority": float64(7), "note": "primary", "websockets": true},
		Attributes: map[string]string{"path": authPath, "priority": "7", "note": "primary", "websockets": "true"},
	}
	if _, err := manager.Register(context.Background(), auth); err != nil {
		t.Fatalf("register auth: %v", err)
	}
	manager.MarkResult(context.Background(), coreauth.Result{AuthID: "alpha.json", Provider: "codex", Model: "gpt-5", Success: true})
	manager.MarkResult(context.Background(), coreauth.Result{AuthID: "alpha.json", Provider: "codex", Model: "gpt-5", Success: false, Error: &coreauth.Error{Message: "boom", HTTPStatus: http.StatusBadGateway}})

	r := gin.New()
	r.GET("/auth-files", h.ListAuthFiles)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/auth-files", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("list status = %d, body=%s", w.Code, w.Body.String())
	}

	var payload struct {
		Files []map[string]any `json:"files"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(payload.Files) != 1 {
		t.Fatalf("expected 1 file, got %+v", payload.Files)
	}
	file := payload.Files[0]
	if got := strings.TrimSpace(file["provider"].(string)); got != "codex" {
		t.Fatalf("provider = %q", got)
	}
	if got := strings.TrimSpace(file["platform"].(string)); got != "codex" {
		t.Fatalf("platform = %q", got)
	}
	if got := strings.TrimSpace(file["project_id"].(string)); got != "proj-1" {
		t.Fatalf("project_id = %q", got)
	}
	if got := int(file["priority"].(float64)); got != 7 {
		t.Fatalf("priority = %d", got)
	}
	if got := strings.TrimSpace(file["note"].(string)); got != "primary" {
		t.Fatalf("note = %q", got)
	}
	if got := file["websockets"]; got != true {
		t.Fatalf("websockets = %#v", got)
	}
	if got := int(file["success"].(float64)); got != 1 {
		t.Fatalf("success = %d", got)
	}
	if got := int(file["failed"].(float64)); got != 1 {
		t.Fatalf("failed = %d", got)
	}
	recent, ok := file["recent_requests"].([]any)
	if !ok || len(recent) != 20 {
		t.Fatalf("recent_requests = %#v", file["recent_requests"])
	}
}

func TestPatchAuthFileFieldsUpdatesMetadataAndRuntime(t *testing.T) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	authDir := t.TempDir()
	h, _, manager := newAuthFilesTestHandler(t, authDir)
	authPath := filepath.Join(authDir, "fields.json")
	if err := os.WriteFile(authPath, []byte(`{"type":"claude","email":"fields@example.com"}`), 0o600); err != nil {
		t.Fatalf("write auth file: %v", err)
	}
	auth := &coreauth.Auth{
		ID:         "fields.json",
		FileName:   "fields.json",
		Provider:   "claude",
		Status:     coreauth.StatusActive,
		Metadata:   map[string]any{"type": "claude", "email": "fields@example.com"},
		Attributes: map[string]string{"path": authPath},
	}
	if _, err := manager.Register(context.Background(), auth); err != nil {
		t.Fatalf("register auth: %v", err)
	}

	r := gin.New()
	r.PATCH("/auth-files/fields", h.PatchAuthFileFields)
	body := bytes.NewBufferString(`{"name":"fields.json","prefix":"team-a","proxy_url":"http://proxy.local","headers":{"X-Test":"yes"},"priority":9,"note":"patched","websockets":false,"nested.cde":true}`)
	req := httptest.NewRequest(http.MethodPatch, "/auth-files/fields", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("patch status = %d, body=%s", w.Code, w.Body.String())
	}

	updated, ok := manager.GetByID("fields.json")
	if !ok || updated == nil {
		t.Fatalf("expected updated auth")
	}
	if updated.Prefix != "team-a" {
		t.Fatalf("prefix = %q", updated.Prefix)
	}
	if updated.ProxyURL != "http://proxy.local" {
		t.Fatalf("proxy_url = %q", updated.ProxyURL)
	}
	if got := updated.Attributes["header:X-Test"]; got != "yes" {
		t.Fatalf("header attr = %q", got)
	}
	if got := updated.Attributes["priority"]; got != "9" {
		t.Fatalf("priority attr = %q", got)
	}
	if got := updated.Attributes["note"]; got != "patched" {
		t.Fatalf("note attr = %q", got)
	}
	if got, ok := authFileIntValue(updated.Metadata["priority"]); !ok || got != 9 {
		t.Fatalf("metadata priority = %#v", updated.Metadata["priority"])
	}
	if got := updated.Metadata["note"]; got != "patched" {
		t.Fatalf("metadata note = %#v", got)
	}
	if got := updated.Attributes["websockets"]; got != "false" {
		t.Fatalf("websockets attr = %q", got)
	}
	if got, ok := updated.Metadata["websockets"].(bool); !ok || got {
		t.Fatalf("metadata websockets = %#v", updated.Metadata["websockets"])
	}
	nested, ok := updated.Metadata["nested"].(map[string]any)
	if !ok {
		t.Fatalf("metadata nested = %#v", updated.Metadata["nested"])
	}
	if got := nested["cde"]; got != true {
		t.Fatalf("metadata nested.cde = %#v", got)
	}
	headers := coreauth.ExtractCustomHeadersFromMetadata(updated.Metadata)
	if got := headers["X-Test"]; got != "yes" {
		t.Fatalf("metadata header = %q", got)
	}
}

func TestPatchAuthFileFieldsPersistsArbitraryFieldsToFile(t *testing.T) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	authDir := t.TempDir()
	fileName := "generic.json"
	filePath := filepath.Join(authDir, fileName)

	store := fileauth.NewFileTokenStore()
	store.SetBaseDir(authDir)
	manager := coreauth.NewManager(store, nil, nil)
	auth := &coreauth.Auth{
		ID:       fileName,
		FileName: fileName,
		Provider: "codex",
		Status:   coreauth.StatusActive,
		Metadata: map[string]any{"type": "codex"},
		Attributes: map[string]string{
			"path": filePath,
		},
	}
	if _, err := manager.Register(context.Background(), auth); err != nil {
		t.Fatalf("register auth: %v", err)
	}

	h := &Handler{cfg: &config.Config{AuthDir: authDir}, authManager: manager}
	r := gin.New()
	r.PATCH("/auth-files/fields", h.PatchAuthFileFields)

	body := bytes.NewBufferString(`{"name":"generic.json","abc":true,"nested.cde":true,"fgh":{"ijk":true}}`)
	req := httptest.NewRequest(http.MethodPatch, "/auth-files/fields", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("patch status = %d, body=%s", w.Code, w.Body.String())
	}

	raw, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("read updated auth file: %v", err)
	}
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatalf("decode auth file: %v", err)
	}
	if got := data["abc"]; got != true {
		t.Fatalf("abc = %#v", got)
	}
	nested, ok := data["nested"].(map[string]any)
	if !ok {
		t.Fatalf("nested = %#v", data["nested"])
	}
	if got := nested["cde"]; got != true {
		t.Fatalf("nested.cde = %#v", got)
	}
	fgh, ok := data["fgh"].(map[string]any)
	if !ok {
		t.Fatalf("fgh = %#v", data["fgh"])
	}
	if got := fgh["ijk"]; got != true {
		t.Fatalf("fgh.ijk = %#v", got)
	}
}

func TestUploadAuthFilePersistsCodexPlanTypeFromIDToken(t *testing.T) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	authDir := t.TempDir()
	h, _, manager := newAuthFilesTestHandler(t, authDir)

	r := gin.New()
	r.POST("/auth-files", h.UploadAuthFile)

	idToken := testManagementCodexJWT("plus@example.com", "acct_plus", "plus")
	req := httptest.NewRequest(http.MethodPost, "/auth-files?name=codex-plus.json", bytes.NewBufferString(`{"type":"codex","email":"plus@example.com","id_token":`+managementQuoteJSON(idToken)+`}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("upload status = %d, body=%s", w.Code, w.Body.String())
	}

	raw, err := os.ReadFile(filepath.Join(authDir, "codex-plus.json"))
	if err != nil {
		t.Fatalf("read uploaded auth file: %v", err)
	}
	var persisted map[string]any
	if err := json.Unmarshal(raw, &persisted); err != nil {
		t.Fatalf("decode uploaded auth file: %v", err)
	}
	if got, _ := persisted["plan_type"].(string); got != "plus" {
		t.Fatalf("persisted plan_type = %q, want plus", got)
	}
	auth, ok := manager.GetByID("codex-plus.json")
	if !ok || auth == nil {
		t.Fatalf("expected uploaded auth to be registered")
	}
	if got, _ := auth.Metadata["plan_type"].(string); got != "plus" {
		t.Fatalf("registered plan_type = %q, want plus", got)
	}
}

func TestUploadAuthFileDoesNotPersistFreePlanTypeFromAccessToken(t *testing.T) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	authDir := t.TempDir()
	h, _, manager := newAuthFilesTestHandler(t, authDir)
	r := gin.New()
	r.POST("/auth-files", h.UploadAuthFile)

	accessToken := testManagementCodexJWT("plus@example.com", "acct_plus", "free")
	idTokenJSON := `{"chatgpt_account_id":"acct_plus","chatgpt_plan_type":"free"}`
	req := httptest.NewRequest(http.MethodPost, "/auth-files?name=codex-plus.json", bytes.NewBufferString(`{"type":"codex","email":"plus@example.com","id_token":`+managementQuoteJSON(idTokenJSON)+`,"access_token":`+managementQuoteJSON(accessToken)+`}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("upload status = %d, body=%s", w.Code, w.Body.String())
	}

	raw, err := os.ReadFile(filepath.Join(authDir, "codex-plus.json"))
	if err != nil {
		t.Fatalf("read uploaded auth file: %v", err)
	}
	var persisted map[string]any
	if err := json.Unmarshal(raw, &persisted); err != nil {
		t.Fatalf("decode uploaded auth file: %v", err)
	}
	if got, ok := persisted["plan_type"].(string); ok && got != "" {
		t.Fatalf("persisted plan_type = %q, want empty", got)
	}
	auth, ok := manager.GetByID("codex-plus.json")
	if !ok || auth == nil {
		t.Fatalf("expected uploaded auth to be registered")
	}
	if got, ok := auth.Metadata["plan_type"].(string); ok && got != "" {
		t.Fatalf("registered plan_type = %q, want empty", got)
	}
}

func TestRenameAuthFileMovesMetadataAndReferences(t *testing.T) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	authDir := t.TempDir()
	oldPath := filepath.Join(authDir, "old.json")
	if err := os.WriteFile(oldPath, []byte(`{"type":"claude","email":"old@example.com"}`), 0o600); err != nil {
		t.Fatalf("write auth file: %v", err)
	}

	h, configFile, manager := newAuthFilesTestHandler(t, authDir)
	h.cfg.AuthFileMetadata = map[string]config.AuthFileMetadata{
		"old.json": {
			ImportedAt:  "2026-01-01T00:00:00Z",
			DisplayName: "Old Display",
			Tags:        []string{"alpha", "beta"},
		},
	}
	h.cfg.ProxyRoutingAuth = map[string]string{"old.json": "proxy-a"}
	h.cfg.APIKeyAuth = map[string][]string{
		"client-a": {"old.json", "keep.json"},
	}
	if err := config.SaveConfigPreserveComments(configFile, h.cfg); err != nil {
		t.Fatalf("persist initial config: %v", err)
	}

	if _, err := manager.Register(context.Background(), &coreauth.Auth{
		ID:       "old.json",
		FileName: "old.json",
		Provider: "claude",
		Attributes: map[string]string{
			"path": oldPath,
		},
		Metadata: map[string]any{
			"type":  "claude",
			"email": "old@example.com",
		},
	}); err != nil {
		t.Fatalf("register auth: %v", err)
	}

	r := gin.New()
	r.PATCH("/auth-files/rename", h.RenameAuthFile)
	r.GET("/auth-files", h.ListAuthFiles)

	req := httptest.NewRequest(http.MethodPatch, "/auth-files/rename", bytes.NewBufferString(`{"name":"old.json","new_name":"new.json"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("rename status = %d, body=%s", w.Code, w.Body.String())
	}

	if _, err := os.Stat(filepath.Join(authDir, "old.json")); !os.IsNotExist(err) {
		t.Fatalf("expected old auth file to be moved, stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(authDir, "new.json")); err != nil {
		t.Fatalf("expected new auth file to exist: %v", err)
	}

	if _, ok := h.cfg.AuthFileMetadata["old.json"]; ok {
		t.Fatalf("expected old metadata key to be removed")
	}
	meta, ok := h.cfg.AuthFileMetadata["new.json"]
	if !ok {
		t.Fatalf("expected metadata to move to new filename")
	}
	if meta.ImportedAt != "2026-01-01T00:00:00Z" {
		t.Fatalf("expected imported_at to be preserved, got %q", meta.ImportedAt)
	}
	if meta.DisplayName != "Old Display" {
		t.Fatalf("expected display name to move, got %q", meta.DisplayName)
	}
	if len(meta.Tags) != 2 || meta.Tags[0] != "alpha" || meta.Tags[1] != "beta" {
		t.Fatalf("unexpected tags: %+v", meta.Tags)
	}

	if got := h.cfg.ProxyRoutingAuth["new.json"]; got != "proxy-a" {
		t.Fatalf("expected proxy routing to move, got %q", got)
	}
	if _, ok := h.cfg.ProxyRoutingAuth["old.json"]; ok {
		t.Fatalf("expected old proxy routing key to be removed")
	}
	if refs := h.cfg.APIKeyAuth["client-a"]; len(refs) != 2 || refs[0] != "new.json" || refs[1] != "keep.json" {
		t.Fatalf("unexpected api-key-auth refs: %+v", refs)
	}

	listReq := httptest.NewRequest(http.MethodGet, "/auth-files", nil)
	listW := httptest.NewRecorder()
	r.ServeHTTP(listW, listReq)
	if listW.Code != http.StatusOK {
		t.Fatalf("list status = %d, body=%s", listW.Code, listW.Body.String())
	}
	var payload struct {
		Files []map[string]any `json:"files"`
	}
	if err := json.Unmarshal(listW.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(payload.Files) != 1 || payload.Files[0]["name"] != "new.json" {
		t.Fatalf("unexpected list payload: %+v", payload.Files)
	}
}

func testManagementCodexJWT(email, accountID, planType string) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"email":` + managementQuoteJSON(email) + `,"https://api.openai.com/auth":{"chatgpt_account_id":` + managementQuoteJSON(accountID) + `,"chatgpt_plan_type":` + managementQuoteJSON(planType) + `}}`))
	return header + "." + payload + ".signature"
}

func managementQuoteJSON(value string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	return `"` + replacer.Replace(value) + `"`
}
