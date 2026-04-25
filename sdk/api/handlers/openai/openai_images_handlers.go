package openai

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/interfaces"
	intlogging "github.com/router-for-me/CLIProxyAPI/v6/internal/logging"
	"github.com/router-for-me/CLIProxyAPI/v6/sdk/api/handlers"
	log "github.com/sirupsen/logrus"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const (
	// Route image requests through a model that is already exposed by the local
	// Codex/OpenAI compatibility registry. The upstream code uses gpt-5.4-mini,
	// but this fork does not currently register that internal model name.
	defaultImagesMainModel = "gpt-5.4"
	defaultImagesToolModel = "gpt-image-2"
	imageJSONShellPrefix   = `{"_proxy_me_progress":"`
	imageJSONShellBurstLen = 36 * 1024
)

type imageCallResult struct {
	Result        string
	RevisedPrompt string
	OutputFormat  string
	Size          string
	Background    string
	Quality       string
}

type imageStreamResults struct {
	items     []imageCallResult
	firstMeta imageCallResult
	seen      map[string]struct{}
}

type imageResponseSummary struct {
	ImageCount   int
	CreatedAt    int64
	OutputFormat string
	UsageImages  int64
}

type imageCollectionResult struct {
	out     []byte
	summary imageResponseSummary
	errMsg  *interfaces.ErrorMessage
}

type imageJSONShellWriter struct {
	writer  io.Writer
	flusher http.Flusher
	stopCh  chan struct{}
	doneCh  chan struct{}
}

type sseFrameAccumulator struct {
	pending []byte
}

func responsesSSEFrameLen(chunk []byte) int {
	if len(chunk) == 0 {
		return 0
	}
	lf := bytes.Index(chunk, []byte("\n\n"))
	crlf := bytes.Index(chunk, []byte("\r\n\r\n"))
	switch {
	case lf < 0:
		if crlf < 0 {
			return 0
		}
		return crlf + 4
	case crlf < 0:
		return lf + 2
	case lf < crlf:
		return lf + 2
	default:
		return crlf + 4
	}
}

func responsesSSENeedsMoreData(chunk []byte) bool {
	trimmed := bytes.TrimSpace(chunk)
	if len(trimmed) == 0 {
		return false
	}
	return responsesSSEHasField(trimmed, []byte("event:")) && !responsesSSEHasField(trimmed, []byte("data:"))
}

func responsesSSEHasField(chunk []byte, prefix []byte) bool {
	s := chunk
	for len(s) > 0 {
		line := s
		if i := bytes.IndexByte(s, '\n'); i >= 0 {
			line = s[:i]
			s = s[i+1:]
		} else {
			s = nil
		}
		line = bytes.TrimSpace(line)
		if bytes.HasPrefix(line, prefix) {
			return true
		}
	}
	return false
}

func imageLogEntry(c *gin.Context) *log.Entry {
	if c == nil {
		return log.NewEntry(log.StandardLogger())
	}
	if requestID := intlogging.GetGinRequestID(c); requestID != "" {
		return log.WithField("request_id", requestID)
	}
	return log.NewEntry(log.StandardLogger())
}

func responsesSSECanEmitWithoutDelimiter(chunk []byte) bool {
	trimmed := bytes.TrimSpace(chunk)
	if len(trimmed) == 0 || responsesSSENeedsMoreData(trimmed) || !responsesSSEHasField(trimmed, []byte("data:")) {
		return false
	}
	return responsesSSEDataLinesValid(trimmed)
}

func responsesSSEDataLinesValid(chunk []byte) bool {
	s := chunk
	for len(s) > 0 {
		line := s
		if i := bytes.IndexByte(s, '\n'); i >= 0 {
			line = s[:i]
			s = s[i+1:]
		} else {
			s = nil
		}
		line = bytes.TrimSpace(line)
		if len(line) == 0 || !bytes.HasPrefix(line, []byte("data:")) {
			continue
		}
		data := bytes.TrimSpace(line[len("data:"):])
		if len(data) == 0 || bytes.Equal(data, []byte("[DONE]")) {
			continue
		}
		if !json.Valid(data) {
			return false
		}
	}
	return true
}

func responsesSSENeedsLineBreak(pending, chunk []byte) bool {
	if len(pending) == 0 || len(chunk) == 0 {
		return false
	}
	if bytes.HasSuffix(pending, []byte("\n")) || bytes.HasSuffix(pending, []byte("\r")) {
		return false
	}
	if chunk[0] == '\n' || chunk[0] == '\r' {
		return false
	}
	trimmed := bytes.TrimLeft(chunk, " \t")
	if len(trimmed) == 0 {
		return false
	}
	for _, prefix := range [][]byte{[]byte("data:"), []byte("event:"), []byte("id:"), []byte("retry:"), []byte(":")} {
		if bytes.HasPrefix(trimmed, prefix) {
			return true
		}
	}
	return false
}

func (a *sseFrameAccumulator) AddChunk(chunk []byte) [][]byte {
	if len(chunk) == 0 {
		return nil
	}

	if responsesSSENeedsLineBreak(a.pending, chunk) {
		a.pending = append(a.pending, '\n')
	}
	a.pending = append(a.pending, chunk...)

	var frames [][]byte
	for {
		frameLen := responsesSSEFrameLen(a.pending)
		if frameLen == 0 {
			break
		}
		frames = append(frames, a.pending[:frameLen])
		copy(a.pending, a.pending[frameLen:])
		a.pending = a.pending[:len(a.pending)-frameLen]
	}

	if len(bytes.TrimSpace(a.pending)) == 0 {
		a.pending = a.pending[:0]
		return frames
	}
	if len(a.pending) == 0 || !responsesSSECanEmitWithoutDelimiter(a.pending) {
		return frames
	}
	frames = append(frames, a.pending)
	a.pending = a.pending[:0]
	return frames
}

