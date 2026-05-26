package codex

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestRefreshTokensWithRetry_NonRetryableOnlyAttemptsOnce(t *testing.T) {
	var calls int32
	auth := &CodexAuth{
		httpClient: &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				atomic.AddInt32(&calls, 1)
				return &http.Response{
					StatusCode: http.StatusBadRequest,
					Body:       io.NopCloser(strings.NewReader(`{"error":"invalid_grant","code":"refresh_token_reused"}`)),
					Header:     make(http.Header),
					Request:    req,
				}, nil
			}),
		},
	}

	_, err := auth.RefreshTokensWithRetry(context.Background(), "dummy_refresh_token", 3)
	if err == nil {
		t.Fatalf("expected error for non-retryable refresh failure")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "refresh_token_reused") {
		t.Fatalf("expected refresh_token_reused in error, got: %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("expected 1 refresh attempt, got %d", got)
	}
}

func TestRefreshTokensExtractsPlanType(t *testing.T) {
	idToken := testCodexJWT("plan@example.com", "acct_123", "plus")
	auth := &CodexAuth{
		httpClient: &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK,
					Body: io.NopCloser(strings.NewReader(`{
						"access_token":"access",
						"refresh_token":"refresh-new",
						"id_token":` + strconvQuote(idToken) + `,
						"token_type":"Bearer",
						"expires_in":3600
					}`)),
					Header:  make(http.Header),
					Request: req,
				}, nil
			}),
		},
	}

	tokenData, err := auth.RefreshTokens(context.Background(), "refresh-old")
	if err != nil {
		t.Fatalf("RefreshTokens returned error: %v", err)
	}
	if tokenData.PlanType != "plus" {
		t.Fatalf("PlanType = %q, want plus", tokenData.PlanType)
	}
	if tokenData.AccountID != "acct_123" {
		t.Fatalf("AccountID = %q, want acct_123", tokenData.AccountID)
	}
}

func testCodexJWT(email, accountID, planType string) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"email":` + strconvQuote(email) + `,"https://api.openai.com/auth":{"chatgpt_account_id":` + strconvQuote(accountID) + `,"chatgpt_plan_type":` + strconvQuote(planType) + `}}`))
	return header + "." + payload + ".signature"
}

func strconvQuote(value string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	return `"` + replacer.Replace(value) + `"`
}
