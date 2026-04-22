package util

import (
	"net/http"
	"testing"
)

func TestApplyCustomHeadersFromAttrs_PreservesHostInHeaderAndRequestHost(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "https://example.com", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	ApplyCustomHeadersFromAttrs(req, map[string]string{
		"header:Host":      "api.override.example",
		"header:X-Trace":   "trace-123",
		"ignored_nonheader": "nope",
	})

	if got := req.Host; got != "api.override.example" {
		t.Fatalf("req.Host = %q, want %q", got, "api.override.example")
	}
	if got := req.Header.Get("Host"); got != "api.override.example" {
		t.Fatalf("req.Header[Host] = %q, want %q", got, "api.override.example")
	}
	if got := req.Header.Get("X-Trace"); got != "trace-123" {
		t.Fatalf("req.Header[X-Trace] = %q, want %q", got, "trace-123")
	}
}