func (a *sseFrameAccumulator) Flush() [][]byte {
	if len(a.pending) == 0 {
		return nil
	}

	var frames [][]byte
	for {
		frameLen := responsesSSEFrameLen(a.pending)
		if frameLen == 0 {
			break
		}
		frames = append(frames, a.pending[:frameLen])
		copy(a.pending, a.pending[frameLen:])
		a.pending = a.pending[:len(a.pending)-frameLen]
	}

	if len(bytes.TrimSpace(a.pending)) == 0 {
		a.pending = nil
		return frames
	}
	if responsesSSECanEmitWithoutDelimiter(a.pending) {
		frames = append(frames, a.pending)
	}
	a.pending = nil
	return frames
}

func mimeTypeFromOutputFormat(outputFormat string) string {
	if outputFormat == "" {
		return "image/png"
	}
	if strings.Contains(outputFormat, "/") {
		return outputFormat
	}
	switch strings.ToLower(strings.TrimSpace(outputFormat)) {
	case "png":
		return "image/png"
	case "jpg", "jpeg":
		return "image/jpeg"
	case "webp":
		return "image/webp"
	default:
		return "image/png"
	}
}

func multipartFileToDataURL(fileHeader *multipart.FileHeader) (string, error) {
	if fileHeader == nil {
		return "", fmt.Errorf("upload file is nil")
	}
	f, err := fileHeader.Open()
	if err != nil {
		return "", fmt.Errorf("open upload file failed: %w", err)
	}
	defer func() {
		if errClose := f.Close(); errClose != nil {
			log.Errorf("openai images: close upload file error: %v", errClose)
		}
	}()

	data, err := io.ReadAll(f)
	if err != nil {
		return "", fmt.Errorf("read upload file failed: %w", err)
	}

	mediaType := strings.TrimSpace(fileHeader.Header.Get("Content-Type"))
	if mediaType == "" {
		mediaType = http.DetectContentType(data)
	}

	b64 := base64.StdEncoding.EncodeToString(data)
	return "data:" + mediaType + ";base64," + b64, nil
}

func parseIntField(raw string, fallback int64) int64 {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback
	}
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return fallback
	}
	return v
}

