package main

import (
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
)

func TestShouldStartExampleAPIKeyWarningServer(t *testing.T) {
	cfg := &config.Config{SDKConfig: config.SDKConfig{APIKeys: []string{"your-api-key-1"}}}
	if !shouldStartExampleAPIKeyWarningServer(cfg, false, false) {
		t.Fatal("expected warning server for example API key")
	}
	if shouldStartExampleAPIKeyWarningServer(cfg, true, false) {
		t.Fatal("command mode should not start warning server")
	}
	if shouldStartExampleAPIKeyWarningServer(cfg, false, true) {
		t.Fatal("cloud config missing mode should not start warning server")
	}
	if shouldStartExampleAPIKeyWarningServer(&config.Config{SDKConfig: config.SDKConfig{APIKeys: []string{"real-key"}}}, false, false) {
		t.Fatal("real API key should not start warning server")
	}
}
