package auth

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/auth/codex"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/browser"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/util"
	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	log "github.com/sirupsen/logrus"
)

const (
	codexLoginModeMetadataKey             = "codex_login_mode"
	codexLoginModeDevice                  = "device"
	codexDeviceUserCodeURL                = "https://auth.openai.com/api/accounts/deviceauth/usercode"
	codexDeviceTokenURL                   = "https://auth.openai.com/api/accounts/deviceauth/token"
	codexDeviceVerificationURL            = "https://auth.openai.com/codex/device"
	codexDeviceTokenExchangeRedirectURI   = "https://auth.openai.com/deviceauth/callback"
	codexDeviceTimeout                    = 15 * time.Minute
	codexDeviceDefaultPollIntervalSeconds = 5
	codexDeviceOriginator                 = "codex_cli_rs"
	codexDeviceUserAgent                  = "codex_cli_rs/0.124.0 (Mac OS 26.0.1; arm64) Apple_Terminal/464"
	codexDeviceMaxErrorBodyBytes          = 512
)

type codexDeviceUserCodeRequest struct {
	ClientID string `json:"client_id"`
}

type codexDeviceUserCodeResponse struct {
	DeviceAuthID string          `json:"device_auth_id"`
	UserCode     string          `json:"user_code"`
	UserCodeAlt  string          `json:"usercode"`
	Interval     json.RawMessage `json:"interval"`
}

type codexDeviceTokenRequest struct {
	DeviceAuthID string `json:"device_auth_id"`
	UserCode     string `json:"user_code"`
}

type codexDeviceTokenResponse struct {
	AuthorizationCode string `json:"authorization_code"`
	CodeVerifier      string `json:"code_verifier"`
	CodeChallenge     string `json:"code_challenge"`
}

// CodexDeviceFlow contains the data needed to complete a Codex device-code login.
type CodexDeviceFlow struct {
	VerificationURL string        `json:"verification_url"`
	UserCode        string        `json:"user_code"`
	DeviceAuthID    string        `json:"-"`
	PollInterval    time.Duration `json:"-"`
}

func shouldUseCodexDeviceFlow(opts *LoginOptions) bool {
	if opts == nil || opts.Metadata == nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(opts.Metadata[codexLoginModeMetadataKey]), codexLoginModeDevice)
}

var newCodexDeviceHTTPClient = func(cfg *config.Config) *http.Client {
	if cfg == nil {
		return util.NewUTLSHTTPClient(nil)
	}
	return util.NewUTLSHTTPClient(&cfg.SDKConfig)
}

var codexDeviceCloudflareChallengePollInterval = 30 * time.Second

func (a *CodexAuthenticator) loginWithDeviceFlow(ctx context.Context, cfg *config.Config, opts *LoginOptions) (*coreauth.Auth, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	flow, err := a.StartDeviceFlow(ctx, cfg)
	if err != nil {
		return nil, err
	}

	fmt.Println("Starting Codex device authentication...")
	fmt.Printf("Codex device URL: %s\n", flow.VerificationURL)
	fmt.Printf("Codex device code: %s\n", flow.UserCode)

	if !opts.NoBrowser {
		if !browser.IsAvailable() {
			log.Warn("No browser available; please open the device URL manually")
		} else if errOpen := browser.OpenURL(flow.VerificationURL); errOpen != nil {
			log.Warnf("Failed to open browser automatically: %v", errOpen)
		}
	}

	return a.CompleteDeviceFlow(ctx, cfg, flow)
}

