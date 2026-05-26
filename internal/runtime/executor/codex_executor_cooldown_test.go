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

func TestCodexQuotaRecoverAt_NearFullWindow(t *testing.T) {
	now := time.Now()
	payload := []byte(`{
		"rate_limit": {
			"limit_reached": false,
			"primary_window": { "used_percent": 99.95, "reset_after_seconds": 18000 }
		}
	}`)
	recoverAt, reason, ok := codexQuotaRecoverAt(payload, now)
	if !ok {
		t.Fatalf("expected near-full usage window to keep cooldown active")
	}
	if reason != "codex_5h_limit" {
		t.Fatalf("expected codex_5h_limit, got %q", reason)
	}
	delta := recoverAt.Sub(now)
	if delta < 5*time.Hour-time.Minute || delta > 5*time.Hour+time.Minute {
		t.Fatalf("expected about 5h cooldown, got %v", delta)
	}
}

func TestCodexQuotaRecoverAt_WindowAllowedFalse(t *testing.T) {
	now := time.Now()
	payload := []byte(`{
		"rate_limit": {
			"limit_reached": false,
			"primary_window": {
				"allowed": false,
				"used_percent": 87,
				"reset_after_seconds": 18000
			}
		}
	}`)
	_, reason, ok := codexQuotaRecoverAt(payload, now)
	if !ok {
		t.Fatalf("expected window allowed=false to keep cooldown active")
	}
	if reason != "codex_5h_limit" {
		t.Fatalf("expected codex_5h_limit, got %q", reason)
	}
}

func TestDetectCodexQuotaHasAvailableWindow(t *testing.T) {
	availablePayload := []byte(`{
		"rate_limit": {
			"limit_reached": false,
			"primary_window": { "used_percent": 80, "reset_after_seconds": 18000 }
		}
	}`)
	if !DetectCodexQuotaHasAvailableWindow(availablePayload) {
		t.Fatalf("expected available quota window")
	}

	unknownPayload := []byte(`{
		"rate_limit": {
			"limit_reached": false,
			"primary_window": { "reset_after_seconds": 18000 }
		}
	}`)
	if DetectCodexQuotaHasAvailableWindow(unknownPayload) {
		t.Fatalf("expected unknown quota shape to avoid clearing cooldown")
	}

	nearFullPayload := []byte(`{
		"rate_limit": {
			"limit_reached": false,
			"primary_window": { "used_percent": 99.95, "reset_after_seconds": 18000 }
		}
	}`)
	if DetectCodexQuotaHasAvailableWindow(nearFullPayload) {
		t.Fatalf("expected near-full quota window to be unavailable")
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
