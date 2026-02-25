package executor

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	codexauth "github.com/router-for-me/CLIProxyAPI/v6/internal/auth/codex"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/misc"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/thinking"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/util"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v6/sdk/translator"
	log "github.com/sirupsen/logrus"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
	"github.com/tiktoken-go/tokenizer"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const (
	codexClientVersion    = "0.98.0"
	defaultCodexUserAgent = "codex_cli_rs/0.98.0 (Mac OS 26.0.1; arm64) Apple_Terminal/464"
	codexUsageURL         = "https://chatgpt.com/backend-api/wham/usage"
)

var dataTag = []byte("data:")

// CodexExecutor is a stateless executor for Codex (OpenAI Responses API entrypoint).
// If api_key is unavailable on auth, it falls back to legacy via ClientAdapter.
type CodexExecutor struct {
	cfg *config.Config
}

func NewCodexExecutor(cfg *config.Config) *CodexExecutor { return &CodexExecutor{cfg: cfg} }

func (e *CodexExecutor) Identifier() string { return "codex" }

// PrepareRequest injects Codex credentials into the outgoing HTTP request.
func (e *CodexExecutor) PrepareRequest(req *http.Request, auth *cliproxyauth.Auth) error {
	if req == nil {
		return nil
	}
	apiKey, _ := codexCreds(auth)
	if strings.TrimSpace(apiKey) != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	applyReverseProxyHeaders(req, e.cfg, auth, e.Identifier())
	var attrs map[string]string
	if auth != nil {
		attrs = auth.Attributes
	}
	util.ApplyCustomHeadersFromAttrs(req, attrs)
	return nil
}

// HttpRequest injects Codex credentials into the request and executes it.
func (e *CodexExecutor) HttpRequest(ctx context.Context, auth *cliproxyauth.Auth, req *http.Request) (*http.Response, error) {
	if req == nil {
		return nil, fmt.Errorf("codex executor: request is nil")
	}
	if ctx == nil {
		ctx = req.Context()
	}
	httpReq := req.WithContext(ctx)
	if err := e.PrepareRequest(httpReq, auth); err != nil {
		return nil, err
	}
	httpClient := newProxyAwareHTTPClient(ctx, e.cfg, auth, 0)
	return httpClient.Do(httpReq)
}

