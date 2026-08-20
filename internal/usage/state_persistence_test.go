package usage

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	coreusage "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/usage"
)

func TestStatePersistenceRoundTripPreservesUsageQuotaAndManualPrices(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "state", "usage-state.json")
	sourceStats := NewRequestStatistics()
	sourceStats.Record(context.Background(), coreusage.Record{Provider: "codex", APIKey: "persisted-api", Model: "gpt-5.4", RequestID: "persisted-request", RequestedAt: time.Date(2026, 8, 20, 1, 2, 3, 0, time.UTC), Detail: coreusage.Detail{InputTokens: 3, OutputTokens: 4, TotalTokens: 7}})
	sourceQuota := coreusage.NewQuotaAuditStore()
	sourceQuota.SetManualPriceSnapshot("gpt-5.5", coreusage.PriceSnapshot{InputPerMillionUSD: floatPtr(9), OutputPerMillionUSD: floatPtr(10), Version: "manual"})
	sourceQuota.SetSyncedPriceSnapshot("gpt-5.4", coreusage.PriceSnapshot{InputPerMillionUSD: floatPtr(1), OutputPerMillionUSD: floatPtr(2), Version: "remote"})
	sourceQuota.RecordQuotaSnapshot("auth-id", "auth-index", "", time.Date(2026, 8, 20, 1, 0, 0, 0, time.UTC), []byte(`{"rate_limit":{"primary_window":{"used_percent":12}}}`))

	persistence := NewStatePersistence(path, sourceStats, sourceQuota)
	if err := persistence.Flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}
	persistence.Close()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("state file: %v", err)
	}
	if entries, err := os.ReadDir(filepath.Dir(path)); err != nil {
		t.Fatalf("read state directory: %v", err)
	} else if len(entries) != 1 || entries[0].Name() != filepath.Base(path) {
		t.Fatalf("temporary files remain: %#v", entries)
	}

	destStats := NewRequestStatistics()
	destQuota := coreusage.NewQuotaAuditStore()
	restored := NewStatePersistence(path, destStats, destQuota)
	if err := restored.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	restored.Close()
	if got := destStats.Snapshot().APIs["persisted-api"].Models["gpt-5.4"].Details; len(got) != 1 || got[0].RequestID != "persisted-request" {
		t.Fatalf("restored usage = %#v", got)
	}
	exported := destQuota.Export()
	if len(exported.Snapshots) != 1 || exported.PriceSnapshots["gpt-5.4"].Version != "remote" {
		t.Fatalf("restored quota = %#v", exported)
	}
	if len(exported.ManualPrices) != 1 || exported.ManualPrices[0] != "gpt-5.5" {
		t.Fatalf("restored manual prices = %#v", exported.ManualPrices)
	}
	if !destQuota.SetSyncedPriceSnapshot("gpt-5.4", coreusage.PriceSnapshot{InputPerMillionUSD: floatPtr(3), OutputPerMillionUSD: floatPtr(4), Version: "remote-next"}) {
		t.Fatal("restored synced price was treated as manual")
	}
	if destQuota.SetSyncedPriceSnapshot("gpt-5.5", coreusage.PriceSnapshot{InputPerMillionUSD: floatPtr(3), OutputPerMillionUSD: floatPtr(4), Version: "remote-next"}) {
		t.Fatal("restored manual price lost its override")
	}
}

func TestStatePersistenceRejectsCorruptFileWithoutMutatingState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage-state.json")
	if err := os.WriteFile(path, []byte("not-json"), 0o600); err != nil {
		t.Fatalf("write corrupt state: %v", err)
	}
	stats := NewRequestStatistics()
	quota := coreusage.NewQuotaAuditStore()
	persistence := NewStatePersistence(path, stats, quota)
	err := persistence.Load()
	persistence.Close()
	if err == nil {
		t.Fatal("expected corrupt state error")
	}
	if snapshot := stats.Snapshot(); snapshot.TotalRequests != 0 || len(quota.Export().Snapshots) != 0 {
		t.Fatalf("corrupt state mutated stores: %#v %#v", snapshot, quota.Export())
	}
}

func TestQuotaSnapshotChangeTriggersAtomicPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "usage-state.json")
	stats := NewRequestStatistics()
	quota := coreusage.NewQuotaAuditStore()
	persistence := NewStatePersistence(path, stats, quota)
	quota.SetChangeHook(persistence.MarkDirty)
	defer persistence.Close()
	quota.RecordQuotaSnapshot("snapshot-auth", "snapshot-index", "", time.Now().UTC(), []byte(`{"rate_limit":{"primary_window":{"used_percent":21}}}`))
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil {
			var state StateFile
			if json.Unmarshal(data, &state) == nil && len(state.QuotaAudit.Snapshots) == 1 {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("quota snapshot was not persisted")
}

func floatPtr(value float64) *float64 { return &value }
