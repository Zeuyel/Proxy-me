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
	"github.com/router-for-me/CLIProxyAPI/v6/internal/registry"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/thinking"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/util"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v6/sdk/translator"
	log "github.com/sirupsen/logrus"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
	"github.com/tiktoken-go/tokenizer"

	"github.com/google/uuid"
)

const (
	codexClientVersion     = "0.144.0"
	defaultCodexUserAgent  = "codex-tui/0.144.0 (Mac OS 26.5.1; arm64) iTerm.app/3.6.11 (codex-tui; 0.144.0)"
	codexUsageURL          = "https://chatgpt.com/backend-api/wham/usage"
	defaultCodexOriginator = "codex-tui"
	codexResponsesBeta     = "responses_websockets=2026-02-06"
	codexWebOrigin         = "https://chatgpt.com"
	codexCodexReferer      = "https://chatgpt.com/codex"
	// The usage endpoint may report 99.x when the UI already rounds remaining
	// quota to 0%. Treat that as exhausted so a quota refresh does not clear
	// cooldown for effectively drained auth files.
	codexQuotaUsedPercentExhaustedThreshold = 99.5
)

var dataTag = []byte("data:")

const codexCapacityMessage = "selected model is at capacity"

func isCodexCapacityPayload(payload []byte) bool {
	return strings.Contains(strings.ToLower(string(payload)), codexCapacityMessage)
}

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
	resetCodexClientHeaders(req)
	token, _ := codexCreds(auth)
	var attrs map[string]string
	if auth != nil {
		attrs = auth.Attributes
	}
	util.ApplyCustomHeadersFromAttrs(req, attrs)
	if strings.TrimSpace(token) != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	misc.EnsureHeader(req.Header, nil, "Content-Type", "application/json")
	misc.EnsureHeader(req.Header, nil, "Version", codexClientVersion)
	misc.EnsureHeader(req.Header, nil, "Openai-Beta", codexResponsesBeta)
	misc.EnsureHeader(req.Header, nil, "Session_id", uuid.NewString())
	misc.EnsureHeader(req.Header, nil, "User-Agent", defaultCodexUserAgent)
	misc.EnsureHeader(req.Header, nil, "X-Client-Request-Id", req.Header.Get("Session_id"))
	ensureCodexWindowHeader(req.Header)
	if !codexUsesAPIKey(auth) {
		misc.EnsureHeader(req.Header, nil, "Originator", defaultCodexOriginator)
		misc.EnsureHeader(req.Header, nil, "Origin", codexWebOrigin)
		misc.EnsureHeader(req.Header, nil, "Referer", codexCodexReferer)
	}
	applyReverseProxyHeaders(req, e.cfg, auth, e.Identifier())
	resetCodexProtectedHeaders(req.Header, auth, token)
	deleteDeprecatedCodexConversationHeader(req.Header)
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
	body, _ = sjson.DeleteBytes(body, "prompt_cache_retention")
	body, _ = sjson.DeleteBytes(body, "safety_identifier")
	if !gjson.GetBytes(body, "instructions").Exists() {
		body, _ = sjson.SetBytes(body, "instructions", "")
	}
	body = normalizeCodexClientMetadata(injectCodexClientMetadata(body, auth))

	originalURL := strings.TrimSuffix(baseURL, "/") + "/responses"
	proxyRoute := resolveReverseProxyRouteForAuth(e.cfg, auth, "codex", originalURL)
	url := proxyRoute.URL
	httpReq, upstreamBody, identityState, err := e.cacheHelper(ctx, from, url, auth, req, opts, originalPayload, body)
	if err != nil {
		return resp, err
	}
	applyCodexHeaders(httpReq, auth, apiKey, true)
	applyModelHeaderOverrides(httpReq.Header, baseModel)
	applyCodexIdentityConfuseHeaders(httpReq.Header, &identityState)
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
		Body:      upstreamBody,
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
		b = applyCodexIdentityConfuseResponsePayload(b, identityState)
		appendAPIResponseChunk(ctx, e.cfg, b)
		logWithRequestID(ctx).Debugf("request error, error status: %d, error message: %s", httpResp.StatusCode, summarizeErrorBody(httpResp.Header.Get("Content-Type"), b))
		if proxyRoute.Proxied && shouldBanReverseProxyOnError(httpResp.StatusCode, string(b)) {
			banReverseProxyTemporarily(proxyRoute.ProxyID, e.Identifier(), httpResp.StatusCode, string(b))
			if errClose := httpResp.Body.Close(); errClose != nil {
				log.Errorf("codex executor: close response body error: %v", errClose)
			}
			fallbackURL := originalURL
			logWithRequestID(ctx).Warnf("codex executor: reverse proxy failed, retrying direct upstream: %s", fallbackURL)
			httpReq, upstreamBody, identityState, err = e.cacheHelper(ctx, from, fallbackURL, auth, req, opts, originalPayload, body)
			if err != nil {
				return resp, err
			}
			applyCodexHeaders(httpReq, auth, apiKey, true)
			applyModelHeaderOverrides(httpReq.Header, baseModel)
			applyCodexIdentityConfuseHeaders(httpReq.Header, &identityState)
			applyReverseProxyHeaders(httpReq, e.cfg, auth, e.Identifier())
			recordAPIRequest(ctx, e.cfg, upstreamRequestLog{
				URL:       fallbackURL,
				Method:    http.MethodPost,
				Headers:   httpReq.Header.Clone(),
				Body:      upstreamBody,
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
				b = applyCodexIdentityConfuseResponsePayload(b, identityState)
				appendAPIResponseChunk(ctx, e.cfg, b)
				logWithRequestID(ctx).Debugf("retry request error, error status: %d, error message: %s", httpResp.StatusCode, summarizeErrorBody(httpResp.Header.Get("Content-Type"), b))
				if errClose := httpResp.Body.Close(); errClose != nil {
					log.Errorf("codex executor: close response body error: %v", errClose)
				}
				clientBody := applyCodexClientResponsePayload(b, identityState)
				err = newCodexStatusErr(ctx, httpClient, auth, httpResp.StatusCode, clientBody, httpResp.Header)
				return resp, err
			}
		} else {
			if errClose := httpResp.Body.Close(); errClose != nil {
				log.Errorf("codex executor: close response body error: %v", errClose)
			}
			clientBody := applyCodexClientResponsePayload(b, identityState)
			err = newCodexStatusErr(ctx, httpClient, auth, httpResp.StatusCode, clientBody, httpResp.Header)
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
	upstreamData := applyCodexIdentityConfuseResponsePayload(data, identityState)
	appendAPIResponseChunk(ctx, e.cfg, upstreamData)

	lines := bytes.Split(upstreamData, []byte("\n"))
	for _, line := range lines {
		if !bytes.HasPrefix(line, dataTag) {
			continue
		}

		line = bytes.TrimSpace(line[5:])
		clientLine := applyCodexClientResponsePayload(line, identityState)
		if isCodexCapacityPayload(clientLine) {
			err = newCodexStatusErr(ctx, httpClient, auth, http.StatusTooManyRequests, clientLine, httpResp.Header)
			return resp, err
		}
		if gjson.GetBytes(line, "type").String() != "response.completed" {
			continue
		}

		if detail, ok := parseCodexUsage(line); ok {
			reporter.publish(ctx, detail)
		}

		var param any
		out := sdktranslator.TranslateNonStream(ctx, to, from, req.Model, originalPayload, body, clientLine, &param)
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
	body = normalizeCodexClientMetadata(injectCodexClientMetadata(body, auth))

	url := strings.TrimSuffix(baseURL, "/") + "/responses/compact"
	httpReq, upstreamBody, identityState, err := e.cacheHelper(ctx, from, url, auth, req, opts, originalPayload, body)
	if err != nil {
		return resp, err
	}
	applyCodexHeaders(httpReq, auth, apiKey, false)
	applyModelHeaderOverrides(httpReq.Header, baseModel)
	applyCodexIdentityConfuseHeaders(httpReq.Header, &identityState)
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
		Body:      upstreamBody,
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
		b = applyCodexIdentityConfuseResponsePayload(b, identityState)
		appendAPIResponseChunk(ctx, e.cfg, b)
		logWithRequestID(ctx).Debugf("request error, error status: %d, error message: %s", httpResp.StatusCode, summarizeErrorBody(httpResp.Header.Get("Content-Type"), b))
		clientBody := applyCodexClientResponsePayload(b, identityState)
		err = newCodexStatusErr(ctx, httpClient, auth, httpResp.StatusCode, clientBody, httpResp.Header)
		return resp, err
	}
	data, err := io.ReadAll(httpResp.Body)
	if err != nil {
		recordAPIResponseError(ctx, e.cfg, err)
		return resp, err
	}
	upstreamData := applyCodexIdentityConfuseResponsePayload(data, identityState)
	appendAPIResponseChunk(ctx, e.cfg, upstreamData)
	reporter.publish(ctx, parseOpenAIUsage(upstreamData))
	reporter.ensurePublished(ctx)
	var param any
	clientData := applyCodexClientResponsePayload(upstreamData, identityState)
	if isCodexCapacityPayload(clientData) {
		err = newCodexStatusErr(ctx, httpClient, auth, http.StatusTooManyRequests, clientData, httpResp.Header)
		return resp, err
	}
	out := sdktranslator.TranslateNonStream(ctx, to, from, req.Model, originalPayload, body, clientData, &param)
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
	body, _ = sjson.DeleteBytes(body, "prompt_cache_retention")
	body, _ = sjson.DeleteBytes(body, "safety_identifier")
	body, _ = sjson.SetBytes(body, "model", baseModel)
	if !gjson.GetBytes(body, "instructions").Exists() {
		body, _ = sjson.SetBytes(body, "instructions", "")
	}
	body = normalizeCodexClientMetadata(injectCodexClientMetadata(body, auth))

	originalURL := strings.TrimSuffix(baseURL, "/") + "/responses"
	proxyRoute := resolveReverseProxyRouteForAuth(e.cfg, auth, "codex", originalURL)
	url := proxyRoute.URL
	httpReq, upstreamBody, identityState, err := e.cacheHelper(ctx, from, url, auth, req, opts, originalPayload, body)
	if err != nil {
		return nil, err
	}
	applyCodexHeaders(httpReq, auth, apiKey, true)
	applyModelHeaderOverrides(httpReq.Header, baseModel)
	applyCodexIdentityConfuseHeaders(httpReq.Header, &identityState)
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
		Body:      upstreamBody,
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
		data = applyCodexIdentityConfuseResponsePayload(data, identityState)
		appendAPIResponseChunk(ctx, e.cfg, data)
		logWithRequestID(ctx).Debugf("request error, error status: %d, error message: %s", httpResp.StatusCode, summarizeErrorBody(httpResp.Header.Get("Content-Type"), data))
		if proxyRoute.Proxied && shouldBanReverseProxyOnError(httpResp.StatusCode, string(data)) {
			banReverseProxyTemporarily(proxyRoute.ProxyID, e.Identifier(), httpResp.StatusCode, string(data))
			fallbackURL := originalURL
			logWithRequestID(ctx).Warnf("codex executor: reverse proxy failed, retrying direct upstream: %s", fallbackURL)
			httpReq, upstreamBody, identityState, err = e.cacheHelper(ctx, from, fallbackURL, auth, req, opts, originalPayload, body)
			if err != nil {
				return nil, err
			}
			applyCodexHeaders(httpReq, auth, apiKey, true)
			applyModelHeaderOverrides(httpReq.Header, baseModel)
			applyCodexIdentityConfuseHeaders(httpReq.Header, &identityState)
			applyReverseProxyHeaders(httpReq, e.cfg, auth, e.Identifier())
			recordAPIRequest(ctx, e.cfg, upstreamRequestLog{
				URL:       fallbackURL,
				Method:    http.MethodPost,
				Headers:   httpReq.Header.Clone(),
				Body:      upstreamBody,
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
				data = applyCodexIdentityConfuseResponsePayload(data, identityState)
				appendAPIResponseChunk(ctx, e.cfg, data)
				logWithRequestID(ctx).Debugf("retry request error, error status: %d, error message: %s", httpResp.StatusCode, summarizeErrorBody(httpResp.Header.Get("Content-Type"), data))
				clientBody := applyCodexClientResponsePayload(data, identityState)
				err = newCodexStatusErr(ctx, httpClient, auth, httpResp.StatusCode, clientBody, httpResp.Header)
				return nil, err
			}
		} else {
			clientBody := applyCodexClientResponsePayload(data, identityState)
			err = newCodexStatusErr(ctx, httpClient, auth, httpResp.StatusCode, clientBody, httpResp.Header)
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
			line := applyCodexIdentityConfuseResponsePayload(scanner.Bytes(), identityState)
			appendAPIResponseChunk(ctx, e.cfg, line)

			if bytes.HasPrefix(line, dataTag) {
				data := bytes.TrimSpace(line[5:])
				if gjson.GetBytes(data, "type").String() == "response.completed" {
					if detail, ok := parseCodexUsage(data); ok {
						reporter.publish(ctx, detail)
					}
				}
			}

			clientLine := applyCodexClientResponsePayload(line, identityState)
			if isCodexCapacityPayload(clientLine) {
				capacityErr := newCodexStatusErr(ctx, httpClient, auth, http.StatusTooManyRequests, clientLine, httpResp.Header)
				out <- cliproxyexecutor.StreamChunk{Err: capacityErr}
				return
			}
			chunks := sdktranslator.TranslateStream(ctx, to, from, req.Model, originalPayload, body, clientLine, &param)
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
	sErr := statusErr{code: statusCode, msg: string(body), capacity: isCodexCapacityPayload(body)}
	if sErr.capacity {
		sErr.code = http.StatusTooManyRequests
		if retryAfter := parseRetryAfterHeader(headers); retryAfter != nil {
			sErr.retryAfter = retryAfter
		}
		return sErr
	}
	if statusCode != http.StatusTooManyRequests {
		return sErr
	}
	if retryAfter := parseRetryAfterHeader(headers); retryAfter != nil {
		sErr.retryAfter = retryAfter
	}
	if retryAfter := parseCodexRetryAfter(statusCode, body, time.Now()); retryAfter != nil && sErr.retryAfter == nil {
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

// DetectCodexQuotaRecoverAt parses a Codex usage payload and returns the earliest
// future quota recovery time when a usage window is currently exhausted.
func DetectCodexQuotaRecoverAt(payload []byte, now time.Time) (time.Time, string, bool) {
	return codexQuotaRecoverAt(payload, now)
}

// DetectCodexQuotaHasAvailableWindow returns true only when a usage payload
// contains a clear positive quota signal. Unknown payload shapes should not be
// treated as available because that would incorrectly clear cooldown state.
func DetectCodexQuotaHasAvailableWindow(payload []byte) bool {
	root := gjson.ParseBytes(payload)
	return codexRateLimitHasAvailableWindow(root.Get("rate_limit")) ||
		codexRateLimitHasAvailableWindow(root.Get("rateLimit")) ||
		codexRateLimitHasAvailableWindow(root.Get("code_review_rate_limit")) ||
		codexRateLimitHasAvailableWindow(root.Get("codeReviewRateLimit"))
}

func codexRateLimitHasAvailableWindow(rateLimit gjson.Result) bool {
	if !rateLimit.Exists() || rateLimit.Type == gjson.Null {
		return false
	}
	if rateLimit.Get("limit_reached").Bool() || rateLimit.Get("limitReached").Bool() {
		return false
	}
	if allowed := rateLimit.Get("allowed"); allowed.Exists() && !allowed.Bool() {
		return false
	}
	for _, key := range []string{"primary_window", "primaryWindow", "secondary_window", "secondaryWindow"} {
		window := rateLimit.Get(key)
		if codexWindowHasAvailableQuota(window) {
			return true
		}
	}
	return false
}

func codexWindowHasAvailableQuota(window gjson.Result) bool {
	if !window.Exists() || window.Type == gjson.Null {
		return false
	}
	if codexWindowLimited(window, false) {
		return false
	}
	for _, key := range []string{"used_percent", "usedPercent"} {
		if usedPercent, ok := gjsonToFloat(window.Get(key)); ok {
			return usedPercent < codexQuotaUsedPercentExhaustedThreshold
		}
	}
	for _, key := range []string{"remaining_percent", "remainingPercent"} {
		if remainingPercent, ok := gjsonToFloat(window.Get(key)); ok {
			return remainingPercent > 0
		}
	}
	for _, key := range []string{"remaining_fraction", "remainingFraction", "remaining"} {
		if remainingFraction, ok := gjsonToFloat(window.Get(key)); ok {
			return remainingFraction > 0
		}
	}
	for _, key := range []string{"remaining_amount", "remainingAmount"} {
		if remainingAmount, ok := gjsonToFloat(window.Get(key)); ok {
			return remainingAmount > 0
		}
	}
	return false
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
	if !codexWindowLimited(window, parentLimited) {
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

func codexWindowLimited(window gjson.Result, parentLimited bool) bool {
	if !window.Exists() || window.Type == gjson.Null {
		return false
	}
	if parentLimited {
		return true
	}
	if window.Get("limit_reached").Bool() || window.Get("limitReached").Bool() {
		return true
	}
	if allowed := window.Get("allowed"); allowed.Exists() && !allowed.Bool() {
		return true
	}
	for _, key := range []string{"used_percent", "usedPercent"} {
		if usedPercent, ok := gjsonToFloat(window.Get(key)); ok && usedPercent >= codexQuotaUsedPercentExhaustedThreshold {
			return true
		}
	}
	for _, key := range []string{"remaining_percent", "remainingPercent"} {
		if remainingPercent, ok := gjsonToFloat(window.Get(key)); ok && remainingPercent <= 0 {
			return true
		}
	}
	for _, key := range []string{"remaining_fraction", "remainingFraction", "remaining"} {
		if remainingFraction, ok := gjsonToFloat(window.Get(key)); ok && remainingFraction <= 0 {
			return true
		}
	}
	for _, key := range []string{"remaining_amount", "remainingAmount"} {
		if remainingAmount, ok := gjsonToFloat(window.Get(key)); ok && remainingAmount <= 0 {
			return true
		}
	}
	return false
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
	if planType := strings.TrimSpace(td.PlanType); planType != "" {
		auth.Metadata["plan_type"] = planType
	} else if planType := codexPlanTypeFromToken(td.IDToken); planType != "" {
		auth.Metadata["plan_type"] = planType
	}
	// Use unified key in files
	auth.Metadata["expired"] = td.Expire
	auth.Metadata["type"] = "codex"
	now := time.Now().Format(time.RFC3339)
	auth.Metadata["last_refresh"] = now
	return auth, nil
}

func codexPlanTypeFromToken(token string) string {
	token = strings.TrimSpace(token)
	if token == "" {
		return ""
	}
	claims, err := codexauth.ParseJWTToken(token)
	if err != nil || claims == nil {
		return ""
	}
	return strings.TrimSpace(claims.CodexAuthInfo.ChatgptPlanType)
}

type codexIdentityConfuseState struct {
	enabled                bool
	authID                 string
	originalPromptCacheKey string
	promptCacheKey         string
	turnIDs                []codexIdentityReplacement
	threadIsolation        codexThreadIsolationState
}

type codexIdentityReplacement struct {
	original string
	confused string
}

type codexInternalSessionContextKey struct{}

func (e *CodexExecutor) cacheHelper(ctx context.Context, from sdktranslator.Format, url string, auth *cliproxyauth.Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options, userPayload []byte, rawJSON []byte) (*http.Request, []byte, codexIdentityConfuseState, error) {
	var cache codexCache
	if from == "claude" {
		userIDResult := gjson.GetBytes(req.Payload, "metadata.user_id")
		if userIDResult.Exists() {
			key := strings.Join([]string{
				"claude",
				strings.TrimSpace(req.Model),
				codexAuthScope(auth, req.Model, codexClientTenant(opts)),
				codexClientTenant(opts),
				userIDResult.String(),
			}, "\x00")
			var ok bool
			if cache, ok = getCodexCache(key); !ok {
				cache = codexCache{
					ID:     uuid.New().String(),
					Expire: time.Now().Add(1 * time.Hour),
				}
				setCodexCache(key, cache)
			}
		}
	}
	if cache.ID == "" {
		cache.ID = extractCodexConversationIDForRequest(req, opts, rawJSON)
	}

	threadScope := codexAuthScope(auth, req.Model, codexClientTenant(opts))
	canonicalThreadID := ""
	if threadScope != "" && cache.ID != "" {
		if binding, ok := resolveCodexResponseBinding(cache.ID, threadScope); ok {
			canonicalThreadID = binding.canonicalPromptCacheKey
			cache.ID = binding.clientSessionID
		} else if sessionID, _, ok := cliproxyauth.ResolveSessionAlias(cache.ID); ok {
			cache.ID = sessionID
		}
	}
	threadIsolation := newCodexThreadIsolationStateWithCanonical(auth, req.Model, cache.ID, opts, threadScope, canonicalThreadID)
	if threadIsolation.enabled {
		rawJSON = applyCodexThreadIsolationBody(rawJSON, threadIsolation)
		cache.ID = threadIsolation.canonicalPromptCacheKey
	} else if auth != nil {
		threadIsolation.rejectProviderThread = true
		cache.ID = ""
		rawJSON = stripCodexThreadIdentifiers(rawJSON)
	}
	if cache.ID != "" {
		rawJSON, _ = sjson.SetBytes(rawJSON, "prompt_cache_key", cache.ID)
	}
	var identityState codexIdentityConfuseState
	rawJSON, identityState = applyCodexIdentityConfuseBody(e.cfg, auth, userPayload, rawJSON)
	identityState.threadIsolation = threadIsolation
	if identityState.promptCacheKey != "" {
		cache.ID = identityState.promptCacheKey
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(rawJSON))
	if err != nil {
		return nil, nil, codexIdentityConfuseState{}, err
	}
	httpReq = httpReq.WithContext(context.WithValue(httpReq.Context(), codexInternalSessionContextKey{}, true))
	if cache.ID != "" {
		httpReq.Header.Set("Session_id", cache.ID)
	}
	return httpReq, rawJSON, identityState, nil
}

func applyCodexIdentityConfuseBody(cfg *config.Config, auth *cliproxyauth.Auth, userPayload []byte, rawJSON []byte) ([]byte, codexIdentityConfuseState) {
	if !codexIdentityConfuseEnabled(cfg) || auth == nil || strings.TrimSpace(auth.ID) == "" || len(rawJSON) == 0 {
		return rawJSON, codexIdentityConfuseState{}
	}

	state := codexIdentityConfuseState{enabled: true, authID: strings.TrimSpace(auth.ID)}
	promptCacheKey := strings.TrimSpace(gjson.GetBytes(userPayload, "prompt_cache_key").String())
	if promptCacheKey == "" {
		promptCacheKey = strings.TrimSpace(gjson.GetBytes(rawJSON, "prompt_cache_key").String())
	}
	if promptCacheKey != "" {
		state.originalPromptCacheKey = promptCacheKey
		state.promptCacheKey = codexIdentityConfuseUUID(auth.ID, "prompt-cache", promptCacheKey)
		rawJSON, _ = sjson.SetBytes(rawJSON, "prompt_cache_key", state.promptCacheKey)
	}

	installationID := strings.TrimSpace(gjson.GetBytes(userPayload, "client_metadata.x-codex-installation-id").String())
	if installationID == "" {
		installationID = strings.TrimSpace(gjson.GetBytes(rawJSON, "client_metadata.x-codex-installation-id").String())
	}
	if installationID != "" {
		rawJSON, _ = sjson.SetBytes(rawJSON, "client_metadata.x-codex-installation-id", codexIdentityConfuseUUID(auth.ID, "installation", installationID))
	}

	if turnMetadata := strings.TrimSpace(gjson.GetBytes(rawJSON, "client_metadata.x-codex-turn-metadata").String()); turnMetadata != "" {
		rawJSON, _ = sjson.SetBytes(rawJSON, "client_metadata.x-codex-turn-metadata", applyCodexTurnMetadataIdentityConfuse(turnMetadata, &state))
	}
	if state.promptCacheKey != "" {
		if windowID := strings.TrimSpace(gjson.GetBytes(rawJSON, "client_metadata.x-codex-window-id").String()); windowID != "" {
			rawJSON, _ = sjson.SetBytes(rawJSON, "client_metadata.x-codex-window-id", state.promptCacheKey+":0")
		}
	}

	return rawJSON, state
}

func applyCodexIdentityConfuseHeaders(headers http.Header, state *codexIdentityConfuseState) {
	if headers == nil {
		return
	}
	defer deleteDeprecatedCodexConversationHeader(headers)
	if state == nil {
		return
	}
	applyCodexThreadIsolationHeaders(headers, state.threadIsolation)
	if !state.enabled {
		return
	}

	if rawTurnMetadata := strings.TrimSpace(headers.Get("X-Codex-Turn-Metadata")); rawTurnMetadata != "" {
		headers.Set("X-Codex-Turn-Metadata", applyCodexTurnMetadataIdentityConfuse(rawTurnMetadata, state))
	}
	if state.promptCacheKey == "" {
		return
	}

	headers.Set("Session_id", state.promptCacheKey)
	headers.Set("X-Client-Request-Id", state.promptCacheKey)
	headers.Set("Thread-Id", state.promptCacheKey)
	headers.Set("X-Codex-Window-Id", state.promptCacheKey+":0")
}

func applyCodexTurnMetadataIdentityConfuse(rawTurnMetadata string, state *codexIdentityConfuseState) string {
	updatedTurnMetadata := rawTurnMetadata
	if state == nil || !state.enabled {
		return updatedTurnMetadata
	}
	if state.promptCacheKey != "" && gjson.Get(rawTurnMetadata, "prompt_cache_key").Exists() {
		updatedTurnMetadata, _ = sjson.Set(updatedTurnMetadata, "prompt_cache_key", state.promptCacheKey)
	} else if state.promptCacheKey != "" && state.originalPromptCacheKey != "" {
		updatedTurnMetadata = strings.ReplaceAll(updatedTurnMetadata, state.originalPromptCacheKey, state.promptCacheKey)
	}
	if turnID := strings.TrimSpace(gjson.Get(rawTurnMetadata, "turn_id").String()); turnID != "" {
		updatedTurnMetadata, _ = sjson.Set(updatedTurnMetadata, "turn_id", state.confuseTurnID(turnID))
	}
	if state.promptCacheKey != "" && gjson.Get(rawTurnMetadata, "window_id").Exists() {
		updatedTurnMetadata, _ = sjson.Set(updatedTurnMetadata, "window_id", state.promptCacheKey+":0")
	}
	return updatedTurnMetadata
}

func applyCodexIdentityConfuseResponsePayload(payload []byte, state codexIdentityConfuseState) []byte {
	payload = replaceCodexIdentityResponsePayload(payload, state.originalPromptCacheKey, state.promptCacheKey)
	for _, turnID := range state.turnIDs {
		payload = replaceCodexIdentityResponsePayload(payload, turnID.original, turnID.confused)
	}
	return payload
}

func applyCodexIdentityExposeResponsePayload(payload []byte, state codexIdentityConfuseState) []byte {
	payload = replaceCodexIdentityResponsePayload(payload, state.promptCacheKey, state.originalPromptCacheKey)
	for _, turnID := range state.turnIDs {
		payload = replaceCodexIdentityResponsePayload(payload, turnID.confused, turnID.original)
	}
	return payload
}

func (state *codexIdentityConfuseState) confuseTurnID(turnID string) string {
	turnID = strings.TrimSpace(turnID)
	if state == nil || !state.enabled || strings.TrimSpace(state.authID) == "" || turnID == "" {
		return turnID
	}
	for _, replacement := range state.turnIDs {
		if replacement.original == turnID || replacement.confused == turnID {
			return replacement.confused
		}
	}
	confusedTurnID := codexIdentityConfuseUUID(state.authID, "turn", turnID)
	state.turnIDs = append(state.turnIDs, codexIdentityReplacement{original: turnID, confused: confusedTurnID})
	return confusedTurnID
}

func replaceCodexIdentityResponsePayload(payload []byte, from string, to string) []byte {
	from = strings.TrimSpace(from)
	to = strings.TrimSpace(to)
	if len(payload) == 0 || from == "" || to == "" || from == to || !bytes.Contains(payload, []byte(from)) {
		return payload
	}
	return bytes.ReplaceAll(payload, []byte(from), []byte(to))
}

func codexIdentityConfuseEnabled(cfg *config.Config) bool {
	if cfg == nil || !cfg.Codex.IdentityConfuse {
		return false
	}
	strategy := strings.ToLower(strings.TrimSpace(cfg.Routing.Strategy))
	return strategy == "fill-first" || strategy == "fillfirst" || strategy == "ff" || strategy == "session" || cfg.Routing.Session.Enabled
}

func codexIdentityConfuseUUID(authID string, kind string, value string) string {
	name := strings.Join([]string{"cli-proxy-api", "codex", "identity-confuse", kind, strings.TrimSpace(authID), strings.TrimSpace(value)}, ":")
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(name)).String()
}

func deleteDeprecatedCodexConversationHeader(headers http.Header) {
	if headers == nil {
		return
	}
	headers.Del("Conversation_id")
	headers.Del("Conversation-Id")
}

const (
	codexConversationPrefix    = "codex_prev_"
	codexConversationMaxLength = 256
	codexConversationMinLength = 21
)

func sanitizeCodexConversationID(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || len(trimmed) < codexConversationMinLength || len(trimmed) > codexConversationMaxLength {
		return ""
	}
	if strings.ContainsAny(trimmed, "\r\n") {
		return ""
	}
	return trimmed
}

func codexConversationIDFromJSON(rawJSON []byte) string {
	if len(rawJSON) == 0 {
		return ""
	}
	if value := sanitizeCodexConversationID(gjson.GetBytes(rawJSON, "prompt_cache_key").String()); value != "" {
		return value
	}
	if value := sanitizeCodexConversationID(gjson.GetBytes(rawJSON, "metadata.session_id").String()); value != "" {
		return value
	}
	if value := sanitizeCodexConversationID(gjson.GetBytes(rawJSON, "previous_response_id").String()); value != "" {
		if prefixed := codexConversationPrefix + value; len(prefixed) <= codexConversationMaxLength {
			return prefixed
		}
	}
	return ""
}

func extractCodexConversationIDForRequest(req cliproxyexecutor.Request, opts cliproxyexecutor.Options, rawJSON []byte) string {
	for _, candidate := range [][]byte{rawJSON, req.Payload, opts.OriginalRequest} {
		if value := codexConversationIDFromJSON(candidate); value != "" {
			return value
		}
	}
	if opts.Metadata != nil {
		if raw, ok := opts.Metadata[cliproxyexecutor.SessionIDMetadataKey]; ok {
			if value, ok := raw.(string); ok {
				if sessionID := sanitizeCodexConversationID(value); sessionID != "" {
					return sessionID
				}
			}
		}
	}
	if opts.Headers != nil {
		for _, key := range []string{"session_id", "x-session-id"} {
			if sessionID := sanitizeCodexConversationID(opts.Headers.Get(key)); sessionID != "" {
				return sessionID
			}
		}
	}
	return ""
}

func applyCodexHeaders(r *http.Request, auth *cliproxyauth.Auth, token string, stream bool) {
	if r == nil {
		return
	}
	resetCodexClientHeaders(r)
	var attrs map[string]string
	if auth != nil {
		attrs = auth.Attributes
	}
	util.ApplyCustomHeadersFromAttrs(r, attrs)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+token)

	misc.EnsureHeader(r.Header, nil, "Version", codexClientVersion)
	misc.EnsureHeader(r.Header, nil, "Openai-Beta", codexResponsesBeta)
	misc.EnsureHeader(r.Header, nil, "Session_id", uuid.NewString())
	misc.EnsureHeader(r.Header, nil, "User-Agent", defaultCodexUserAgent)
	misc.EnsureHeader(r.Header, nil, "X-Client-Request-Id", r.Header.Get("Session_id"))
	ensureCodexWindowHeader(r.Header)

	if stream {
		r.Header.Set("Accept", "text/event-stream")
	} else {
		r.Header.Set("Accept", "application/json")
	}
	r.Header.Set("Connection", "Keep-Alive")

	if !codexUsesAPIKey(auth) {
		r.Header.Set("Originator", defaultCodexOriginator)
		misc.EnsureHeader(r.Header, nil, "Origin", codexWebOrigin)
		misc.EnsureHeader(r.Header, nil, "Referer", codexCodexReferer)
	}
	resetCodexProtectedHeaders(r.Header, auth, token)
	deleteDeprecatedCodexConversationHeader(r.Header)
}

func resetCodexClientHeaders(req *http.Request) {
	if req == nil {
		return
	}
	headers := req.Header
	if headers == nil {
		return
	}
	keepSession := req.Context().Value(codexInternalSessionContextKey{}) == true
	for key := range headers {
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "session_id":
			if keepSession {
				continue
			}
			delete(headers, key)
		default:
			delete(headers, key)
		}
	}
}

func resetCodexProtectedHeaders(headers http.Header, auth *cliproxyauth.Auth, token string) {
	if headers == nil {
		return
	}
	headerValue := func(key string) string { return strings.TrimSpace(headers.Get(key)) }
	sessionID := headerValue("Session_id")
	if sessionID == "" {
		sessionID = uuid.NewString()
		headers.Set("Session_id", sessionID)
	}
	headers.Set("Content-Type", "application/json")
	if token = strings.TrimSpace(token); token != "" {
		headers.Set("Authorization", "Bearer "+token)
	} else {
		headers.Del("Authorization")
	}
	headers.Set("Version", codexClientVersion)
	headers.Set("Openai-Beta", codexResponsesBeta)
	headers.Set("User-Agent", defaultCodexUserAgent)
	headers.Set("X-Client-Request-Id", sessionID)
	if !codexUsesAPIKey(auth) {
		headers.Set("Originator", defaultCodexOriginator)
		headers.Set("Origin", codexWebOrigin)
		headers.Set("Referer", codexCodexReferer)
	} else {
		headers.Del("Originator")
		headers.Del("Origin")
		headers.Del("Referer")
		headers.Del("Chatgpt-Account-Id")
	}
}

func applyModelHeaderOverrides(headers http.Header, modelName string) {
	if headers == nil {
		return
	}
	overrides := registry.ModelOverrideHeaders(modelName)
	if len(overrides) == 0 {
		return
	}
	for key, value := range overrides {
		headers.Set(key, value)
	}
	if strings.Contains(headers.Get("User-Agent"), "Mac OS") && strings.TrimSpace(headers.Get("Session_id")) == "" {
		headers.Set("Session_id", uuid.NewString())
	}
}

func ensureCodexWindowHeader(headers http.Header) {
	if headers == nil || strings.TrimSpace(headers.Get("X-Codex-Window-Id")) != "" {
		return
	}
	sessionID := sanitizeCodexConversationID(headers.Get("Session_id"))
	if sessionID == "" {
		sessionID = sanitizeCodexConversationID(headers.Get("Conversation_id"))
	}
	if sessionID == "" {
		return
	}
	headers.Set("X-Codex-Window-Id", sessionID+":0")
}

func injectCodexClientMetadata(rawJSON []byte, auth *cliproxyauth.Auth) []byte {
	if len(rawJSON) == 0 {
		return rawJSON
	}
	installationID := resolveCodexInstallationID(auth)
	if installationID != "" {
		updated, err := sjson.SetBytes(rawJSON, "client_metadata.x-codex-installation-id", installationID)
		if err == nil {
			rawJSON = updated
		}
	} else {
		rawJSON, _ = sjson.DeleteBytes(rawJSON, "client_metadata.x-codex-installation-id")
	}
	return rawJSON
}

func normalizeCodexClientMetadata(rawJSON []byte) []byte {
	if len(rawJSON) == 0 {
		return rawJSON
	}
	metadata := gjson.GetBytes(rawJSON, "client_metadata")
	if !metadata.IsObject() {
		return rawJSON
	}
	allowed := map[string]struct{}{
		"x-codex-installation-id": {},
		"x-codex-turn-metadata":   {},
		"x-codex-window-id":       {},
	}
	for key := range metadata.Map() {
		if _, ok := allowed[key]; ok {
			continue
		}
		rawJSON, _ = sjson.DeleteBytes(rawJSON, "client_metadata."+key)
	}
	return rawJSON
}

func resolveCodexInstallationID(auth *cliproxyauth.Auth) string {
	if auth == nil {
		return ""
	}
	for _, candidate := range []string{
		strings.TrimSpace(auth.Attributes["x_codex_installation_id"]),
		strings.TrimSpace(auth.Attributes["installation_id"]),
	} {
		if candidate != "" {
			return candidate
		}
	}
	if auth.Metadata != nil {
		for _, key := range []string{"x_codex_installation_id", "installation_id"} {
			if value, ok := auth.Metadata[key].(string); ok && strings.TrimSpace(value) != "" {
				return strings.TrimSpace(value)
			}
		}
	}

	seed := ""
	for _, candidate := range []string{
		resolveCodexAccountID(auth),
		auth.ID,
		auth.FileName,
	} {
		candidate = strings.TrimSpace(candidate)
		if candidate != "" {
			seed = candidate
			break
		}
	}
	if seed == "" && auth.Metadata != nil {
		if value, ok := auth.Metadata["email"].(string); ok {
			seed = strings.TrimSpace(value)
		}
	}
	if seed == "" {
		return ""
	}
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte("codex-installation:"+seed)).String()
}

func codexUserAgent(context.Context) string {
	return defaultCodexUserAgent
}

func parseCodexRetryAfter(statusCode int, errorBody []byte, now time.Time) *time.Duration {
	if statusCode != http.StatusTooManyRequests || len(errorBody) == 0 {
		return nil
	}
	if strings.TrimSpace(gjson.GetBytes(errorBody, "error.type").String()) != "usage_limit_reached" {
		return nil
	}
	if resetsAt := gjson.GetBytes(errorBody, "error.resets_at").Int(); resetsAt > 0 {
		resetAtTime := time.Unix(resetsAt, 0)
		if resetAtTime.After(now) {
			retryAfter := resetAtTime.Sub(now)
			return &retryAfter
		}
	}
	if resetsInSeconds := gjson.GetBytes(errorBody, "error.resets_in_seconds").Int(); resetsInSeconds > 0 {
		retryAfter := time.Duration(resetsInSeconds) * time.Second
		return &retryAfter
	}
	return nil
}

func codexCreds(a *cliproxyauth.Auth) (apiKey, baseURL string) {
	if a == nil {
		return "", ""
	}
	if a.Attributes != nil {
		apiKey = strings.TrimSpace(a.Attributes["api_key"])
		if apiKey == "" {
			apiKey = strings.TrimSpace(a.Attributes["access_token"])
		}
		if apiKey == "" {
			apiKey = strings.TrimSpace(a.Attributes["token"])
		}
		baseURL = a.Attributes["base_url"]
	}
	if apiKey == "" && a.Metadata != nil {
		if v, ok := a.Metadata["access_token"].(string); ok {
			apiKey = strings.TrimSpace(v)
		}
		if apiKey == "" {
			if v, ok := a.Metadata["token"].(string); ok {
				apiKey = strings.TrimSpace(v)
			}
		}
	}
	return
}

func codexUsesAPIKey(auth *cliproxyauth.Auth) bool {
	if auth == nil || auth.Attributes == nil {
		return false
	}
	return strings.TrimSpace(auth.Attributes["api_key"]) != ""
}

func resolveCodexAccountID(auth *cliproxyauth.Auth) string {
	if auth == nil {
		return ""
	}
	if auth.Metadata != nil {
		for _, key := range []string{"account_id", "chatgpt_account_id"} {
			if v, ok := auth.Metadata[key].(string); ok && strings.TrimSpace(v) != "" {
				return strings.TrimSpace(v)
			}
		}
		for _, key := range []string{"access_token", "id_token", "token"} {
			if raw, ok := auth.Metadata[key].(string); ok && strings.TrimSpace(raw) != "" {
				if claims, err := codexauth.ParseJWTToken(strings.TrimSpace(raw)); err == nil && claims != nil {
					if accountID := strings.TrimSpace(claims.GetAccountID()); accountID != "" {
						return accountID
					}
				}
			}
		}
	}
	if auth.Attributes != nil {
		for _, key := range []string{"account_id", "chatgpt_account_id"} {
			if v := strings.TrimSpace(auth.Attributes[key]); v != "" {
				return v
			}
		}
	}
	return ""
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