func (e *CodexExecutor) Execute(ctx context.Context, auth *cliproxyauth.Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (resp cliproxyexecutor.Response, err error) {
	if opts.Alt == "responses/compact" {
		return e.executeCompact(ctx, auth, req, opts)
	}
	baseModel := thinking.ParseSuffix(req.Model).ModelName

	apiKey, baseURL := codexCreds(auth)
	if baseURL == "" {
		baseURL = "https://chatgpt.com/backend-api/codex"
	}

	reporter := newUsageReporter(ctx, e.Identifier(), baseModel, auth)
	defer reporter.trackFailure(ctx, &err)

	from := opts.SourceFormat
	to := sdktranslator.FromString("codex")
	userAgent := codexUserAgent(ctx)
	originalPayload := req.Payload
	if len(opts.OriginalRequest) > 0 {
		originalPayload = opts.OriginalRequest
	}
	originalPayload = misc.InjectCodexUserAgent(originalPayload, userAgent)
	originalTranslated := sdktranslator.TranslateRequest(from, to, baseModel, originalPayload, false)
	body := misc.InjectCodexUserAgent(req.Payload, userAgent)
	body = sdktranslator.TranslateRequest(from, to, baseModel, body, false)
	body = misc.StripCodexUserAgent(body)

	body, err = thinking.ApplyThinking(body, req.Model, from.String(), to.String(), e.Identifier())
	if err != nil {
		return resp, err
	}

	requestedModel := payloadRequestedModel(opts, req.Model)
	body = applyPayloadConfigWithRoot(e.cfg, baseModel, to.String(), "", body, originalTranslated, requestedModel)
	body, _ = sjson.SetBytes(body, "model", baseModel)
	body, _ = sjson.SetBytes(body, "stream", true)
	body, _ = sjson.DeleteBytes(body, "previous_response_id")
	body, _ = sjson.DeleteBytes(body, "prompt_cache_retention")
	body, _ = sjson.DeleteBytes(body, "safety_identifier")
	if !gjson.GetBytes(body, "instructions").Exists() {
		body, _ = sjson.SetBytes(body, "instructions", "")
	}

	originalURL := strings.TrimSuffix(baseURL, "/") + "/responses"
	proxyRoute := resolveReverseProxyRouteForAuth(e.cfg, auth, "codex", originalURL)
	url := proxyRoute.URL
	httpReq, err := e.cacheHelper(ctx, from, url, req, body)
	if err != nil {
		return resp, err
	}
	applyCodexHeaders(httpReq, auth, apiKey, true)
	applyReverseProxyHeaders(httpReq, e.cfg, auth, e.Identifier())
	var authID, authLabel, authType, authValue string
	if auth != nil {
		authID = auth.ID
		authLabel = auth.Label
		authType, authValue = auth.AccountInfo()
	}
	recordAPIRequest(ctx, e.cfg, upstreamRequestLog{
		URL:       url,
		Method:    http.MethodPost,
		Headers:   httpReq.Header.Clone(),
		Body:      body,
		Provider:  e.Identifier(),
		AuthID:    authID,
		AuthLabel: authLabel,
		AuthType:  authType,
		AuthValue: authValue,
	})
	httpClient := newProxyAwareHTTPClient(ctx, e.cfg, auth, 0)
	httpResp, err := httpClient.Do(httpReq)
	if err != nil {
		recordAPIResponseError(ctx, e.cfg, err)
		return resp, err
	}
	recordAPIResponseMetadata(ctx, e.cfg, httpResp.StatusCode, httpResp.Header.Clone())
	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		b, _ := io.ReadAll(httpResp.Body)
		appendAPIResponseChunk(ctx, e.cfg, b)
		logWithRequestID(ctx).Debugf("request error, error status: %d, error message: %s", httpResp.StatusCode, summarizeErrorBody(httpResp.Header.Get("Content-Type"), b))
		if proxyRoute.Proxied && shouldBanReverseProxyOnError(httpResp.StatusCode, string(b)) {
			banReverseProxyTemporarily(proxyRoute.ProxyID, e.Identifier(), httpResp.StatusCode, string(b))
			if errClose := httpResp.Body.Close(); errClose != nil {
				log.Errorf("codex executor: close response body error: %v", errClose)
			}
			fallbackURL := originalURL
			logWithRequestID(ctx).Warnf("codex executor: reverse proxy failed, retrying direct upstream: %s", fallbackURL)
			httpReq, err = e.cacheHelper(ctx, from, fallbackURL, req, body)
			if err != nil {
				return resp, err
			}
			applyCodexHeaders(httpReq, auth, apiKey, true)
			applyReverseProxyHeaders(httpReq, e.cfg, auth, e.Identifier())
			recordAPIRequest(ctx, e.cfg, upstreamRequestLog{
				URL:       fallbackURL,
				Method:    http.MethodPost,
				Headers:   httpReq.Header.Clone(),
				Body:      body,
				Provider:  e.Identifier(),
				AuthID:    authID,
				AuthLabel: authLabel,
				AuthType:  authType,
				AuthValue: authValue,
			})
			httpResp, err = httpClient.Do(httpReq)
			if err != nil {
				recordAPIResponseError(ctx, e.cfg, err)
				return resp, err
			}
			recordAPIResponseMetadata(ctx, e.cfg, httpResp.StatusCode, httpResp.Header.Clone())
			if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
				b, _ := io.ReadAll(httpResp.Body)
				appendAPIResponseChunk(ctx, e.cfg, b)
				logWithRequestID(ctx).Debugf("retry request error, error status: %d, error message: %s", httpResp.StatusCode, summarizeErrorBody(httpResp.Header.Get("Content-Type"), b))
				if errClose := httpResp.Body.Close(); errClose != nil {
					log.Errorf("codex executor: close response body error: %v", errClose)
				}
				err = newCodexStatusErr(ctx, httpClient, auth, httpResp.StatusCode, b, httpResp.Header)
				return resp, err
			}
		} else {
			if errClose := httpResp.Body.Close(); errClose != nil {
				log.Errorf("codex executor: close response body error: %v", errClose)
			}
			err = newCodexStatusErr(ctx, httpClient, auth, httpResp.StatusCode, b, httpResp.Header)
			return resp, err
		}
	}
	defer func() {
		if errClose := httpResp.Body.Close(); errClose != nil {
			log.Errorf("codex executor: close response body error: %v", errClose)
		}
	}()
	data, err := io.ReadAll(httpResp.Body)
	if err != nil {
		recordAPIResponseError(ctx, e.cfg, err)
		return resp, err
	}
	appendAPIResponseChunk(ctx, e.cfg, data)

	lines := bytes.Split(data, []byte("\n"))
	for _, line := range lines {
		if !bytes.HasPrefix(line, dataTag) {
			continue
		}

		line = bytes.TrimSpace(line[5:])
		if gjson.GetBytes(line, "type").String() != "response.completed" {
			continue
		}

		if detail, ok := parseCodexUsage(line); ok {
			reporter.publish(ctx, detail)
		}

		var param any
		out := sdktranslator.TranslateNonStream(ctx, to, from, req.Model, originalPayload, body, line, &param)
		resp = cliproxyexecutor.Response{Payload: []byte(out)}
		return resp, nil
	}
	err = statusErr{code: 408, msg: "stream error: stream disconnected before completion: stream closed before response.completed"}
	return resp, err
}

