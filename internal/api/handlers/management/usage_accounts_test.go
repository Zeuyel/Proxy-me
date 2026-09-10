package management

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	coreusage "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/usage"
)

func TestGetQuotaAuditSyncsCurrentCodexAuthRoster(t *testing.T) {
	coreusage.SyncQuotaAuditAccounts(nil)
	t.Cleanup(func() { coreusage.SyncQuotaAuditAccounts(nil) })

	manager := coreauth.NewManager(&memoryAuthStore{}, nil, nil)
	auth := &coreauth.Auth{
		ID:       "codex-new.json",
		FileName: "codex-new.json",
		Provider: "codex",
		Metadata: map[string]any{"email": "new-one@example.com"},
	}
	if _, err := manager.Register(context.Background(), auth); err != nil {
		t.Fatalf("register auth: %v", err)
	}
	second := &coreauth.Auth{
		ID:       "codex-second.json",
		FileName: "codex-second.json",
		Provider: "codex",
		Metadata: map[string]any{"email": "new-two@example.com"},
	}
	if _, err := manager.Register(context.Background(), second); err != nil {
		t.Fatalf("register second auth: %v", err)
	}

	h := &Handler{authManager: manager}
	r := gin.New()
	r.GET("/usage/quota-audit", h.GetQuotaAudit)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/usage/quota-audit", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}

	var response coreusage.QuotaAuditResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(response.Accounts) != 2 {
		t.Fatalf("expected current auth roster, got %#v", response.Accounts)
	}
	if response.Accounts[0].Account != response.Accounts[1].Account {
		t.Fatalf("expected masked labels to collide in regression setup, got %#v", response.Accounts)
	}
	if response.Summary.Accounts != 2 {
		t.Fatalf("expected two identities in summary, got %d", response.Summary.Accounts)
	}
}