func parseBoolField(raw string, fallback bool) bool {
	raw = strings.TrimSpace(strings.ToLower(raw))
	if raw == "" {
		return fallback
	}
	switch raw {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

func (h *OpenAIAPIHandler) ImagesGenerations(c *gin.Context) {
	rawJSON, err := c.GetRawData()
	if err != nil {
		c.JSON(http.StatusBadRequest, handlers.ErrorResponse{
			Error: handlers.ErrorDetail{
				Message: fmt.Sprintf("Invalid request: %v", err),
				Type:    "invalid_request_error",
			},
		})
		return
	}
	if !json.Valid(rawJSON) {
		c.JSON(http.StatusBadRequest, handlers.ErrorResponse{
			Error: handlers.ErrorDetail{
				Message: "Invalid request: body must be valid JSON",
				Type:    "invalid_request_error",
			},
		})
		return
	}

	prompt := strings.TrimSpace(gjson.GetBytes(rawJSON, "prompt").String())
	if prompt == "" {
		c.JSON(http.StatusBadRequest, handlers.ErrorResponse{
			Error: handlers.ErrorDetail{
				Message: "Invalid request: prompt is required",
				Type:    "invalid_request_error",
			},
		})
		return
	}

	responseFormat := strings.TrimSpace(gjson.GetBytes(rawJSON, "response_format").String())
	if responseFormat == "" {
		responseFormat = "b64_json"
	}
	stream := gjson.GetBytes(rawJSON, "stream").Bool()

	imageModel := strings.TrimSpace(gjson.GetBytes(rawJSON, "model").String())
	if imageModel == "" {
		imageModel = defaultImagesToolModel
	}
	imageLogEntry(c).WithFields(log.Fields{
		"endpoint":        "/v1/images/generations",
		"tool_model":      imageModel,
		"main_model":      defaultImagesMainModel,
		"stream":          stream,
		"response_format": responseFormat,
		"prompt_len":      len(prompt),
	}).Info("image request accepted")

	tool := []byte(`{"type":"image_generation","action":"generate","output_format":"png"}`)
	tool, _ = sjson.SetBytes(tool, "model", imageModel)

	if v := strings.TrimSpace(gjson.GetBytes(rawJSON, "size").String()); v != "" {
		tool, _ = sjson.SetBytes(tool, "size", v)
	}
	if v := strings.TrimSpace(gjson.GetBytes(rawJSON, "quality").String()); v != "" {
		tool, _ = sjson.SetBytes(tool, "quality", v)
	}
	if v := strings.TrimSpace(gjson.GetBytes(rawJSON, "background").String()); v != "" {
		tool, _ = sjson.SetBytes(tool, "background", v)
	}
	if v := strings.TrimSpace(gjson.GetBytes(rawJSON, "output_format").String()); v != "" {
		tool, _ = sjson.SetBytes(tool, "output_format", v)
	}
	if v := gjson.GetBytes(rawJSON, "output_compression"); v.Exists() {
		if v.Type == gjson.Number {
			tool, _ = sjson.SetBytes(tool, "output_compression", v.Int())
		}
	}
	if v := gjson.GetBytes(rawJSON, "partial_images"); v.Exists() {
		if v.Type == gjson.Number {
			tool, _ = sjson.SetBytes(tool, "partial_images", v.Int())
		}
	}
	if v := strings.TrimSpace(gjson.GetBytes(rawJSON, "moderation").String()); v != "" {
		tool, _ = sjson.SetBytes(tool, "moderation", v)
	}

	responsesReq := buildImagesResponsesRequest(prompt, nil, tool)
	if stream {
		h.streamImagesFromResponses(c, responsesReq, responseFormat, "image_generation")
		return
	}
	h.collectImagesFromResponses(c, responsesReq, responseFormat)
}

func (h *OpenAIAPIHandler) ImagesEdits(c *gin.Context) {
	contentType := strings.ToLower(strings.TrimSpace(c.GetHeader("Content-Type")))
	if strings.HasPrefix(contentType, "application/json") {
		h.imagesEditsFromJSON(c)
		return
	}
	if strings.HasPrefix(contentType, "multipart/form-data") || contentType == "" {
		h.imagesEditsFromMultipart(c)
		return
	}

	c.JSON(http.StatusBadRequest, handlers.ErrorResponse{
		Error: handlers.ErrorDetail{
			Message: fmt.Sprintf("Invalid request: unsupported Content-Type %q", contentType),
			Type:    "invalid_request_error",
		},
	})
}

func (h *OpenAIAPIHandler) imagesEditsFromMultipart(c *gin.Context) {
	form, err := c.MultipartForm()
	if err != nil {
		c.JSON(http.StatusBadRequest, handlers.ErrorResponse{
			Error: handlers.ErrorDetail{
				Message: fmt.Sprintf("Invalid request: %v", err),
				Type:    "invalid_request_error",
			},
		})
		return
	}

	prompt := strings.TrimSpace(c.PostForm("prompt"))
	if prompt == "" {
		c.JSON(http.StatusBadRequest, handlers.ErrorResponse{
			Error: handlers.ErrorDetail{
				Message: "Invalid request: prompt is required",
				Type:    "invalid_request_error",
			},
		})
		return
	}

	var imageFiles []*multipart.FileHeader
	if files := form.File["image[]"]; len(files) > 0 {
		imageFiles = files
	} else if files := form.File["image"]; len(files) > 0 {
		imageFiles = files
	}
	if len(imageFiles) == 0 {
		c.JSON(http.StatusBadRequest, handlers.ErrorResponse{
			Error: handlers.ErrorDetail{
				Message: "Invalid request: image is required",
				Type:    "invalid_request_error",
			},
		})
		return
	}

	images := make([]string, 0, len(imageFiles))
	for _, fh := range imageFiles {
		dataURL, err := multipartFileToDataURL(fh)
		if err != nil {
			c.JSON(http.StatusBadRequest, handlers.ErrorResponse{
				Error: handlers.ErrorDetail{
					Message: fmt.Sprintf("Invalid request: %v", err),
					Type:    "invalid_request_error",
				},
			})
			return
		}
		images = append(images, dataURL)
	}

	var maskDataURL *string
	if maskFiles := form.File["mask"]; len(maskFiles) > 0 && maskFiles[0] != nil {
		dataURL, err := multipartFileToDataURL(maskFiles[0])
		if err != nil {
			c.JSON(http.StatusBadRequest, handlers.ErrorResponse{
				Error: handlers.ErrorDetail{
					Message: fmt.Sprintf("Invalid request: %v", err),
					Type:    "invalid_request_error",
				},
			})
			return
		}
		maskDataURL = &dataURL
	}

	responseFormat := strings.TrimSpace(c.PostForm("response_format"))
	if responseFormat == "" {
		responseFormat = "b64_json"
	}
	stream := parseBoolField(c.PostForm("stream"), false)

	imageModel := strings.TrimSpace(c.PostForm("model"))
	if imageModel == "" {
		imageModel = defaultImagesToolModel
	}

	tool := []byte(`{"type":"image_generation","action":"edit","output_format":"png"}`)
	tool, _ = sjson.SetBytes(tool, "model", imageModel)

	if v := strings.TrimSpace(c.PostForm("size")); v != "" {
		tool, _ = sjson.SetBytes(tool, "size", v)
	}
	if v := strings.TrimSpace(c.PostForm("quality")); v != "" {
		tool, _ = sjson.SetBytes(tool, "quality", v)
	}
	if v := strings.TrimSpace(c.PostForm("background")); v != "" {
		tool, _ = sjson.SetBytes(tool, "background", v)
	}
	if v := strings.TrimSpace(c.PostForm("output_format")); v != "" {
		tool, _ = sjson.SetBytes(tool, "output_format", v)
	}
	if v := strings.TrimSpace(c.PostForm("input_fidelity")); v != "" {
		tool, _ = sjson.SetBytes(tool, "input_fidelity", v)
	}
	if v := strings.TrimSpace(c.PostForm("moderation")); v != "" {
		tool, _ = sjson.SetBytes(tool, "moderation", v)
	}

	if v := strings.TrimSpace(c.PostForm("output_compression")); v != "" {
		tool, _ = sjson.SetBytes(tool, "output_compression", parseIntField(v, 0))
	}
	if v := strings.TrimSpace(c.PostForm("partial_images")); v != "" {
		tool, _ = sjson.SetBytes(tool, "partial_images", parseIntField(v, 0))
	}

	if maskDataURL != nil && strings.TrimSpace(*maskDataURL) != "" {
		tool, _ = sjson.SetBytes(tool, "input_image_mask.image_url", strings.TrimSpace(*maskDataURL))
	}

	responsesReq := buildImagesResponsesRequest(prompt, images, tool)
	if stream {
		h.streamImagesFromResponses(c, responsesReq, responseFormat, "image_edit")
		return
	}
	h.collectImagesFromResponses(c, responsesReq, responseFormat)
}

func (h *OpenAIAPIHandler) imagesEditsFromJSON(c *gin.Context) {
	rawJSON, err := c.GetRawData()
	if err != nil {
		c.JSON(http.StatusBadRequest, handlers.ErrorResponse{
			Error: handlers.ErrorDetail{
				Message: fmt.Sprintf("Invalid request: %v", err),
				Type:    "invalid_request_error",
			},
		})
		return
	}
	if !json.Valid(rawJSON) {
		c.JSON(http.StatusBadRequest, handlers.ErrorResponse{
			Error: handlers.ErrorDetail{
				Message: "Invalid request: body must be valid JSON",
				Type:    "invalid_request_error",
			},
		})
		return
	}

	prompt := strings.TrimSpace(gjson.GetBytes(rawJSON, "prompt").String())
	if prompt == "" {
		c.JSON(http.StatusBadRequest, handlers.ErrorResponse{
			Error: handlers.ErrorDetail{
				Message: "Invalid request: prompt is required",
				Type:    "invalid_request_error",
			},
		})
		return
	}

	var images []string
	imagesResult := gjson.GetBytes(rawJSON, "images")
	if imagesResult.IsArray() {
		for _, img := range imagesResult.Array() {
			url := strings.TrimSpace(img.Get("image_url").String())
			if url == "" {
				continue
			}
			images = append(images, url)
		}
	}
	if len(images) == 0 {
		c.JSON(http.StatusBadRequest, handlers.ErrorResponse{
			Error: handlers.ErrorDetail{
				Message: "Invalid request: images[].image_url is required (file_id is not supported)",
				Type:    "invalid_request_error",
			},
		})
		return
	}

	var maskDataURL *string
	if mask := gjson.GetBytes(rawJSON, "mask.image_url"); mask.Exists() {
		url := strings.TrimSpace(mask.String())
		if url != "" {
			maskDataURL = &url
		}
	} else if mask := gjson.GetBytes(rawJSON, "mask.file_id"); mask.Exists() {
		c.JSON(http.StatusBadRequest, handlers.ErrorResponse{
			Error: handlers.ErrorDetail{
				Message: "Invalid request: mask.file_id is not supported (use mask.image_url instead)",
				Type:    "invalid_request_error",
			},
		})
		return
	}

	responseFormat := strings.TrimSpace(gjson.GetBytes(rawJSON, "response_format").String())
	if responseFormat == "" {
		responseFormat = "b64_json"
	}
	stream := gjson.GetBytes(rawJSON, "stream").Bool()

	imageModel := strings.TrimSpace(gjson.GetBytes(rawJSON, "model").String())
	if imageModel == "" {
		imageModel = defaultImagesToolModel
	}
	imageLogEntry(c).WithFields(log.Fields{
		"endpoint":        "/v1/images/edits",
		"tool_model":      imageModel,
		"main_model":      defaultImagesMainModel,
		"stream":          stream,
		"response_format": responseFormat,
		"prompt_len":      len(prompt),
		"image_count":     len(images),
		"has_mask":        maskDataURL != nil && strings.TrimSpace(*maskDataURL) != "",
	}).Info("image edit request accepted")

	tool := []byte(`{"type":"image_generation","action":"edit","output_format":"png"}`)
	tool, _ = sjson.SetBytes(tool, "model", imageModel)

	for _, field := range []string{"size", "quality", "background", "output_format", "input_fidelity", "moderation"} {
		if v := strings.TrimSpace(gjson.GetBytes(rawJSON, field).String()); v != "" {
			tool, _ = sjson.SetBytes(tool, field, v)
		}
	}

	for _, field := range []string{"output_compression", "partial_images"} {
		if v := gjson.GetBytes(rawJSON, field); v.Exists() && v.Type == gjson.Number {
			tool, _ = sjson.SetBytes(tool, field, v.Int())
		}
	}

	if maskDataURL != nil && strings.TrimSpace(*maskDataURL) != "" {
		tool, _ = sjson.SetBytes(tool, "input_image_mask.image_url", strings.TrimSpace(*maskDataURL))
	}

	responsesReq := buildImagesResponsesRequest(prompt, images, tool)
	if stream {
		h.streamImagesFromResponses(c, responsesReq, responseFormat, "image_edit")
		return
	}
	h.collectImagesFromResponses(c, responsesReq, responseFormat)
}

func buildImagesResponsesRequest(prompt string, images []string, toolJSON []byte) []byte {
	req := []byte(`{"instructions":"","stream":true,"reasoning":{"effort":"medium","summary":"auto"},"parallel_tool_calls":true,"include":["reasoning.encrypted_content"],"model":"","store":false,"tool_choice":{"type":"image_generation"}}`)
	req, _ = sjson.SetBytes(req, "model", defaultImagesMainModel)

	input := []byte(`[{"type":"message","role":"user","content":[{"type":"input_text","text":""}]}]`)
	input, _ = sjson.SetBytes(input, "0.content.0.text", prompt)
	contentIndex := 1
	for _, img := range images {
		if strings.TrimSpace(img) == "" {
			continue
		}
		part := []byte(`{"type":"input_image","image_url":""}`)
		part, _ = sjson.SetBytes(part, "image_url", img)
		path := fmt.Sprintf("0.content.%d", contentIndex)
		input, _ = sjson.SetRawBytes(input, path, part)
		contentIndex++
	}
	req, _ = sjson.SetRawBytes(req, "input", input)

	req, _ = sjson.SetRawBytes(req, "tools", []byte(`[]`))
	if len(toolJSON) > 0 && json.Valid(toolJSON) {
		req, _ = sjson.SetRawBytes(req, "tools.-1", toolJSON)
	}
	return req
}

func (h *OpenAIAPIHandler) collectImagesFromResponses(c *gin.Context, responsesReq []byte, responseFormat string) {
	c.Header("Content-Type", "application/json")
	startedAt := time.Now()
	logger := imageLogEntry(c).WithFields(log.Fields{
		"mode":            "nonstream",
		"main_model":      defaultImagesMainModel,
		"response_format": responseFormat,
	})
	logger.Info("image upstream collection started")

	cliCtx, cliCancel := h.GetContextWithCancel(h, c, context.Background())
	dataChan, errChan := h.ExecuteStreamWithAuthManager(cliCtx, "openai-response", defaultImagesMainModel, responsesReq, "")

	resultCh := make(chan imageCollectionResult, 1)
	go func() {
		out, summary, errMsg := collectImagesFromResponsesStream(cliCtx, dataChan, errChan, responseFormat)
		resultCh <- imageCollectionResult{out: out, summary: summary, errMsg: errMsg}
	}()

	shellDelay := 2 * time.Second
	delayTimer := time.NewTimer(shellDelay)
	defer delayTimer.Stop()

	var shellWriter *imageJSONShellWriter
	shellStarted := false
	shellInterval := handlers.NonStreamingKeepAliveInterval(h.Cfg)
	if shellInterval <= 0 {
		shellInterval = 5 * time.Second
	}

	for {
		select {
		case <-cliCtx.Done():
			logger.WithFields(log.Fields{
				"duration_ms": time.Since(startedAt).Milliseconds(),
				"error":       cliCtx.Err(),
			}).Warn("image upstream collection cancelled")
			return
		case <-delayTimer.C:
			shellWriter = startImageJSONShell(c, shellInterval)
			shellStarted = shellWriter != nil
			if shellStarted {
				logger.WithFields(log.Fields{
					"delay_ms":    shellDelay.Milliseconds(),
					"interval_ms": shellInterval.Milliseconds(),
					"compat_mode": "json_shell",
				}).Info("image nonstream compatibility shell started")
			}
		case result := <-resultCh:
			if result.errMsg != nil {
				fields := log.Fields{
					"duration_ms": time.Since(startedAt).Milliseconds(),
					"status":      result.errMsg.StatusCode,
				}
				if result.errMsg.Error != nil {
					fields["error"] = result.errMsg.Error.Error()
				}
				logger.WithFields(fields).Warn("image upstream collection failed")
				if shellStarted {
					if err := shellWriter.finishWithError(result.errMsg); err != nil {
						logger.WithField("shell_error", err.Error()).Warn("failed to finalize image compatibility shell with error")
					}
				} else {
					h.WriteErrorResponse(c, result.errMsg)
				}
				if result.errMsg.Error != nil {
					cliCancel(result.errMsg.Error)
				} else {
					cliCancel(nil)
				}
				return
			}

			if shellStarted {
				if err := shellWriter.finishWithObject(result.out); err != nil {
					logger.WithField("shell_error", err.Error()).Warn("failed to finalize image compatibility shell")
				}
			} else {
				_, _ = c.Writer.Write(result.out)
			}
			logger.WithFields(log.Fields{
				"duration_ms":   time.Since(startedAt).Milliseconds(),
				"image_count":   result.summary.ImageCount,
				"created_at":    result.summary.CreatedAt,
				"output_format": result.summary.OutputFormat,
				"usage_images":  result.summary.UsageImages,
				"compat_mode":   map[bool]string{true: "json_shell", false: "standard"}[shellStarted],
			}).Info("image upstream collection completed")
			cliCancel(nil)
			return
		}
	}
}

func collectImagesFromResponsesStream(ctx context.Context, data <-chan []byte, errs <-chan *interfaces.ErrorMessage, responseFormat string) ([]byte, imageResponseSummary, *interfaces.ErrorMessage) {
	acc := &sseFrameAccumulator{}
	collected := &imageStreamResults{}

	processFrame := func(frame []byte) ([]byte, imageResponseSummary, bool, *interfaces.ErrorMessage) {
		for _, line := range bytes.Split(frame, []byte("\n")) {
			trimmed := bytes.TrimSpace(bytes.TrimRight(line, "\r"))
			if len(trimmed) == 0 {
				continue
			}
			if !bytes.HasPrefix(trimmed, []byte("data:")) {
				continue
			}
			payload := bytes.TrimSpace(trimmed[len("data:"):])
			if len(payload) == 0 || bytes.Equal(payload, []byte("[DONE]")) {
				continue
			}
			if !json.Valid(payload) {
				return nil, imageResponseSummary{}, false, &interfaces.ErrorMessage{StatusCode: http.StatusBadGateway, Error: fmt.Errorf("invalid SSE data JSON")}
			}

			switch gjson.GetBytes(payload, "type").String() {
			case "response.output_item.done":
				if item, ok := extractImageFromResponsesOutputItemDone(payload); ok {
					collected.Add(item)
				}
			case "response.completed":
				results, createdAt, usageRaw, firstMeta, err := extractImagesFromResponsesCompleted(payload)
				if err != nil {
					return nil, imageResponseSummary{}, false, &interfaces.ErrorMessage{StatusCode: http.StatusBadGateway, Error: err}
				}
				collected.AddMany(results)

				if len(collected.items) == 0 {
					return nil, imageResponseSummary{}, false, &interfaces.ErrorMessage{StatusCode: http.StatusBadGateway, Error: fmt.Errorf("upstream did not return image output")}
				}
				if firstMeta.Result == "" && collected.firstMeta.Result != "" {
					firstMeta = collected.firstMeta
				}
				out, err := buildImagesAPIResponse(collected.items, createdAt, usageRaw, firstMeta, responseFormat)
				if err != nil {
					return nil, imageResponseSummary{}, false, &interfaces.ErrorMessage{StatusCode: http.StatusInternalServerError, Error: err}
				}
				return out, imageResponseSummary{
					ImageCount:   len(collected.items),
					CreatedAt:    createdAt,
					OutputFormat: firstMeta.OutputFormat,
					UsageImages:  gjson.GetBytes(usageRaw, "num_images").Int(),
				}, true, nil
			}
		}
		return nil, imageResponseSummary{}, false, nil
	}

	for {
		select {
		case <-ctx.Done():
			return nil, imageResponseSummary{}, &interfaces.ErrorMessage{StatusCode: http.StatusRequestTimeout, Error: ctx.Err()}
		case errMsg, ok := <-errs:
			if ok && errMsg != nil {
				return nil, imageResponseSummary{}, errMsg
			}
			errs = nil
		case chunk, ok := <-data:
			if !ok {
				for _, frame := range acc.Flush() {
					if out, summary, done, errMsg := processFrame(frame); errMsg != nil {
						return nil, imageResponseSummary{}, errMsg
					} else if done {
						return out, summary, nil
					}
				}
				return nil, imageResponseSummary{}, &interfaces.ErrorMessage{StatusCode: http.StatusBadGateway, Error: fmt.Errorf("stream disconnected before completion")}
			}
			for _, frame := range acc.AddChunk(chunk) {
				if out, summary, done, errMsg := processFrame(frame); errMsg != nil {
					return nil, imageResponseSummary{}, errMsg
				} else if done {
					return out, summary, nil
				}
			}
		}
	}
}

func extractImagesFromResponsesCompleted(payload []byte) (results []imageCallResult, createdAt int64, usageRaw []byte, firstMeta imageCallResult, err error) {
	if gjson.GetBytes(payload, "type").String() != "response.completed" {
		return nil, 0, nil, imageCallResult{}, fmt.Errorf("unexpected event type")
	}

	createdAt = gjson.GetBytes(payload, "response.created_at").Int()
	if createdAt <= 0 {
		createdAt = time.Now().Unix()
	}

	output := gjson.GetBytes(payload, "response.output")
	if output.IsArray() {
		for _, item := range output.Array() {
			if item.Get("type").String() != "image_generation_call" {
				continue
			}
			res := strings.TrimSpace(item.Get("result").String())
			if res == "" {
				continue
			}
			entry := imageCallResult{
				Result:        res,
				RevisedPrompt: strings.TrimSpace(item.Get("revised_prompt").String()),
				OutputFormat:  strings.TrimSpace(item.Get("output_format").String()),
				Size:          strings.TrimSpace(item.Get("size").String()),
				Background:    strings.TrimSpace(item.Get("background").String()),
				Quality:       strings.TrimSpace(item.Get("quality").String()),
			}
			if len(results) == 0 {
				firstMeta = entry
			}
			results = append(results, entry)
		}
	}

	if usage := gjson.GetBytes(payload, "response.tool_usage.image_gen"); usage.Exists() && usage.IsObject() {
		usageRaw = []byte(usage.Raw)
	}

	return results, createdAt, usageRaw, firstMeta, nil
}

func extractImageFromResponsesOutputItemDone(payload []byte) (imageCallResult, bool) {
	if gjson.GetBytes(payload, "type").String() != "response.output_item.done" {
		return imageCallResult{}, false
	}

	item := gjson.GetBytes(payload, "item")
	if !item.Exists() || item.Get("type").String() != "image_generation_call" {
		return imageCallResult{}, false
	}

	res := strings.TrimSpace(item.Get("result").String())
	if res == "" {
		return imageCallResult{}, false
	}

	return imageCallResult{
		Result:        res,
		RevisedPrompt: strings.TrimSpace(item.Get("revised_prompt").String()),
		OutputFormat:  strings.TrimSpace(item.Get("output_format").String()),
		Size:          strings.TrimSpace(item.Get("size").String()),
		Background:    strings.TrimSpace(item.Get("background").String()),
		Quality:       strings.TrimSpace(item.Get("quality").String()),
	}, true
}

func (r *imageStreamResults) Add(item imageCallResult) {
	if strings.TrimSpace(item.Result) == "" {
		return
	}
	if r.seen == nil {
		r.seen = make(map[string]struct{})
	}

	key := item.OutputFormat + "\x00" + item.Result
	if _, exists := r.seen[key]; exists {
		return
	}
	r.seen[key] = struct{}{}

	if len(r.items) == 0 {
		r.firstMeta = item
	}
	r.items = append(r.items, item)
}

func (r *imageStreamResults) AddMany(items []imageCallResult) {
	for _, item := range items {
		r.Add(item)
	}
}

func firstNonEmptyOutputFormat(results []imageCallResult) string {
	for _, item := range results {
		if strings.TrimSpace(item.OutputFormat) != "" {
			return strings.TrimSpace(item.OutputFormat)
		}
	}
	return ""
}

func startImageJSONShell(c *gin.Context, interval time.Duration) *imageJSONShellWriter {
	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		return nil
	}
	if interval <= 0 {
		interval = 5 * time.Second
	}

	c.Header("Content-Type", "application/json")
	c.Header("X-Accel-Buffering", "no")
	_, _ = c.Writer.Write([]byte(imageJSONShellPrefix))
	_, _ = c.Writer.Write([]byte(strings.Repeat(".", imageJSONShellBurstLen)))
	flusher.Flush()

	writer := &imageJSONShellWriter{
		writer:  c.Writer,
		flusher: flusher,
		stopCh:  make(chan struct{}),
		doneCh:  make(chan struct{}),
	}

	go func() {
		defer close(writer.doneCh)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-writer.stopCh:
				return
			case <-ticker.C:
				_, _ = writer.writer.Write([]byte("."))
				writer.flusher.Flush()
			}
		}
	}()

	return writer
}

