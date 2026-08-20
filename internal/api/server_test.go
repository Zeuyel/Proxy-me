package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gin "github.com/gin-gonic/gin"
	proxyconfig "github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/usage"
	sdkaccess "github.com/router-for-me/CLIProxyAPI/v6/sdk/access"
	"github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	coreusage "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/usage"
	sdkconfig "github.com/router-for-me/CLIProxyAPI/v6/sdk/config"
	"golang.org/x/crypto/bcrypt"
)

func usageRecordForStateFlush() coreusage.Record {
	return coreusage.Record{Provider: "codex", APIKey: "stop-state-api", Model: "gpt-5.4", RequestID: "stop-state-request", RequestedAt: time.Now().UTC(), Detail: coreusage.Detail{InputTokens: 1, OutputTokens: 2, TotalTokens: 3}}
}

func TestServerStopFlushesUsageState(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state", "usage-state.json")
	t.Setenv("USAGE_STATE_PATH", statePath)
	t.Cleanup(usage.CloseUsageState)
	usage.SetStatisticsEnabled(true)
	t.Cleanup(func() { usage.SetStatisticsEnabled(false) })
	server := newTestServer(t)
	usage.GetRequestStatistics().Record(context.Background(), usageRecordForStateFlush())
	usage.PersistUsageState()
	if err := server.Stop(context.Background()); err != nil {
		t.Fatalf("stop server: %v", err)
	}
	data, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("read flushed state: %v", err)
	}
	var state usage.StateFile
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatalf("decode flushed state: %v", err)
	}
	if details := state.Usage.APIs["stop-state-api"].Models["gpt-5.4"].Details; len(details) != 1 || details[0].RequestID != "stop-state-request" {
		t.Fatalf("flushed usage details = %#v", details)
	}
}

func newTestServer(t *testing.T) *Server {
	t.Helper()

	gin.SetMode(gin.TestMode)
	t.Cleanup(usage.CloseUsageState)

	tmpDir := t.TempDir()
	authDir := filepath.Join(tmpDir, "auth")
	if err := os.MkdirAll(authDir, 0o700); err != nil {
		t.Fatalf("failed to create auth dir: %v", err)
	}

	cfg := &proxyconfig.Config{
		SDKConfig: sdkconfig.SDKConfig{
			APIKeys: []string{"test-key"},
		},
		Port:                   0,
		AuthDir:                authDir,
		Debug:                  true,
		LoggingToFile:          false,
		UsageStatisticsEnabled: false,
	}

	authManager := auth.NewManager(nil, nil, nil)
	accessManager := sdkaccess.NewManager()

	configPath := filepath.Join(tmpDir, "config.yaml")
	return NewServer(cfg, authManager, accessManager, configPath)
}

func TestManagementRoutesEnabledWithUploadKeyOnly(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tmpDir := t.TempDir()
	uploadKey, err := bcrypt.GenerateFromPassword([]byte("upload-secret"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash upload key: %v", err)
	}
	cfg := &proxyconfig.Config{
		SDKConfig: sdkconfig.SDKConfig{
			APIKeys: []string{"test-key"},
		},
		Port:    0,
		AuthDir: tmpDir,
		RemoteManagement: proxyconfig.RemoteManagement{
			AllowRemote:         true,
			UploadKey:           string(uploadKey),
			DisableControlPanel: true,
		},
	}
	server := NewServer(cfg, auth.NewManager(nil, nil, nil), sdkaccess.NewManager(), filepath.Join(tmpDir, "config.yaml"))

	req := httptest.NewRequest(http.MethodGet, "/v0/management/auth-files", nil)
	req.Header.Set("X-Auth-Upload-Key", "upload-secret")
	rr := httptest.NewRecorder()
	server.engine.ServeHTTP(rr, req)

	if rr.Code == http.StatusNotFound {
		t.Fatalf("management routes were not registered with upload-key only")
	}
	if rr.Code == http.StatusOK {
		t.Fatalf("upload-only key unexpectedly allowed non-upload route")
	}
}

func TestAmpProviderModelRoutes(t *testing.T) {
	testCases := []struct {
		name         string
		path         string
		wantStatus   int
		wantContains string
	}{
		{
			name:         "openai root models",
			path:         "/api/provider/openai/models",
			wantStatus:   http.StatusOK,
			wantContains: `"object":"list"`,
		},
		{
			name:         "groq root models",
			path:         "/api/provider/groq/models",
			wantStatus:   http.StatusOK,
			wantContains: `"object":"list"`,
		},
		{
			name:         "openai models",
			path:         "/api/provider/openai/v1/models",
			wantStatus:   http.StatusOK,
			wantContains: `"object":"list"`,
		},
		{
			name:         "anthropic models",
			path:         "/api/provider/anthropic/v1/models",
			wantStatus:   http.StatusOK,
			wantContains: `"data"`,
		},
		{
			name:         "google models v1",
			path:         "/api/provider/google/v1/models",
			wantStatus:   http.StatusOK,
			wantContains: `"models"`,
		},
		{
			name:         "google models v1beta",
			path:         "/api/provider/google/v1beta/models",
			wantStatus:   http.StatusOK,
			wantContains: `"models"`,
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			server := newTestServer(t)

			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			req.Header.Set("Authorization", "Bearer test-key")

			rr := httptest.NewRecorder()
			server.engine.ServeHTTP(rr, req)

			if rr.Code != tc.wantStatus {
				t.Fatalf("unexpected status code for %s: got %d want %d; body=%s", tc.path, rr.Code, tc.wantStatus, rr.Body.String())
			}
			if body := rr.Body.String(); !strings.Contains(body, tc.wantContains) {
				t.Fatalf("response body for %s missing %q: %s", tc.path, tc.wantContains, body)
			}
		})
	}
}
