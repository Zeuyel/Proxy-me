package management

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/usage"
	coreusage "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/usage"
)

func TestNewHandlerRestoresUsageState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "usage-state.json")
	t.Setenv("USAGE_STATE_PATH", path)
	t.Cleanup(usage.CloseUsageState)
	stats := usage.NewRequestStatistics()
	stats.Record(context.Background(), coreusage.Record{Provider: "codex", APIKey: "startup-state-api", Model: "gpt-5.4", RequestID: "startup-state-request", RequestedAt: time.Now().UTC(), Detail: coreusage.Detail{InputTokens: 1, OutputTokens: 2, TotalTokens: 3}})
	quota := coreusage.NewQuotaAuditStore()
	quota.RecordQuotaSnapshot("startup-state-auth", "startup-state-index", "", time.Now().UTC(), []byte(`{"rate_limit":{"primary_window":{"used_percent":20}}}`))
	state := usage.StateFile{Version: 2, ExportedAt: time.Now().UTC(), Usage: stats.Snapshot(), QuotaAudit: quota.Export()}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("marshal state: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create state directory: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write state: %v", err)
	}

	h := NewHandler(&config.Config{}, filepath.Join(filepath.Dir(path), "config.yaml"), nil)
	if h == nil {
		t.Fatal("handler is nil")
	}
	if details := usage.GetRequestStatistics().Snapshot().APIs["startup-state-api"].Models["gpt-5.4"].Details; len(details) != 1 || details[0].RequestID != "startup-state-request" {
		t.Fatalf("restored usage details = %#v", details)
	}
	found := false
	for _, snapshot := range coreusage.DefaultQuotaAuditStore().Export().Snapshots {
		if snapshot.AuthID == "startup-state-auth" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("handler did not restore quota snapshot")
	}
}