func (w *imageJSONShellWriter) finishWithObject(obj []byte) error {
	if w == nil {
		return nil
	}
	close(w.stopCh)
	<-w.doneCh
	return appendObjectToJSONShell(w.writer, obj, w.flusher)
}

func (w *imageJSONShellWriter) finishWithError(errMsg *interfaces.ErrorMessage) error {
	if w == nil {
		return nil
	}
	close(w.stopCh)
	<-w.doneCh
	payload, err := buildImageShellErrorObject(errMsg)
	if err != nil {
		return err
	}
	return appendObjectToJSONShell(w.writer, payload, w.flusher)
}

func appendObjectToJSONShell(writer io.Writer, obj []byte, flusher http.Flusher) error {
	trimmed := bytes.TrimSpace(obj)
	if !json.Valid(trimmed) || len(trimmed) < 2 || trimmed[0] != '{' || trimmed[len(trimmed)-1] != '}' {
		return fmt.Errorf("invalid JSON object for shell append")
	}
	inner := bytes.TrimSpace(trimmed[1 : len(trimmed)-1])
	if len(inner) == 0 {
		_, err := writer.Write([]byte(`"}`))
		if flusher != nil {
			flusher.Flush()
		}
		return err
	}
	if _, err := writer.Write([]byte(`",`)); err != nil {
		return err
	}
	if _, err := writer.Write(inner); err != nil {
		return err
	}
	_, err := writer.Write([]byte(`}`))
	if flusher != nil {
		flusher.Flush()
	}
	return err
}

