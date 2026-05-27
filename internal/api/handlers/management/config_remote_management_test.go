package management

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	"golang.org/x/crypto/bcrypt"
)

func newRemoteManagementConfigRouter(h *Handler) *gin.Engine {
	r := gin.New()
	r.GET("/remote-management/upload-key", h.GetRemoteManagementUploadKey)
	r.PUT("/remote-management/upload-key", h.PutRemoteManagementUploadKey)
	r.DELETE("/remote-management/upload-key", h.DeleteRemoteManagementUploadKey)
	return r
}

func performJSONRequest(r http.Handler, method, path string, payload any) *httptest.ResponseRecorder {
	var body bytes.Buffer
	if payload != nil {
		_ = json.NewEncoder(&body).Encode(payload)
	}
	req := httptest.NewRequest(method, path, &body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func writeRemoteManagementConfig(t *testing.T, dir string, uploadKey string) string {
	t.Helper()
	configFile := filepath.Join(dir, "config.yaml")
	data := "remote-management:\n  allow-remote: true\n  secret-key: \"\"\n  upload-key: \"" + uploadKey + "\"\n  disable-control-panel: false\n"
	if err := os.WriteFile(configFile, []byte(data), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return configFile
}

func TestRemoteManagementUploadKeyStatusDoesNotExposeSecret(t *testing.T) {
	gin.SetMode(gin.TestMode)

	hashed := bcryptTestSecret(t, "upload-secret")
	h := &Handler{cfg: &config.Config{RemoteManagement: config.RemoteManagement{UploadKey: hashed}}}
	r := newRemoteManagementConfigRouter(h)

	w := performJSONRequest(r, http.MethodGet, "/remote-management/upload-key", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), hashed) || strings.Contains(w.Body.String(), "upload-secret") {
		t.Fatalf("status response exposed upload key: %s", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"configured":true`) {
		t.Fatalf("status response did not report configured=true: %s", w.Body.String())
	}
}

func TestPutRemoteManagementUploadKeyHashesAndPersists(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tmpDir := t.TempDir()
	configFile := writeRemoteManagementConfig(t, tmpDir, "")
	h := &Handler{cfg: &config.Config{}, configFilePath: configFile}
	r := newRemoteManagementConfigRouter(h)

	w := performJSONRequest(r, http.MethodPut, "/remote-management/upload-key", gin.H{"value": "upload-secret"})
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	if h.cfg.RemoteManagement.UploadKey == "" || h.cfg.RemoteManagement.UploadKey == "upload-secret" {
		t.Fatalf("upload key was not hashed: %q", h.cfg.RemoteManagement.UploadKey)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(h.cfg.RemoteManagement.UploadKey), []byte("upload-secret")); err != nil {
		t.Fatalf("hash does not match upload-secret: %v", err)
	}
	data, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if strings.Contains(string(data), "upload-secret") {
		t.Fatalf("persisted plaintext upload key: %s", string(data))
	}
	if !strings.Contains(string(data), h.cfg.RemoteManagement.UploadKey) {
		t.Fatalf("persisted config missing hashed upload key: %s", string(data))
	}
}

func TestDeleteRemoteManagementUploadKeyClearsAndPersists(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tmpDir := t.TempDir()
	hashed := bcryptTestSecret(t, "upload-secret")
	configFile := writeRemoteManagementConfig(t, tmpDir, hashed)
	h := &Handler{
		cfg: &config.Config{
			RemoteManagement: config.RemoteManagement{UploadKey: hashed},
		},
		configFilePath: configFile,
	}
	r := newRemoteManagementConfigRouter(h)

	w := performJSONRequest(r, http.MethodDelete, "/remote-management/upload-key", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	if h.cfg.RemoteManagement.UploadKey != "" {
		t.Fatalf("upload key not cleared: %q", h.cfg.RemoteManagement.UploadKey)
	}
	data, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if strings.Contains(string(data), hashed) {
		t.Fatalf("persisted config still contains old hash: %s", string(data))
	}
}