func (e *CodexExecutor) executeCompact(ctx context.Context, auth *cliproxyauth.Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (resp cliproxyexecutor.Response, err error) {
	baseModel := thinking.ParseSuffix(req.Model).ModelName

	apiKey, baseURL := codexCreds(auth)
	if baseURL == "" {
		baseURL = "https://chatgpt.com/backend-api/codex"
	}

	reporter := newUsageReporter(ctx, e.Identifier(), baseModel, auth)
	defer reporter.trackFailure(ctx, &err)

	from := opts.SourceFormat
	to := sdktranslator.FromString("openai-response")
	originalPayload := req.Payload
	if len(opts.OriginalRequest) > 0 {
		originalPayload = opts.OriginalRequest
	}
	originalTranslated := sdktranslator.TranslateRequest(from, to, baseModel, originalPayload, false)
	body := sdktranslator.TranslateRequest(from, to, baseModel, req.Payload, false)

	body, err = thinking.ApplyThinking(body, req.Model, from.String(), to.String(), e.Identifier())
	if err != nil {
		return resp, err
	}

	requestedModel := payloadRequestedModel(opts, req.Model)
	body = applyPayloadConfigWithRoot(e.cfg, baseModel, to.String(), "", body, originalTranslated, requestedModel)
	body, _ = sjson.SetBytes(body, "model", baseModel)
	body, _ = sjson.DeleteBytes(body, "stream")

	url := strings.TrimSuffix(baseURL, "/") + "/responses/compact"
	httpReq, err := e.cacheHelper(ctx, from, url, req, body)
	if err != nil {
		return resp, err
	}
	applyCodexHeaders(httpReq, auth, apiKey, false)
	applyReverseProxyHeaders(httpReq, e.cfg, auth, e.Identifier())
	var authID, authLabel, authType, authValue string
	if auth != nil {
		authID = auth.ID
		authLabel = auth.Label
		authType, authValue = auth.AccountInfo()
	}
	recordAPIRequest(ctx, e.cfg, upstreamRequestLog{
		URL:       url,
		Method:    http.MethodPost,
		Headers:   httpReq.Header.Clone(),
		Body:      body,
		Provider:  e.Identifier(),
		AuthID:    authID,
		AuthLabel: authLabel,
		AuthType:  authType,
		AuthValue: authValue,
	})
	httpClient := newProxyAwareHTTPClient(ctx, e.cfg, auth, 0)
	httpResp, err := httpClient.Do(httpReq)
	if err != nil {
		recordAPIResponseError(ctx, e.cfg, err)
		return resp, err
	}
	defer func() {
		if errClose := httpResp.Body.Close(); errClose != nil {
			log.Errorf("codex executor: close response body error: %v", errClose)
		}
	}()
	recordAPIResponseMetadata(ctx, e.cfg, httpResp.StatusCode, httpResp.Header.Clone())
	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		b, _ := io.ReadAll(httpResp.Body)
		appendAPIResponseChunk(ctx, e.cfg, b)
		logWithRequestID(ctx).Debugf("request error, error status: %d, error message: %s", httpResp.StatusCode, summarizeErrorBody(httpResp.Header.Get("Content-Type"), b))
		err = newCodexStatusErr(ctx, httpClient, auth, httpResp.StatusCode, b, httpResp.Header)
		return resp, err
	}
	data, err := io.ReadAll(httpResp.Body)
	if err != nil {
		recordAPIResponseError(ctx, e.cfg, err)
		return resp, err
	}
	appendAPIResponseChunk(ctx, e.cfg, data)
	reporter.publish(ctx, parseOpenAIUsage(data))
	reporter.ensurePublished(ctx)
	var param any
	out := sdktranslator.TranslateNonStream(ctx, to, from, req.Model, originalPayload, body, data, &param)
	resp = cliproxyexecutor.Response{Payload: []byte(out)}
	return resp, nil
}