func buildImageShellErrorObject(errMsg *interfaces.ErrorMessage) ([]byte, error) {
	status := http.StatusBadGateway
	message := "image generation failed"
	if errMsg != nil {
		if errMsg.StatusCode > 0 {
			status = errMsg.StatusCode
		}
		if errMsg.Error != nil && strings.TrimSpace(errMsg.Error.Error()) != "" {
			message = errMsg.Error.Error()
		}
	}
	body := handlers.BuildErrorResponseBody(status, message)
	if !json.Valid(body) {
		return nil, fmt.Errorf("invalid error response body")
	}
	return body, nil
}

func buildImagesAPIResponse(results []imageCallResult, createdAt int64, usageRaw []byte, firstMeta imageCallResult, responseFormat string) ([]byte, error) {
	out := []byte(`{"created":0,"data":[]}`)
	out, _ = sjson.SetBytes(out, "created", createdAt)

	responseFormat = strings.ToLower(strings.TrimSpace(responseFormat))
	if responseFormat == "" {
		responseFormat = "b64_json"
	}

	for _, img := range results {
		item := []byte(`{}`)
		if responseFormat == "url" {
			mt := mimeTypeFromOutputFormat(img.OutputFormat)
			item, _ = sjson.SetBytes(item, "url", "data:"+mt+";base64,"+img.Result)
		} else {
			item, _ = sjson.SetBytes(item, "b64_json", img.Result)
		}
		if img.RevisedPrompt != "" {
			item, _ = sjson.SetBytes(item, "revised_prompt", img.RevisedPrompt)
		}
		out, _ = sjson.SetRawBytes(out, "data.-1", item)
	}

	if firstMeta.Background != "" {
		out, _ = sjson.SetBytes(out, "background", firstMeta.Background)
	}
	if firstMeta.OutputFormat != "" {
		out, _ = sjson.SetBytes(out, "output_format", firstMeta.OutputFormat)
	}
	if firstMeta.Quality != "" {
		out, _ = sjson.SetBytes(out, "quality", firstMeta.Quality)
	}
	if firstMeta.Size != "" {
		out, _ = sjson.SetBytes(out, "size", firstMeta.Size)
	}

	if len(usageRaw) > 0 && json.Valid(usageRaw) {
		out, _ = sjson.SetRawBytes(out, "usage", usageRaw)
	}

	return out, nil
}

