package config

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestLoadConfigHashesRemoteManagementUploadKey(t *testing.T) {
	configFile := filepath.Join(t.TempDir(), "config.yaml")
	raw := []byte(`remote-management:
  secret-key: "admin-secret"
  upload-key: "upload-secret"
`)
	if err := os.WriteFile(configFile, raw, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := LoadConfig(configFile)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if !looksLikeBcrypt(cfg.RemoteManagement.SecretKey) {
		t.Fatalf("secret-key was not hashed: %q", cfg.RemoteManagement.SecretKey)
	}
	if !looksLikeBcrypt(cfg.RemoteManagement.UploadKey) {
		t.Fatalf("upload-key was not hashed: %q", cfg.RemoteManagement.UploadKey)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(cfg.RemoteManagement.UploadKey), []byte("upload-secret")); err != nil {
		t.Fatalf("hashed upload-key does not match plaintext: %v", err)
	}

	persisted, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatalf("read persisted config: %v", err)
	}
	if string(persisted) == string(raw) {
		t.Fatalf("expected hashed keys to be persisted")
	}
}