func (e *CodexExecutor) ExecuteStream(ctx context.Context, auth *cliproxyauth.Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (stream <-chan cliproxyexecutor.StreamChunk, err error) {
	if opts.Alt == "responses/compact" {
		return nil, statusErr{code: http.StatusBadRequest, msg: "streaming not supported for /responses/compact"}
	}
	baseModel := thinking.ParseSuffix(req.Model).ModelName

	apiKey, baseURL := codexCreds(auth)
	if baseURL == "" {
		baseURL = "https://chatgpt.com/backend-api/codex"
	}

	reporter := newUsageReporter(ctx, e.Identifier(), baseModel, auth)
	defer reporter.trackFailure(ctx, &err)

	from := opts.SourceFormat
	to := sdktranslator.FromString("codex")
	userAgent := codexUserAgent(ctx)
	originalPayload := req.Payload
	if len(opts.OriginalRequest) > 0 {
		originalPayload = opts.OriginalRequest
	}
	originalPayload = misc.InjectCodexUserAgent(originalPayload, userAgent)
	originalTranslated := sdktranslator.TranslateRequest(from, to, baseModel, originalPayload, true)
	body := misc.InjectCodexUserAgent(req.Payload, userAgent)
	body = sdktranslator.TranslateRequest(from, to, baseModel, body, true)
	body = misc.StripCodexUserAgent(body)

	body, err = thinking.ApplyThinking(body, req.Model, from.String(), to.String(), e.Identifier())
	if err != nil {
		return nil, err
	}

	requestedModel := payloadRequestedModel(opts, req.Model)
	body = applyPayloadConfigWithRoot(e.cfg, baseModel, to.String(), "", body, originalTranslated, requestedModel)
	body, _ = sjson.DeleteBytes(body, "previous_response_id")
	body, _ = sjson.DeleteBytes(body, "prompt_cache_retention")
	body, _ = sjson.DeleteBytes(body, "safety_identifier")
	body, _ = sjson.SetBytes(body, "model", baseModel)
	if !gjson.GetBytes(body, "instructions").Exists() {
		body, _ = sjson.SetBytes(body, "instructions", "")
	}

	originalURL := strings.TrimSuffix(baseURL, "/") + "/responses"
	proxyRoute := resolveReverseProxyRouteForAuth(e.cfg, auth, "codex", originalURL)
	url := proxyRoute.URL
	httpReq, err := e.cacheHelper(ctx, from, url, req, body)
	if err != nil {
		return nil, err
	}
	applyCodexHeaders(httpReq, auth, apiKey, true)
	applyReverseProxyHeaders(httpReq, e.cfg, auth, e.Identifier())
	var authID, authLabel, authType, authValue string
	if auth != nil {
		authID = auth.ID
		authLabel = auth.Label
		authType, authValue = auth.AccountInfo()
	}
	recordAPIRequest(ctx, e.cfg, upstreamRequestLog{
		URL:       url,
		Method:    http.MethodPost,
		Headers:   httpReq.Header.Clone(),
		Body:      body,
		Provider:  e.Identifier(),
		AuthID:    authID,
		AuthLabel: authLabel,
		AuthType:  authType,
		AuthValue: authValue,
	})

	httpClient := newProxyAwareHTTPClient(ctx, e.cfg, auth, 0)
	httpResp, err := httpClient.Do(httpReq)
	if err != nil {
		recordAPIResponseError(ctx, e.cfg, err)
		return nil, err
	}
	recordAPIResponseMetadata(ctx, e.cfg, httpResp.StatusCode, httpResp.Header.Clone())
	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		data, readErr := io.ReadAll(httpResp.Body)
		if errClose := httpResp.Body.Close(); errClose != nil {
			log.Errorf("codex executor: close response body error: %v", errClose)
		}
		if readErr != nil {
			recordAPIResponseError(ctx, e.cfg, readErr)
			return nil, readErr
		}
		appendAPIResponseChunk(ctx, e.cfg, data)
		logWithRequestID(ctx).Debugf("request error, error status: %d, error message: %s", httpResp.StatusCode, summarizeErrorBody(httpResp.Header.Get("Content-Type"), data))
		if proxyRoute.Proxied && shouldBanReverseProxyOnError(httpResp.StatusCode, string(data)) {
			banReverseProxyTemporarily(proxyRoute.ProxyID, e.Identifier(), httpResp.StatusCode, string(data))
			fallbackURL := originalURL
			logWithRequestID(ctx).Warnf("codex executor: reverse proxy failed, retrying direct upstream: %s", fallbackURL)
			httpReq, err = e.cacheHelper(ctx, from, fallbackURL, req, body)
			if err != nil {
				return nil, err
			}
			applyCodexHeaders(httpReq, auth, apiKey, true)
			applyReverseProxyHeaders(httpReq, e.cfg, auth, e.Identifier())
			recordAPIRequest(ctx, e.cfg, upstreamRequestLog{
				URL:       fallbackURL,
				Method:    http.MethodPost,
				Headers:   httpReq.Header.Clone(),
				Body:      body,
				Provider:  e.Identifier(),
				AuthID:    authID,
				AuthLabel: authLabel,
				AuthType:  authType,
				AuthValue: authValue,
			})
			httpResp, err = httpClient.Do(httpReq)
			if err != nil {
				recordAPIResponseError(ctx, e.cfg, err)
				return nil, err
			}
			recordAPIResponseMetadata(ctx, e.cfg, httpResp.StatusCode, httpResp.Header.Clone())
			if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
				data, readErr = io.ReadAll(httpResp.Body)
				if errClose := httpResp.Body.Close(); errClose != nil {
					log.Errorf("codex executor: close response body error: %v", errClose)
				}
				if readErr != nil {
					recordAPIResponseError(ctx, e.cfg, readErr)
					return nil, readErr
				}
				appendAPIResponseChunk(ctx, e.cfg, data)
				logWithRequestID(ctx).Debugf("retry request error, error status: %d, error message: %s", httpResp.StatusCode, summarizeErrorBody(httpResp.Header.Get("Content-Type"), data))
				err = newCodexStatusErr(ctx, httpClient, auth, httpResp.StatusCode, data, httpResp.Header)
				return nil, err
			}
		} else {
			err = newCodexStatusErr(ctx, httpClient, auth, httpResp.StatusCode, data, httpResp.Header)
			return nil, err
		}
	}
	out := make(chan cliproxyexecutor.StreamChunk)
	stream = out
	go func() {
		defer close(out)
		defer func() {
			if errClose := httpResp.Body.Close(); errClose != nil {
				log.Errorf("codex executor: close response body error: %v", errClose)
			}
		}()
		scanner := bufio.NewScanner(httpResp.Body)
		scanner.Buffer(nil, 52_428_800) // 50MB
		var param any
		for scanner.Scan() {
			line := scanner.Bytes()
			appendAPIResponseChunk(ctx, e.cfg, line)

			if bytes.HasPrefix(line, dataTag) {
				data := bytes.TrimSpace(line[5:])
				if gjson.GetBytes(data, "type").String() == "response.completed" {
					if detail, ok := parseCodexUsage(data); ok {
						reporter.publish(ctx, detail)
					}
				}
			}

			chunks := sdktranslator.TranslateStream(ctx, to, from, req.Model, originalPayload, body, line, &param)
			for i := range chunks {
				out <- cliproxyexecutor.StreamChunk{Payload: []byte(chunks[i])}
			}
		}
		if errScan := scanner.Err(); errScan != nil {
			recordAPIResponseError(ctx, e.cfg, errScan)
			reporter.publishFailure(ctx)
			out <- cliproxyexecutor.StreamChunk{Err: errScan}
		}
	}()
	return stream, nil
}