func (h *OpenAIAPIHandler) streamImagesFromResponses(c *gin.Context, responsesReq []byte, responseFormat string, streamPrefix string) {
	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		c.JSON(http.StatusInternalServerError, handlers.ErrorResponse{
			Error: handlers.ErrorDetail{
				Message: "Streaming not supported",
				Type:    "server_error",
			},
		})
		return
	}
	startedAt := time.Now()
	logger := imageLogEntry(c).WithFields(log.Fields{
		"mode":            "stream",
		"stream_prefix":   streamPrefix,
		"main_model":      defaultImagesMainModel,
		"response_format": responseFormat,
	})
	logger.Info("image upstream stream started")

	cliCtx, cliCancel := h.GetContextWithCancel(h, c, context.Background())
	dataChan, errChan := h.ExecuteStreamWithAuthManager(cliCtx, "openai-response", defaultImagesMainModel, responsesReq, "")

	setSSEHeaders := func() {
		c.Header("Content-Type", "text/event-stream")
		c.Header("Cache-Control", "no-cache")
		c.Header("Connection", "keep-alive")
		c.Header("Access-Control-Allow-Origin", "*")
	}

	writeEvent := func(eventName string, dataJSON []byte) {
		if strings.TrimSpace(eventName) != "" {
			_, _ = fmt.Fprintf(c.Writer, "event: %s\n", eventName)
		}
		_, _ = fmt.Fprintf(c.Writer, "data: %s\n\n", string(dataJSON))
		flusher.Flush()
	}

	// Peek for first chunk/error so we can still return a JSON error body.
	for {
		select {
		case <-c.Request.Context().Done():
			cliCancel(c.Request.Context().Err())
			return
		case errMsg, ok := <-errChan:
			if !ok {
				errChan = nil
				continue
			}
			fields := log.Fields{
				"duration_ms": time.Since(startedAt).Milliseconds(),
			}
			if errMsg != nil {
				fields["status"] = errMsg.StatusCode
				if errMsg.Error != nil {
					fields["error"] = errMsg.Error.Error()
				}
			}
			logger.WithFields(fields).Warn("image upstream stream failed before first event")
			h.WriteErrorResponse(c, errMsg)
			if errMsg != nil {
				cliCancel(errMsg.Error)
			} else {
				cliCancel(nil)
			}
			return
		case chunk, ok := <-dataChan:
			if !ok {
				setSSEHeaders()
				_, _ = c.Writer.Write([]byte("\n"))
				flusher.Flush()
				cliCancel(nil)
				return
			}

			setSSEHeaders()

			h.forwardImagesStream(cliCtx, c, flusher, func(err error) { cliCancel(err) }, dataChan, errChan, chunk, responseFormat, streamPrefix, writeEvent, logger, startedAt)
			return
		}
	}
}

