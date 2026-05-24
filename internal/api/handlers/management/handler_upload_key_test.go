package management

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	"golang.org/x/crypto/bcrypt"
)

func bcryptTestSecret(t *testing.T, secret string) string {
	t.Helper()
	hashed, err := bcrypt.GenerateFromPassword([]byte(secret), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash test secret: %v", err)
	}
	return string(hashed)
}

func newMiddlewareTestRouter(h *Handler) *gin.Engine {
	r := gin.New()
	mgmt := r.Group("/v0/management")
	mgmt.Use(h.Middleware())
	mgmt.POST("/auth-files", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"route": "upload"}) })
	mgmt.GET("/auth-files", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"route": "list"}) })
	mgmt.PATCH("/auth-files/metadata", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"route": "metadata"}) })
	return r
}

func performMiddlewareRequest(r http.Handler, method, path, headerName, headerValue string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	if headerName != "" {
		req.Header.Set(headerName, headerValue)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestManagementMiddlewareUploadKeyAllowsOnlyAuthFileUpload(t *testing.T) {
	gin.SetMode(gin.TestMode)

	h := &Handler{
		cfg: &config.Config{
			RemoteManagement: config.RemoteManagement{
				AllowRemote: true,
				UploadKey:   bcryptTestSecret(t, "upload-secret"),
			},
		},
		failedAttempts: make(map[string]*attemptInfo),
	}
	r := newMiddlewareTestRouter(h)

	if w := performMiddlewareRequest(r, http.MethodPost, "/v0/management/auth-files", authFileUploadKeyHeader, "upload-secret"); w.Code != http.StatusOK {
		t.Fatalf("upload with upload key status = %d, body=%s", w.Code, w.Body.String())
	}
	if w := performMiddlewareRequest(r, http.MethodPost, "/v0/management/auth-files", "Authorization", "Bearer upload-secret"); w.Code != http.StatusOK {
		t.Fatalf("upload with bearer upload key status = %d, body=%s", w.Code, w.Body.String())
	}
	if w := performMiddlewareRequest(r, http.MethodGet, "/v0/management/auth-files", authFileUploadKeyHeader, "upload-secret"); w.Code == http.StatusOK {
		t.Fatalf("upload key unexpectedly allowed list route")
	}
	if w := performMiddlewareRequest(r, http.MethodPatch, "/v0/management/auth-files/metadata", authFileUploadKeyHeader, "upload-secret"); w.Code == http.StatusOK {
		t.Fatalf("upload key unexpectedly allowed metadata route")
	}
}

func TestManagementMiddlewareUploadKeyDoesNotReplaceManagementKey(t *testing.T) {
	gin.SetMode(gin.TestMode)

	h := &Handler{
		cfg: &config.Config{
			RemoteManagement: config.RemoteManagement{
				AllowRemote: true,
				SecretKey:   bcryptTestSecret(t, "management-secret"),
				UploadKey:   bcryptTestSecret(t, "upload-secret"),
			},
		},
		failedAttempts: make(map[string]*attemptInfo),
	}
	r := newMiddlewareTestRouter(h)

	if w := performMiddlewareRequest(r, http.MethodGet, "/v0/management/auth-files", "Authorization", "Bearer management-secret"); w.Code != http.StatusOK {
		t.Fatalf("management key list status = %d, body=%s", w.Code, w.Body.String())
	}
	if w := performMiddlewareRequest(r, http.MethodPost, "/v0/management/auth-files", "Authorization", "Bearer management-secret"); w.Code != http.StatusOK {
		t.Fatalf("management key upload status = %d, body=%s", w.Code, w.Body.String())
	}
	if w := performMiddlewareRequest(r, http.MethodGet, "/v0/management/auth-files", authFileUploadKeyHeader, "upload-secret"); w.Code == http.StatusOK {
		t.Fatalf("upload key unexpectedly replaced management key")
	}
	if w := performMiddlewareRequest(r, http.MethodPost, "/v0/management/auth-files", authFileUploadKeyHeader, "wrong-secret"); w.Code != http.StatusUnauthorized {
		t.Fatalf("wrong upload key status = %d, body=%s", w.Code, w.Body.String())
	}
}
