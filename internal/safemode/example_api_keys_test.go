package safemode

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
)

func TestExampleAPIKeys(t *testing.T) {
	got := ExampleAPIKeys([]string{" your-api-key-1 ", "real-key", "your-api-key-1", "your-api-key-3"})
	want := []string{"your-api-key-1", "your-api-key-3"}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got[%d] = %q, want %q; all=%#v", i, got[i], want[i], got)
		}
	}
	if HasExampleAPIKeys([]string{"real-key"}) {
		t.Fatal("HasExampleAPIKeys returned true for real key")
	}
}

func TestWarningServerURL(t *testing.T) {
	got := WarningServerURL(&config.Config{Host: "::1", Port: 8443, TLS: config.TLSConfig{Enable: true}})
	if got != "https://[::1]:8443/" {
		t.Fatalf("WarningServerURL = %q", got)
	}
}

func TestWarningHandler(t *testing.T) {
	handler := NewExampleAPIKeyWarningHandler("/tmp/config.yaml", []string{"your-api-key-1"})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Example API key detected") || !strings.Contains(body, "your-api-key-1") {
		t.Fatalf("warning body missing expected content: %s", body)
	}

	req = httptest.NewRequest(http.MethodPost, "/", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST status = %d, want 405", rec.Code)
	}
}
