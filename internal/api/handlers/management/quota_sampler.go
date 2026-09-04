package management

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	coreusage "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/usage"
)

var codexQuotaSamplerURL = "https://chatgpt.com/backend-api/wham/usage"

const codexQuotaSamplerUserAgent = "codex_cli_rs/0.153.4"

func (h *Handler) probeCodexQuotaUsage(ctx context.Context, request coreusage.QuotaProbeRequest) error {
	if h == nil || h.authManager == nil {
		return fmt.Errorf("quota probe auth manager unavailable")
	}
	auth := h.authByIndex(request.AuthIndex)
	if auth == nil && strings.TrimSpace(request.AuthID) != "" {
		if candidate, ok := h.authManager.GetByID(strings.TrimSpace(request.AuthID)); ok {
			auth = candidate
		}
	}
	if auth == nil || !strings.EqualFold(strings.TrimSpace(auth.Provider), "codex") {
		return fmt.Errorf("quota probe auth not found")
	}
	if strings.TrimSpace(request.AuthIndex) != "" && strings.TrimSpace(auth.EnsureIndex()) != strings.TrimSpace(request.AuthIndex) {
		return fmt.Errorf("quota probe auth index changed")
	}
	token, err := h.resolveTokenForAuth(ctx, auth)
	if err != nil {
		return fmt.Errorf("quota probe token unavailable")
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return fmt.Errorf("quota probe token unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	probeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	requestURL, err := url.Parse(codexQuotaSamplerURL)
	if err != nil {
		return fmt.Errorf("quota probe URL invalid")
	}
	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, requestURL.String(), nil)
	if err != nil {
		return fmt.Errorf("quota probe request failed")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", codexQuotaSamplerUserAgent)
	if accountID := resolveCodexAccountID(auth); accountID != "" {
		req.Header.Set("ChatGPT-Account-ID", accountID)
	}
	client := &http.Client{Transport: h.apiCallTransport(auth), Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("quota probe request failed")
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("quota probe response failed")
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("quota probe upstream status %d", resp.StatusCode)
	}
	if !json.Valid(body) {
		return fmt.Errorf("quota probe response invalid")
	}
	authIndex := auth.EnsureIndex()
	if added := coreusage.RecordQuotaSnapshot(auth.ID, authIndex, maskedAuditAccount(auth), time.Now().UTC(), body); added == 0 {
		return fmt.Errorf("quota probe response unrecognized")
	}
	h.syncQuotaProbeFromAPICall(probeCtx, auth, requestURL, resp.StatusCode, body)
	return nil
}
