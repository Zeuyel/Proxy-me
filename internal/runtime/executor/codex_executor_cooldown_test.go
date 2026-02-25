package executor

import (
	"net/http"
	"testing"
	"time"
)

func TestParseRetryAfterHeader_Seconds(t *testing.T) {
	headers := http.Header{}
	headers.Set("Retry-After", "120")
	retryAfter := parseRetryAfterHeader(headers)
	if retryAfter == nil {
		t.Fatalf("expected retryAfter to be parsed")
	}
	if *retryAfter != 120*time.Second {
		t.Fatalf("expected 120s, got %v", *retryAfter)
	}
}

func TestCodexQuotaRecoverAt_PrefersNearestWindow(t *testing.T) {
	now := time.Now()
	payload := []byte(`{
		"rate_limit": {
			"limit_reached": true,
			"primary_window": { "reset_after_seconds": 18000 },
			"secondary_window": { "reset_after_seconds": 86400 }
		}
	}`)
	recoverAt, reason, ok := codexQuotaRecoverAt(payload, now)
	if !ok {
		t.Fatalf("expected cooldown recovery hint")
	}
	if reason != "codex_5h_limit" {
		t.Fatalf("expected codex_5h_limit, got %q", reason)
	}
	delta := recoverAt.Sub(now)
	if delta < 5*time.Hour-time.Minute || delta > 5*time.Hour+time.Minute {
		t.Fatalf("expected about 5h cooldown, got %v", delta)
	}
}

func TestCodexQuotaRecoverAt_WeeklyLimit(t *testing.T) {
	now := time.Now()
	payload := []byte(`{
		"rate_limit": {
			"limit_reached": true,
			"secondary_window": { "reset_after_seconds": 604800 }
		}
	}`)
	recoverAt, reason, ok := codexQuotaRecoverAt(payload, now)
	if !ok {
		t.Fatalf("expected cooldown recovery hint")
	}
	if reason != "codex_weekly_limit" {
		t.Fatalf("expected codex_weekly_limit, got %q", reason)
	}
	delta := recoverAt.Sub(now)
	if delta < 7*24*time.Hour-time.Minute || delta > 7*24*time.Hour+time.Minute {
		t.Fatalf("expected about 7d cooldown, got %v", delta)
	}
}

func TestCodexQuotaRecoverAt_WeeklyLimitFromPrimaryWindow(t *testing.T) {
	now := time.Now()
	payload := []byte(`{
		"rate_limit": {
			"limit_reached": true,
			"primary_window": {
				"limit_window_seconds": 604800,
				"reset_after_seconds": 604800
			}
		}
	}`)
	recoverAt, reason, ok := codexQuotaRecoverAt(payload, now)
	if !ok {
		t.Fatalf("expected cooldown recovery hint")
	}
	if reason != "codex_weekly_limit" {
		t.Fatalf("expected codex_weekly_limit, got %q", reason)
	}
	delta := recoverAt.Sub(now)
	if delta < 7*24*time.Hour-time.Minute || delta > 7*24*time.Hour+time.Minute {
		t.Fatalf("expected about 7d cooldown, got %v", delta)
	}
}