func (e *CodexExecutor) CountTokens(ctx context.Context, auth *cliproxyauth.Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	baseModel := thinking.ParseSuffix(req.Model).ModelName

	from := opts.SourceFormat
	to := sdktranslator.FromString("codex")
	userAgent := codexUserAgent(ctx)
	body := misc.InjectCodexUserAgent(req.Payload, userAgent)
	body = sdktranslator.TranslateRequest(from, to, baseModel, body, false)
	body = misc.StripCodexUserAgent(body)

	body, err := thinking.ApplyThinking(body, req.Model, from.String(), to.String(), e.Identifier())
	if err != nil {
		return cliproxyexecutor.Response{}, err
	}

	body, _ = sjson.SetBytes(body, "model", baseModel)
	body, _ = sjson.DeleteBytes(body, "previous_response_id")
	body, _ = sjson.DeleteBytes(body, "prompt_cache_retention")
	body, _ = sjson.DeleteBytes(body, "safety_identifier")
	body, _ = sjson.SetBytes(body, "stream", false)
	if !gjson.GetBytes(body, "instructions").Exists() {
		body, _ = sjson.SetBytes(body, "instructions", "")
	}

	enc, err := tokenizerForCodexModel(baseModel)
	if err != nil {
		return cliproxyexecutor.Response{}, fmt.Errorf("codex executor: tokenizer init failed: %w", err)
	}

	count, err := countCodexInputTokens(enc, body)
	if err != nil {
		return cliproxyexecutor.Response{}, fmt.Errorf("codex executor: token counting failed: %w", err)
	}

	usageJSON := fmt.Sprintf(`{"response":{"usage":{"input_tokens":%d,"output_tokens":0,"total_tokens":%d}}}`, count, count)
	translated := sdktranslator.TranslateTokenCount(ctx, to, from, count, []byte(usageJSON))
	return cliproxyexecutor.Response{Payload: []byte(translated)}, nil
}

type codexQuotaCooldownHint struct {
	retryAfter time.Duration
	reason     string
}

func newCodexStatusErr(ctx context.Context, client *http.Client, auth *cliproxyauth.Auth, statusCode int, body []byte, headers http.Header) statusErr {
	sErr := statusErr{code: statusCode, msg: string(body)}
	if statusCode != http.StatusTooManyRequests {
		return sErr
	}
	if retryAfter := parseRetryAfterHeader(headers); retryAfter != nil {
		sErr.retryAfter = retryAfter
	}
	if hint, ok := fetchCodexQuotaCooldownHint(ctx, client, auth); ok {
		if hint.retryAfter > 0 {
			retryAfter := hint.retryAfter
			sErr.retryAfter = &retryAfter
		}
		sErr.quotaReason = hint.reason
	}
	return sErr
}

func parseRetryAfterHeader(headers http.Header) *time.Duration {
	if len(headers) == 0 {
		return nil
	}
	raw := strings.TrimSpace(headers.Get("Retry-After"))
	if raw == "" {
		return nil
	}
	if seconds, err := strconv.Atoi(raw); err == nil && seconds > 0 {
		retryAfter := time.Duration(seconds) * time.Second
		return &retryAfter
	}
	retryAt, err := http.ParseTime(raw)
	if err != nil {
		return nil
	}
	retryAfter := time.Until(retryAt)
	if retryAfter <= 0 {
		return nil
	}
	return &retryAfter
}

func fetchCodexQuotaCooldownHint(ctx context.Context, client *http.Client, auth *cliproxyauth.Auth) (codexQuotaCooldownHint, bool) {
	var hint codexQuotaCooldownHint
	if client == nil || auth == nil {
		return hint, false
	}
	token, _ := codexCreds(auth)
	token = strings.TrimSpace(token)
	if token == "" {
		return hint, false
	}
	accountID := ""
	if auth.Metadata != nil {
		if value, ok := auth.Metadata["account_id"].(string); ok {
			accountID = strings.TrimSpace(value)
		}
	}

	reqCtx := ctx
	if reqCtx == nil {
		reqCtx = context.Background()
	}
	reqCtx, cancel := context.WithTimeout(reqCtx, 3*time.Second)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(reqCtx, http.MethodGet, codexUsageURL, nil)
	if err != nil {
		return hint, false
	}
	httpReq.Header.Set("Authorization", "Bearer "+token)
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("User-Agent", defaultCodexUserAgent)
	if accountID != "" {
		httpReq.Header.Set("Chatgpt-Account-Id", accountID)
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		return hint, false
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return hint, false
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return hint, false
	}
	if len(body) == 0 {
		return hint, false
	}
	now := time.Now()
	retryAt, reason, ok := codexQuotaRecoverAt(body, now)
	if !ok || retryAt.IsZero() || !retryAt.After(now) {
		return hint, false
	}
	hint.retryAfter = retryAt.Sub(now)
	hint.reason = reason
	return hint, true
}