// StartDeviceFlow requests a Codex one-time user code without blocking for completion.
func (a *CodexAuthenticator) StartDeviceFlow(ctx context.Context, cfg *config.Config) (*CodexDeviceFlow, error) {
	if cfg == nil {
		return nil, fmt.Errorf("cliproxy auth: configuration is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	httpClient := newCodexDeviceHTTPClient(cfg)
	userCodeResp, err := requestCodexDeviceUserCode(ctx, httpClient)
	if err != nil {
		return nil, err
	}

	deviceCode := strings.TrimSpace(userCodeResp.UserCode)
	if deviceCode == "" {
		deviceCode = strings.TrimSpace(userCodeResp.UserCodeAlt)
	}
	deviceAuthID := strings.TrimSpace(userCodeResp.DeviceAuthID)
	if deviceCode == "" || deviceAuthID == "" {
		return nil, fmt.Errorf("codex device flow did not return required fields")
	}

	return &CodexDeviceFlow{
		VerificationURL: codexDeviceVerificationURL,
		UserCode:        deviceCode,
		DeviceAuthID:    deviceAuthID,
		PollInterval:    parseCodexDevicePollInterval(userCodeResp.Interval),
	}, nil
}

// CompleteDeviceFlow polls until the user authorizes the one-time code and returns an auth record.
func (a *CodexAuthenticator) CompleteDeviceFlow(ctx context.Context, cfg *config.Config, flow *CodexDeviceFlow) (*coreauth.Auth, error) {
	if cfg == nil {
		return nil, fmt.Errorf("cliproxy auth: configuration is required")
	}
	if flow == nil {
		return nil, fmt.Errorf("codex device flow is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	deviceAuthID := strings.TrimSpace(flow.DeviceAuthID)
	deviceCode := strings.TrimSpace(flow.UserCode)
	if deviceAuthID == "" || deviceCode == "" {
		return nil, fmt.Errorf("codex device flow did not return required fields")
	}

	httpClient := newCodexDeviceHTTPClient(cfg)
	pollInterval := flow.PollInterval
	if pollInterval <= 0 {
		pollInterval = time.Duration(codexDeviceDefaultPollIntervalSeconds) * time.Second
	}

	tokenResp, err := pollCodexDeviceToken(ctx, httpClient, deviceAuthID, deviceCode, pollInterval)
	if err != nil {
		return nil, err
	}

	authCode := strings.TrimSpace(tokenResp.AuthorizationCode)
	codeVerifier := strings.TrimSpace(tokenResp.CodeVerifier)
	codeChallenge := strings.TrimSpace(tokenResp.CodeChallenge)
	if authCode == "" || codeVerifier == "" || codeChallenge == "" {
		return nil, fmt.Errorf("codex device flow token response missing required fields")
	}

	authSvc := codex.NewCodexAuth(cfg)
	authBundle, err := authSvc.ExchangeCodeForTokensWithRedirect(
		ctx,
		authCode,
		codexDeviceTokenExchangeRedirectURI,
		&codex.PKCECodes{
			CodeVerifier:  codeVerifier,
			CodeChallenge: codeChallenge,
		},
	)
	if err != nil {
		return nil, codex.NewAuthenticationError(codex.ErrCodeExchangeFailed, err)
	}

	return a.buildAuthRecord(authSvc, authBundle)
}

func requestCodexDeviceUserCode(ctx context.Context, client *http.Client) (*codexDeviceUserCodeResponse, error) {
	body, err := json.Marshal(codexDeviceUserCodeRequest{ClientID: codex.ClientID})
	if err != nil {
		return nil, fmt.Errorf("failed to encode codex device request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, codexDeviceUserCodeURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create codex device request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	setCodexDeviceRequestHeaders(req)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to request codex device code: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read codex device code response: %w", err)
	}

	if !codexDeviceIsSuccessStatus(resp.StatusCode) {
		trimmed := codexDeviceResponseSnippet(respBody)
		if resp.StatusCode == http.StatusNotFound {
			return nil, fmt.Errorf("codex device endpoint is unavailable (status %d)", resp.StatusCode)
		}
		if trimmed == "" {
			trimmed = "empty response body"
		}
		return nil, fmt.Errorf("codex device code request failed with status %d: %s", resp.StatusCode, trimmed)
	}

	var parsed codexDeviceUserCodeResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return nil, fmt.Errorf("failed to decode codex device code response: %w", err)
	}

	return &parsed, nil
}

func pollCodexDeviceToken(ctx context.Context, client *http.Client, deviceAuthID, userCode string, interval time.Duration) (*codexDeviceTokenResponse, error) {
	deadline := time.Now().Add(codexDeviceTimeout)

	for {
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("codex device authentication timed out after 15 minutes")
		}

		body, err := json.Marshal(codexDeviceTokenRequest{
			DeviceAuthID: deviceAuthID,
			UserCode:     userCode,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to encode codex device poll request: %w", err)
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, codexDeviceTokenURL, bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("failed to create codex device poll request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		setCodexDeviceRequestHeaders(req)

		resp, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("failed to poll codex device token: %w", err)
		}

		respBody, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("failed to read codex device poll response: %w", readErr)
		}

		switch {
		case codexDeviceIsSuccessStatus(resp.StatusCode):
			var parsed codexDeviceTokenResponse
			if err := json.Unmarshal(respBody, &parsed); err != nil {
				return nil, fmt.Errorf("failed to decode codex device token response: %w", err)
			}
			return &parsed, nil
		case codexDeviceShouldKeepPolling(resp.StatusCode, respBody):
			sleepFor := codexDevicePollSleepInterval(interval, resp.StatusCode, respBody, deadline)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(sleepFor):
				continue
			}
		default:
			trimmed := codexDeviceResponseSnippet(respBody)
			if trimmed == "" {
				trimmed = "empty response body"
			}
			return nil, fmt.Errorf("codex device token polling failed with status %d: %s", resp.StatusCode, trimmed)
		}
	}
}

func setCodexDeviceRequestHeaders(req *http.Request) {
	req.Header.Set("User-Agent", codexDeviceUserAgent)
	req.Header.Set("Originator", codexDeviceOriginator)
}

func codexDeviceShouldKeepPolling(statusCode int, body []byte) bool {
	if statusCode == http.StatusForbidden || statusCode == http.StatusNotFound {
		return true
	}
	return statusCode == http.StatusTooManyRequests && codexDeviceIsCloudflareChallenge(body)
}

func codexDeviceIsCloudflareChallenge(body []byte) bool {
	text := strings.ToLower(string(body))
	return strings.Contains(text, "challenges.cloudflare.com") ||
		strings.Contains(text, "__cf_chl") ||
		strings.Contains(text, "cf_chl") ||
		(strings.Contains(text, "cloudflare") && strings.Contains(text, "just a moment"))
}

func codexDevicePollSleepInterval(interval time.Duration, statusCode int, body []byte, deadline time.Time) time.Duration {
	if interval <= 0 {
		interval = time.Duration(codexDeviceDefaultPollIntervalSeconds) * time.Second
	}
	if statusCode == http.StatusTooManyRequests &&
		codexDeviceIsCloudflareChallenge(body) &&
		codexDeviceCloudflareChallengePollInterval > 0 &&
		interval < codexDeviceCloudflareChallengePollInterval {
		interval = codexDeviceCloudflareChallengePollInterval
	}
	remaining := time.Until(deadline)
	if remaining <= 0 {
		return 0
	}
	if interval > remaining {
		return remaining
	}
	return interval
}

func codexDeviceResponseSnippet(body []byte) string {
	trimmed := strings.TrimSpace(string(body))
	if len(trimmed) <= codexDeviceMaxErrorBodyBytes {
		return trimmed
	}
	return trimmed[:codexDeviceMaxErrorBodyBytes] + "...(truncated)"
}

func parseCodexDevicePollInterval(raw json.RawMessage) time.Duration {
	defaultInterval := time.Duration(codexDeviceDefaultPollIntervalSeconds) * time.Second
	if len(raw) == 0 {
		return defaultInterval
	}

	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		if seconds, convErr := strconv.Atoi(strings.TrimSpace(asString)); convErr == nil && seconds > 0 {
			return time.Duration(seconds) * time.Second
		}
	}

	var asInt int
	if err := json.Unmarshal(raw, &asInt); err == nil && asInt > 0 {
		return time.Duration(asInt) * time.Second
	}

	return defaultInterval
}

func codexDeviceIsSuccessStatus(code int) bool {
	return code >= 200 && code < 300
}

func (a *CodexAuthenticator) buildAuthRecord(authSvc *codex.CodexAuth, authBundle *codex.CodexAuthBundle) (*coreauth.Auth, error) {
	tokenStorage := authSvc.CreateTokenStorage(authBundle)

	if tokenStorage == nil || tokenStorage.Email == "" {
		return nil, fmt.Errorf("codex token storage missing account information")
	}

	planType := ""
	hashAccountID := ""
	if tokenStorage.IDToken != "" {
		if claims, errParse := codex.ParseJWTToken(tokenStorage.IDToken); errParse == nil && claims != nil {
			planType = strings.TrimSpace(claims.CodexAuthInfo.ChatgptPlanType)
			accountID := strings.TrimSpace(claims.CodexAuthInfo.ChatgptAccountID)
			if accountID != "" {
				digest := sha256.Sum256([]byte(accountID))
				hashAccountID = hex.EncodeToString(digest[:])[:8]
			}
		}
	}

	fileName := codex.CredentialFileName(tokenStorage.Email, planType, hashAccountID, true)
	metadata := map[string]any{
		"email": tokenStorage.Email,
	}
	if tokenStorage.AccountID != "" {
		metadata["account_id"] = tokenStorage.AccountID
	}
	if planType != "" {
		metadata["plan_type"] = planType
	}

	fmt.Println("Codex authentication successful")
	if authBundle.APIKey != "" {
		fmt.Println("Codex API key obtained and stored")
	}

	return &coreauth.Auth{
		ID:       fileName,
		Provider: a.Provider(),
		FileName: fileName,
		Storage:  tokenStorage,
		Metadata: metadata,
	}, nil
}