func (h *OpenAIAPIHandler) forwardImagesStream(ctx context.Context, c *gin.Context, flusher http.Flusher, cancel func(error), data <-chan []byte, errs <-chan *interfaces.ErrorMessage, firstChunk []byte, responseFormat string, streamPrefix string, writeEvent func(string, []byte), logger *log.Entry, startedAt time.Time) {
	acc := &sseFrameAccumulator{}

	responseFormat = strings.ToLower(strings.TrimSpace(responseFormat))
	if responseFormat == "" {
		responseFormat = "b64_json"
	}

	emitError := func(errMsg *interfaces.ErrorMessage) {
		if errMsg == nil {
			return
		}
		status := http.StatusInternalServerError
		if errMsg.StatusCode > 0 {
			status = errMsg.StatusCode
		}
		errText := http.StatusText(status)
		if errMsg.Error != nil && strings.TrimSpace(errMsg.Error.Error()) != "" {
			errText = errMsg.Error.Error()
		}
		if logger != nil {
			logger.WithFields(log.Fields{
				"duration_ms": time.Since(startedAt).Milliseconds(),
				"status":      status,
				"error":       errText,
			}).Warn("image upstream stream emitted error")
		}
		body := handlers.BuildErrorResponseBody(status, errText)
		writeEvent("error", body)
	}

	processFrame := func(frame []byte) (done bool) {
		for _, line := range bytes.Split(frame, []byte("\n")) {
			trimmed := bytes.TrimSpace(bytes.TrimRight(line, "\r"))
			if len(trimmed) == 0 || !bytes.HasPrefix(trimmed, []byte("data:")) {
				continue
			}
			payload := bytes.TrimSpace(trimmed[len("data:"):])
			if len(payload) == 0 || bytes.Equal(payload, []byte("[DONE]")) || !json.Valid(payload) {
				continue
			}

			switch gjson.GetBytes(payload, "type").String() {
			case "response.image_generation_call.partial_image":
				b64 := strings.TrimSpace(gjson.GetBytes(payload, "partial_image_b64").String())
				if b64 == "" {
					continue
				}
				outputFormat := strings.TrimSpace(gjson.GetBytes(payload, "output_format").String())
				index := gjson.GetBytes(payload, "partial_image_index").Int()
				eventName := streamPrefix + ".partial_image"
				data := []byte(`{"type":"","partial_image_index":0}`)
				data, _ = sjson.SetBytes(data, "type", eventName)
				data, _ = sjson.SetBytes(data, "partial_image_index", index)
				if responseFormat == "url" {
					mt := mimeTypeFromOutputFormat(outputFormat)
					data, _ = sjson.SetBytes(data, "url", "data:"+mt+";base64,"+b64)
				} else {
					data, _ = sjson.SetBytes(data, "b64_json", b64)
				}
				writeEvent(eventName, data)
			case "response.completed":
				results, _, usageRaw, _, err := extractImagesFromResponsesCompleted(payload)
				if err != nil {
					emitError(&interfaces.ErrorMessage{StatusCode: http.StatusBadGateway, Error: err})
					return true
				}
				if len(results) == 0 {
					emitError(&interfaces.ErrorMessage{StatusCode: http.StatusBadGateway, Error: fmt.Errorf("upstream did not return image output")})
					return true
				}
				if logger != nil {
					logger.WithFields(log.Fields{
						"duration_ms":   time.Since(startedAt).Milliseconds(),
						"image_count":   len(results),
						"output_format": firstNonEmptyOutputFormat(results),
						"usage_images":  gjson.GetBytes(usageRaw, "num_images").Int(),
					}).Info("image upstream stream completed")
				}
				eventName := streamPrefix + ".completed"
				for _, img := range results {
					data := []byte(`{"type":""}`)
					data, _ = sjson.SetBytes(data, "type", eventName)
					if responseFormat == "url" {
						mt := mimeTypeFromOutputFormat(img.OutputFormat)
						data, _ = sjson.SetBytes(data, "url", "data:"+mt+";base64,"+img.Result)
					} else {
						data, _ = sjson.SetBytes(data, "b64_json", img.Result)
					}
					if len(usageRaw) > 0 && json.Valid(usageRaw) {
						data, _ = sjson.SetRawBytes(data, "usage", usageRaw)
					}
					writeEvent(eventName, data)
				}
				return true
			}
		}
		return false
	}

	for _, frame := range acc.AddChunk(firstChunk) {
		if processFrame(frame) {
			cancel(nil)
			return
		}
	}

	for {
		select {
		case <-c.Request.Context().Done():
			cancel(c.Request.Context().Err())
			return
		case errMsg, ok := <-errs:
			if ok && errMsg != nil {
				emitError(errMsg)
				cancel(errMsg.Error)
				return
			}
			errs = nil
		case chunk, ok := <-data:
			if !ok {
				for _, frame := range acc.Flush() {
					if processFrame(frame) {
						cancel(nil)
						return
					}
				}
				cancel(nil)
				return
			}
			for _, frame := range acc.AddChunk(chunk) {
				if processFrame(frame) {
					cancel(nil)
					return
				}
			}
		}
	}
}