func codexQuotaRecoverAt(payload []byte, now time.Time) (time.Time, string, bool) {
	root := gjson.ParseBytes(payload)
	candidates := make([]struct {
		resetAt time.Time
		reason  string
	}, 0, 4)

	addByRateLimit := func(rateLimit gjson.Result, reasonPrimary, reasonSecondary string) {
		if !rateLimit.Exists() || rateLimit.Type == gjson.Null {
			return
		}
		parentLimited := rateLimit.Get("limit_reached").Bool() || rateLimit.Get("limitReached").Bool()
		if allowed := rateLimit.Get("allowed"); allowed.Exists() && !allowed.Bool() {
			parentLimited = true
		}
		appendCodexWindowCandidate(
			&candidates,
			rateLimit.Get("primary_window"),
			parentLimited,
			resolveCodexWindowReason(rateLimit.Get("primary_window"), reasonPrimary, reasonSecondary),
			now,
		)
		appendCodexWindowCandidate(
			&candidates,
			rateLimit.Get("primaryWindow"),
			parentLimited,
			resolveCodexWindowReason(rateLimit.Get("primaryWindow"), reasonPrimary, reasonSecondary),
			now,
		)
		appendCodexWindowCandidate(&candidates, rateLimit.Get("secondary_window"), parentLimited, reasonSecondary, now)
		appendCodexWindowCandidate(&candidates, rateLimit.Get("secondaryWindow"), parentLimited, reasonSecondary, now)
	}

	addByRateLimit(root.Get("rate_limit"), "codex_5h_limit", "codex_weekly_limit")
	addByRateLimit(root.Get("rateLimit"), "codex_5h_limit", "codex_weekly_limit")
	addByRateLimit(root.Get("code_review_rate_limit"), "codex_code_review_limit", "codex_code_review_limit")
	addByRateLimit(root.Get("codeReviewRateLimit"), "codex_code_review_limit", "codex_code_review_limit")

	if len(candidates) == 0 {
		return time.Time{}, "", false
	}
	earliest := candidates[0]
	for i := 1; i < len(candidates); i++ {
		if candidates[i].resetAt.Before(earliest.resetAt) {
			earliest = candidates[i]
		}
	}
	return earliest.resetAt, earliest.reason, true
}

func resolveCodexWindowReason(window gjson.Result, reasonPrimary, reasonSecondary string) string {
	if reasonPrimary == reasonSecondary {
		return reasonPrimary
	}
	if !window.Exists() || window.Type == gjson.Null {
		return reasonPrimary
	}
	if limitWindowSeconds, ok := gjsonToFloat(window.Get("limit_window_seconds")); ok && limitWindowSeconds >= 24*60*60 {
		return reasonSecondary
	}
	if limitWindowSeconds, ok := gjsonToFloat(window.Get("limitWindowSeconds")); ok && limitWindowSeconds >= 24*60*60 {
		return reasonSecondary
	}
	if resetAfterSeconds, ok := gjsonToFloat(window.Get("reset_after_seconds")); ok && resetAfterSeconds >= 24*60*60 {
		return reasonSecondary
	}
	if resetAfterSeconds, ok := gjsonToFloat(window.Get("resetAfterSeconds")); ok && resetAfterSeconds >= 24*60*60 {
		return reasonSecondary
	}
	return reasonPrimary
}

func appendCodexWindowCandidate(candidates *[]struct {
	resetAt time.Time
	reason  string
}, window gjson.Result, parentLimited bool, reason string, now time.Time) {
	if !window.Exists() || window.Type == gjson.Null {
		return
	}
	windowLimited := parentLimited
	if usedPercent, ok := gjsonToFloat(window.Get("used_percent")); ok && usedPercent >= 100 {
		windowLimited = true
	}
	if usedPercent, ok := gjsonToFloat(window.Get("usedPercent")); ok && usedPercent >= 100 {
		windowLimited = true
	}
	if !windowLimited {
		return
	}
	resetAt, ok := codexWindowRecoverAt(window, now)
	if !ok || !resetAt.After(now) {
		return
	}
	*candidates = append(*candidates, struct {
		resetAt time.Time
		reason  string
	}{
		resetAt: resetAt,
		reason:  reason,
	})
}

func codexWindowRecoverAt(window gjson.Result, now time.Time) (time.Time, bool) {
	if resetAt, ok := gjsonToFloat(window.Get("reset_at")); ok && resetAt > 0 {
		return time.Unix(int64(resetAt), 0), true
	}
	if resetAt, ok := gjsonToFloat(window.Get("resetAt")); ok && resetAt > 0 {
		return time.Unix(int64(resetAt), 0), true
	}
	if resetAfter, ok := gjsonToFloat(window.Get("reset_after_seconds")); ok && resetAfter > 0 {
		return now.Add(time.Duration(resetAfter * float64(time.Second))), true
	}
	if resetAfter, ok := gjsonToFloat(window.Get("resetAfterSeconds")); ok && resetAfter > 0 {
		return now.Add(time.Duration(resetAfter * float64(time.Second))), true
	}
	return time.Time{}, false
}

func gjsonToFloat(result gjson.Result) (float64, bool) {
	if !result.Exists() {
		return 0, false
	}
	switch result.Type {
	case gjson.Number:
		return result.Float(), true
	case gjson.String:
		trimmed := strings.TrimSpace(result.String())
		if trimmed == "" {
			return 0, false
		}
		parsed, err := strconv.ParseFloat(trimmed, 64)
		if err != nil {
			return 0, false
		}
		return parsed, true
	default:
		trimmed := strings.TrimSpace(result.String())
		if trimmed == "" {
			return 0, false
		}
		parsed, err := strconv.ParseFloat(trimmed, 64)
		if err != nil {
			return 0, false
		}
		return parsed, true
	}
}

