package usage

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/util"
	coreusage "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/usage"
	log "github.com/sirupsen/logrus"
)

const usageStateFileVersion = 2
const usageStateDebounce = time.Second

// StateFile is the on-disk form shared by management export/import and runtime persistence.
type StateFile struct {
	Version    int                        `json:"version"`
	ExportedAt time.Time                  `json:"exported_at"`
	Usage      StatisticsSnapshot         `json:"usage"`
	QuotaAudit coreusage.QuotaAuditExport `json:"quota_audit,omitempty"`
}

type StatePersistence struct {
	path    string
	stats   *RequestStatistics
	quota   *coreusage.QuotaAuditStore
	writeMu sync.Mutex

	dirty chan struct{}
	stop  chan struct{}
	done  chan struct{}
	once  sync.Once
}

var defaultStateMu sync.Mutex
var defaultState *StatePersistence

// UsageStatePath resolves an explicit state path, WRITABLE_PATH, or the config directory.
func UsageStatePath(configPath string) string {
	if explicit := strings.TrimSpace(os.Getenv("USAGE_STATE_PATH")); explicit != "" {
		return filepath.Clean(explicit)
	}
	if base := util.WritablePath(); base != "" {
		return filepath.Join(base, "state", "usage-state.json")
	}
	if configPath != "" {
		return filepath.Join(filepath.Dir(configPath), "state", "usage-state.json")
	}
	return filepath.Join("state", "usage-state.json")
}

func NewStatePersistence(path string, stats *RequestStatistics, quota *coreusage.QuotaAuditStore) *StatePersistence {
	if stats == nil {
		stats = NewRequestStatistics()
	}
	if quota == nil {
		quota = coreusage.DefaultQuotaAuditStore()
	}
	cleanPath := strings.TrimSpace(path)
	if cleanPath != "" {
		cleanPath = filepath.Clean(cleanPath)
	}
	p := &StatePersistence{
		path:  cleanPath,
		stats: stats,
		quota: quota,
		dirty: make(chan struct{}, 1),
		stop:  make(chan struct{}),
		done:  make(chan struct{}),
	}
	go p.run()
	return p
}

// ConfigureUsageState loads the shared state once for the configured server path.
func ConfigureUsageState(configPath string) *StatePersistence {
	path := UsageStatePath(configPath)
	stats := GetRequestStatistics()
	quota := coreusage.DefaultQuotaAuditStore()
	defaultStateMu.Lock()
	if defaultState != nil && defaultState.path == path && defaultState.stats == stats && defaultState.quota == quota {
		current := defaultState
		defaultStateMu.Unlock()
		return current
	}
	previous := defaultState
	p := NewStatePersistence(path, stats, quota)
	defaultState = p
	defaultStateMu.Unlock()
	if previous != nil {
		previous.Close()
	}
	if err := p.Load(); err != nil {
		log.WithError(err).WithField("path", path).Warn("usage state restore skipped")
	}
	quota.SetChangeHook(PersistUsageState)
	return p
}

func CurrentStatePersistence() *StatePersistence {
	defaultStateMu.Lock()
	defer defaultStateMu.Unlock()
	return defaultState
}

func PersistUsageState() {
	if persistence := CurrentStatePersistence(); persistence != nil {
		persistence.MarkDirty()
	}
}

func FlushUsageState() error {
	if persistence := CurrentStatePersistence(); persistence != nil {
		return persistence.Flush()
	}
	return nil
}

func CloseUsageState() {
	defaultStateMu.Lock()
	persistence := defaultState
	defaultState = nil
	defaultStateMu.Unlock()
	if persistence != nil {
		persistence.Close()
	}
}

func (p *StatePersistence) Path() string {
	if p == nil {
		return ""
	}
	return p.path
}

func (p *StatePersistence) Load() error {
	if p == nil || p.path == "" {
		return nil
	}
	data, err := os.ReadFile(p.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read state file: %w", err)
	}
	var state StateFile
	if err := json.Unmarshal(data, &state); err != nil {
		return fmt.Errorf("decode state file: %w", err)
	}
	if state.Version != 0 && state.Version != 1 && state.Version != usageStateFileVersion {
		return fmt.Errorf("unsupported state file version %d", state.Version)
	}
	if p.stats != nil {
		p.stats.MergeSnapshot(state.Usage)
	}
	if p.quota != nil {
		p.quota.MergePersisted(state.QuotaAudit)
	}
	return nil
}

func (p *StatePersistence) MarkDirty() {
	if p == nil || p.path == "" {
		return
	}
	select {
	case p.dirty <- struct{}{}:
	default:
	}
}

func (p *StatePersistence) Flush() error {
	if p == nil || p.path == "" {
		return nil
	}
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	state := StateFile{Version: usageStateFileVersion, ExportedAt: time.Now().UTC()}
	if p.stats != nil {
		state.Usage = p.stats.Snapshot()
	}
	if p.quota != nil {
		state.QuotaAudit = p.quota.Export()
	}
	data, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("encode state file: %w", err)
	}
	dir := filepath.Dir(p.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create state directory: %w", err)
	}
	temp, err := os.CreateTemp(dir, ".usage-state-*.tmp")
	if err != nil {
		return fmt.Errorf("create state temp file: %w", err)
	}
	tempName := temp.Name()
	defer func() {
		_ = temp.Close()
		_ = os.Remove(tempName)
	}()
	if err := temp.Chmod(0o600); err != nil {
		return fmt.Errorf("set state temp permissions: %w", err)
	}
	if _, err := temp.Write(data); err != nil {
		return fmt.Errorf("write state temp file: %w", err)
	}
	if err := temp.Sync(); err != nil {
		return fmt.Errorf("sync state temp file: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close state temp file: %w", err)
	}
	if err := os.Rename(tempName, p.path); err != nil {
		return fmt.Errorf("replace state file: %w", err)
	}
	return nil
}

func (p *StatePersistence) run() {
	defer close(p.done)
	for {
		select {
		case <-p.dirty:
			timer := time.NewTimer(usageStateDebounce)
			select {
			case <-timer.C:
			case <-p.stop:
				if !timer.Stop() {
					<-timer.C
				}
				if err := p.Flush(); err != nil {
					log.WithError(err).WithField("path", p.path).Warn("usage state persistence failed")
				}
				return
			}
			if err := p.Flush(); err != nil {
				log.WithError(err).WithField("path", p.path).Warn("usage state persistence failed")
			}
		case <-p.stop:
			if err := p.Flush(); err != nil {
				log.WithError(err).WithField("path", p.path).Warn("usage state persistence failed")
			}
			return
		}
	}
}

func (p *StatePersistence) Close() {
	if p == nil {
		return
	}
	p.once.Do(func() {
		close(p.stop)
		<-p.done
	})
}
