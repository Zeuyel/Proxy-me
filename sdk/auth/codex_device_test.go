package auth

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
)

func TestStartDeviceFlowParsesResponse(t *testing.T) {
	origClient := newCodexDeviceHTTPClient
	t.Cleanup(func() { newCodexDeviceHTTPClient = origClient })

	newCodexDeviceHTTPClient = func(*config.Config) *http.Client {
		return &http.Client{
			Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
				if req.URL.Path != "/api/accounts/deviceauth/usercode" {
					t.Fatalf("unexpected path: %s", req.URL.Path)
				}
				body := `{"device_auth_id":"device-123","user_code":"ABCD-EFGH","interval":"7"}`
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(body)),
					Header:     make(http.Header),
					Request:    req,
				}, nil
			}),
		}
	}

	auth := &CodexAuthenticator{}
	flow, err := auth.StartDeviceFlow(context.Background(), &config.Config{})
	if err != nil {
		t.Fatalf("StartDeviceFlow returned error: %v", err)
	}
	if got, want := flow.VerificationURL, codexDeviceVerificationURL; got != want {
		t.Fatalf("VerificationURL = %q, want %q", got, want)
	}
	if got, want := flow.UserCode, "ABCD-EFGH"; got != want {
		t.Fatalf("UserCode = %q, want %q", got, want)
	}
	if got, want := flow.DeviceAuthID, "device-123"; got != want {
		t.Fatalf("DeviceAuthID = %q, want %q", got, want)
	}
	if got, want := flow.PollInterval, 7*time.Second; got != want {
		t.Fatalf("PollInterval = %s, want %s", got, want)
	}
}

func TestPollCodexDeviceTokenRetriesPendingStatus(t *testing.T) {
	var callCount int
	client := &http.Client{
		Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			callCount++
			if req.URL.Path != "/api/accounts/deviceauth/token" {
				t.Fatalf("unexpected path: %s", req.URL.Path)
			}
			if callCount == 1 {
				return &http.Response{
					StatusCode: http.StatusForbidden,
					Body:       io.NopCloser(strings.NewReader(`{}`)),
					Header:     make(http.Header),
					Request:    req,
				}, nil
			}
			payload := `{"authorization_code":"auth-code","code_verifier":"verifier","code_challenge":"challenge"}`
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(payload)),
				Header:     make(http.Header),
				Request:    req,
			}, nil
		}),
	}

	resp, err := pollCodexDeviceToken(context.Background(), client, "device-123", "ABCD-EFGH", 1*time.Nanosecond)
	if err != nil {
		t.Fatalf("pollCodexDeviceToken returned error: %v", err)
	}
	if callCount != 2 {
		t.Fatalf("callCount = %d, want 2", callCount)
	}
	if resp.AuthorizationCode != "auth-code" || resp.CodeVerifier != "verifier" || resp.CodeChallenge != "challenge" {
		t.Fatalf("unexpected token response: %+v", resp)
	}
}

func TestPollCodexDeviceTokenRetriesCloudflareChallenge(t *testing.T) {
	origPollInterval := codexDeviceCloudflareChallengePollInterval
	t.Cleanup(func() { codexDeviceCloudflareChallengePollInterval = origPollInterval })
	codexDeviceCloudflareChallengePollInterval = 1 * time.Nanosecond

	var callCount int
	client := &http.Client{
		Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			callCount++
			if req.Header.Get("User-Agent") != codexDeviceUserAgent {
				t.Fatalf("User-Agent = %q, want %q", req.Header.Get("User-Agent"), codexDeviceUserAgent)
			}
			if req.Header.Get("Originator") != codexDeviceOriginator {
				t.Fatalf("Originator = %q, want %q", req.Header.Get("Originator"), codexDeviceOriginator)
			}
			if callCount == 1 {
				body := `<!DOCTYPE html><title>Just a moment...</title><script src="https://challenges.cloudflare.com/test"></script>`
				return &http.Response{
					StatusCode: http.StatusTooManyRequests,
					Body:       io.NopCloser(strings.NewReader(body)),
					Header:     make(http.Header),
					Request:    req,
				}, nil
			}
			payload := `{"authorization_code":"auth-code","code_verifier":"verifier","code_challenge":"challenge"}`
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(payload)),
				Header:     make(http.Header),
				Request:    req,
			}, nil
		}),
	}

	resp, err := pollCodexDeviceToken(context.Background(), client, "device-123", "ABCD-EFGH", 1*time.Nanosecond)
	if err != nil {
		t.Fatalf("pollCodexDeviceToken returned error: %v", err)
	}
	if callCount != 2 {
		t.Fatalf("callCount = %d, want 2", callCount)
	}
	if resp.AuthorizationCode != "auth-code" || resp.CodeVerifier != "verifier" || resp.CodeChallenge != "challenge" {
		t.Fatalf("unexpected token response: %+v", resp)
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