func tokenizerForCodexModel(model string) (tokenizer.Codec, error) {
	sanitized := strings.ToLower(strings.TrimSpace(model))
	switch {
	case sanitized == "":
		return tokenizer.Get(tokenizer.Cl100kBase)
	case strings.HasPrefix(sanitized, "gpt-5"):
		return tokenizer.ForModel(tokenizer.GPT5)
	case strings.HasPrefix(sanitized, "gpt-4.1"):
		return tokenizer.ForModel(tokenizer.GPT41)
	case strings.HasPrefix(sanitized, "gpt-4o"):
		return tokenizer.ForModel(tokenizer.GPT4o)
	case strings.HasPrefix(sanitized, "gpt-4"):
		return tokenizer.ForModel(tokenizer.GPT4)
	case strings.HasPrefix(sanitized, "gpt-3.5"), strings.HasPrefix(sanitized, "gpt-3"):
		return tokenizer.ForModel(tokenizer.GPT35Turbo)
	default:
		return tokenizer.Get(tokenizer.Cl100kBase)
	}
}

func countCodexInputTokens(enc tokenizer.Codec, body []byte) (int64, error) {
	if enc == nil {
		return 0, fmt.Errorf("encoder is nil")
	}
	if len(body) == 0 {
		return 0, nil
	}

	root := gjson.ParseBytes(body)
	var segments []string

	if inst := strings.TrimSpace(root.Get("instructions").String()); inst != "" {
		segments = append(segments, inst)
	}

	inputItems := root.Get("input")
	if inputItems.IsArray() {
		arr := inputItems.Array()
		for i := range arr {
			item := arr[i]
			switch item.Get("type").String() {
			case "message":
				content := item.Get("content")
				if content.IsArray() {
					parts := content.Array()
					for j := range parts {
						part := parts[j]
						if text := strings.TrimSpace(part.Get("text").String()); text != "" {
							segments = append(segments, text)
						}
					}
				}
			case "function_call":
				if name := strings.TrimSpace(item.Get("name").String()); name != "" {
					segments = append(segments, name)
				}
				if args := strings.TrimSpace(item.Get("arguments").String()); args != "" {
					segments = append(segments, args)
				}
			case "function_call_output":
				if out := strings.TrimSpace(item.Get("output").String()); out != "" {
					segments = append(segments, out)
				}
			default:
				if text := strings.TrimSpace(item.Get("text").String()); text != "" {
					segments = append(segments, text)
				}
			}
		}
	}

	tools := root.Get("tools")
	if tools.IsArray() {
		tarr := tools.Array()
		for i := range tarr {
			tool := tarr[i]
			if name := strings.TrimSpace(tool.Get("name").String()); name != "" {
				segments = append(segments, name)
			}
			if desc := strings.TrimSpace(tool.Get("description").String()); desc != "" {
				segments = append(segments, desc)
			}
			if params := tool.Get("parameters"); params.Exists() {
				val := params.Raw
				if params.Type == gjson.String {
					val = params.String()
				}
				if trimmed := strings.TrimSpace(val); trimmed != "" {
					segments = append(segments, trimmed)
				}
			}
		}
	}

	textFormat := root.Get("text.format")
	if textFormat.Exists() {
		if name := strings.TrimSpace(textFormat.Get("name").String()); name != "" {
			segments = append(segments, name)
		}
		if schema := textFormat.Get("schema"); schema.Exists() {
			val := schema.Raw
			if schema.Type == gjson.String {
				val = schema.String()
			}
			if trimmed := strings.TrimSpace(val); trimmed != "" {
				segments = append(segments, trimmed)
			}
		}
	}

	text := strings.Join(segments, "\n")
	if text == "" {
		return 0, nil
	}

	count, err := enc.Count(text)
	if err != nil {
		return 0, err
	}
	return int64(count), nil
}

func (e *CodexExecutor) Refresh(ctx context.Context, auth *cliproxyauth.Auth) (*cliproxyauth.Auth, error) {
	log.Debugf("codex executor: refresh called")
	if auth == nil {
		return nil, statusErr{code: 500, msg: "codex executor: auth is nil"}
	}
	var refreshToken string
	if auth.Metadata != nil {
		if v, ok := auth.Metadata["refresh_token"].(string); ok && v != "" {
			refreshToken = v
		}
	}
	if refreshToken == "" {
		return auth, nil
	}
	svc := codexauth.NewCodexAuth(e.cfg)
	td, err := svc.RefreshTokensWithRetry(ctx, refreshToken, 3)
	if err != nil {
		return nil, err
	}
	if auth.Metadata == nil {
		auth.Metadata = make(map[string]any)
	}
	auth.Metadata["id_token"] = td.IDToken
	auth.Metadata["access_token"] = td.AccessToken
	if td.RefreshToken != "" {
		auth.Metadata["refresh_token"] = td.RefreshToken
	}
	if td.AccountID != "" {
		auth.Metadata["account_id"] = td.AccountID
	}
	auth.Metadata["email"] = td.Email
	// Use unified key in files
	auth.Metadata["expired"] = td.Expire
	auth.Metadata["type"] = "codex"
	now := time.Now().Format(time.RFC3339)
	auth.Metadata["last_refresh"] = now
	return auth, nil
}

func (e *CodexExecutor) cacheHelper(ctx context.Context, from sdktranslator.Format, url string, req cliproxyexecutor.Request, rawJSON []byte) (*http.Request, error) {
	var cache codexCache
	if from == "claude" {
		userIDResult := gjson.GetBytes(req.Payload, "metadata.user_id")
		if userIDResult.Exists() {
			key := fmt.Sprintf("%s-%s", req.Model, userIDResult.String())
			var ok bool
			if cache, ok = getCodexCache(key); !ok {
				cache = codexCache{
					ID:     uuid.New().String(),
					Expire: time.Now().Add(1 * time.Hour),
				}
				setCodexCache(key, cache)
			}
		}
	} else if from == "openai-response" {
		promptCacheKey := gjson.GetBytes(req.Payload, "prompt_cache_key")
		if promptCacheKey.Exists() {
			cache.ID = promptCacheKey.String()
		}
	}

	if cache.ID != "" {
		rawJSON, _ = sjson.SetBytes(rawJSON, "prompt_cache_key", cache.ID)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(rawJSON))
	if err != nil {
		return nil, err
	}
	if cache.ID != "" {
		httpReq.Header.Set("Conversation_id", cache.ID)
		httpReq.Header.Set("Session_id", cache.ID)
	}
	return httpReq, nil
}

func applyCodexHeaders(r *http.Request, auth *cliproxyauth.Auth, token string, stream bool) {
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+token)

	var ginHeaders http.Header
	if ginCtx, ok := r.Context().Value("gin").(*gin.Context); ok && ginCtx != nil && ginCtx.Request != nil {
		ginHeaders = ginCtx.Request.Header
	}

	misc.EnsureHeader(r.Header, ginHeaders, "Version", codexClientVersion)
	misc.EnsureHeader(r.Header, ginHeaders, "Openai-Beta", "responses=experimental")
	misc.EnsureHeader(r.Header, ginHeaders, "Session_id", uuid.NewString())
	misc.EnsureHeader(r.Header, ginHeaders, "User-Agent", defaultCodexUserAgent)

	if stream {
		r.Header.Set("Accept", "text/event-stream")
	} else {
		r.Header.Set("Accept", "application/json")
	}
	r.Header.Set("Connection", "Keep-Alive")

	isAPIKey := false
	if auth != nil && auth.Attributes != nil {
		if v := strings.TrimSpace(auth.Attributes["api_key"]); v != "" {
			isAPIKey = true
		}
	}
	if !isAPIKey {
		r.Header.Set("Originator", "codex_cli_rs")
		if auth != nil && auth.Metadata != nil {
			if accountID, ok := auth.Metadata["account_id"].(string); ok {
				r.Header.Set("Chatgpt-Account-Id", accountID)
			}
		}
	}
	var attrs map[string]string
	if auth != nil {
		attrs = auth.Attributes
	}
	util.ApplyCustomHeadersFromAttrs(r, attrs)
}

func codexUserAgent(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if ginCtx, ok := ctx.Value("gin").(*gin.Context); ok && ginCtx != nil && ginCtx.Request != nil {
		return strings.TrimSpace(ginCtx.Request.UserAgent())
	}
	return ""
}

func codexCreds(a *cliproxyauth.Auth) (apiKey, baseURL string) {
	if a == nil {
		return "", ""
	}
	if a.Attributes != nil {
		apiKey = a.Attributes["api_key"]
		baseURL = a.Attributes["base_url"]
	}
	if apiKey == "" && a.Metadata != nil {
		if v, ok := a.Metadata["access_token"].(string); ok {
			apiKey = v
		}
	}
	return
}

func (e *CodexExecutor) resolveCodexConfig(auth *cliproxyauth.Auth) *config.CodexKey {
	if auth == nil || e.cfg == nil {
		return nil
	}
	var attrKey, attrBase string
	if auth.Attributes != nil {
		attrKey = strings.TrimSpace(auth.Attributes["api_key"])
		attrBase = strings.TrimSpace(auth.Attributes["base_url"])
	}
	for i := range e.cfg.CodexKey {
		entry := &e.cfg.CodexKey[i]
		cfgKey := strings.TrimSpace(entry.APIKey)
		cfgBase := strings.TrimSpace(entry.BaseURL)
		if attrKey != "" && attrBase != "" {
			if strings.EqualFold(cfgKey, attrKey) && strings.EqualFold(cfgBase, attrBase) {
				return entry
			}
			continue
		}
		if attrKey != "" && strings.EqualFold(cfgKey, attrKey) {
			if cfgBase == "" || strings.EqualFold(cfgBase, attrBase) {
				return entry
			}
		}
		if attrKey == "" && attrBase != "" && strings.EqualFold(cfgBase, attrBase) {
			return entry
		}
	}
	if attrKey != "" {
		for i := range e.cfg.CodexKey {
			entry := &e.cfg.CodexKey[i]
			if strings.EqualFold(strings.TrimSpace(entry.APIKey), attrKey) {
				return entry
			}
		}
	}
	return nil
}
