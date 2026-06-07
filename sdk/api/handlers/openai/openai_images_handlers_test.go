package openai

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/interfaces"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/registry"
	"github.com/router-for-me/CLIProxyAPI/v6/sdk/api/handlers"
	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	coreexecutor "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/executor"
	sdkconfig "github.com/router-for-me/CLIProxyAPI/v6/sdk/config"
	"github.com/tidwall/gjson"
)

type blockingImageStreamExecutor struct {
	started chan struct{}
}

func (e *blockingImageStreamExecutor) Identifier() string { return "codex" }

func (e *blockingImageStreamExecutor) Execute(context.Context, *coreauth.Auth, coreexecutor.Request, coreexecutor.Options) (coreexecutor.Response, error) {
	return coreexecutor.Response{}, errors.New("not implemented")
}

func (e *blockingImageStreamExecutor) ExecuteStream(ctx context.Context, _ *coreauth.Auth, _ coreexecutor.Request, _ coreexecutor.Options) (<-chan coreexecutor.StreamChunk, error) {
	select {
	case <-e.started:
	default:
		close(e.started)
	}
	<-ctx.Done()
	return nil, ctx.Err()
}

func (e *blockingImageStreamExecutor) Refresh(_ context.Context, auth *coreauth.Auth) (*coreauth.Auth, error) {
	return auth, nil
}

func (e *blockingImageStreamExecutor) CountTokens(context.Context, *coreauth.Auth, coreexecutor.Request, coreexecutor.Options) (coreexecutor.Response, error) {
	return coreexecutor.Response{}, errors.New("not implemented")
}

func (e *blockingImageStreamExecutor) HttpRequest(context.Context, *coreauth.Auth, *http.Request) (*http.Response, error) {
	return nil, errors.New("not implemented")
}

func TestBuildImagesResponsesRequestUsesNativeImageGenerationTool(t *testing.T) {
	req := buildImagesResponsesRequest("draw a cat", nil, []byte(`{"type":"image_generation","action":"generate","model":"gpt-image-2","output_format":"png"}`))

	if got := gjson.GetBytes(req, "tool_choice.type").String(); got != "image_generation" {
		t.Fatalf("tool_choice.type = %q, want image_generation", got)
	}
	if got := gjson.GetBytes(req, "tools.0.type").String(); got != "image_generation" {
		t.Fatalf("tools.0.type = %q, want image_generation", got)
	}
	if got := gjson.GetBytes(req, "tools.0.model").String(); got != "gpt-image-2" {
		t.Fatalf("tools.0.model = %q, want gpt-image-2", got)
	}
	if got := gjson.GetBytes(req, "tools.0.action").String(); got != "generate" {
		t.Fatalf("tools.0.action = %q, want generate", got)
	}
	if got := gjson.GetBytes(req, "tools.0.output_format").String(); got != "png" {
		t.Fatalf("tools.0.output_format = %q, want png", got)
	}
}

func TestBuildImagesResponsesRequestNormalizesGenericImageDataURL(t *testing.T) {
	pngHeader := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}
	raw := "data:image;base64," + base64.StdEncoding.EncodeToString(pngHeader)

	req := buildImagesResponsesRequest("edit this", []string{raw}, []byte(`{"type":"image_generation","action":"edit","model":"gpt-image-2","output_format":"png"}`))

	if got := gjson.GetBytes(req, "input.0.content.1.image_url").String(); got != "data:image/png;base64,"+base64.StdEncoding.EncodeToString(pngHeader) {
		t.Fatalf("input image_url = %q, want normalized image/png data url", got)
	}
}

func TestBuildImagesResponsesRequestPropagatesModelPrefix(t *testing.T) {
	req := buildImagesResponsesRequest("draw a cat", nil, []byte(`{"type":"image_generation","action":"generate","model":"team-a/gpt-image-2"}`))

	if got := gjson.GetBytes(req, "model").String(); got != "team-a/gpt-5.4" {
		t.Fatalf("model = %q, want team-a/gpt-5.4", got)
	}
}

func TestBuildImageShellErrorObjectIsImagesAPICompatible(t *testing.T) {
	out, err := buildImageShellErrorObject(&interfaces.ErrorMessage{
		StatusCode: 502,
		Error:      context.Canceled,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := gjson.GetBytes(out, "data"); !got.IsArray() {
		t.Fatalf("data must be an array for OpenAI Images compatibility; out=%s", out)
	}
	if got := gjson.GetBytes(out, "error.message").String(); got == "" {
		t.Fatalf("error.message is empty; out=%s", out)
	}
}

func TestCollectImagesFromResponsesStartsShellWhileStreamDispatchBlocks(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor := &blockingImageStreamExecutor{started: make(chan struct{})}
	manager := coreauth.NewManager(nil, nil, nil)
	manager.RegisterExecutor(executor)
	auth := &coreauth.Auth{ID: "image-shell-auth", Provider: "codex", Status: coreauth.StatusActive}
	if _, err := manager.Register(context.Background(), auth); err != nil {
		t.Fatalf("register auth: %v", err)
	}
	registry.GetGlobalRegistry().RegisterClient(auth.ID, auth.Provider, []*registry.ModelInfo{{ID: defaultImagesMainModel}})
	t.Cleanup(func() {
		registry.GetGlobalRegistry().UnregisterClient(auth.ID)
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{NonStreamKeepAliveInterval: 1}, manager)
	h := NewOpenAIAPIHandler(base)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	reqCtx, cancel := context.WithCancel(context.Background())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil).WithContext(reqCtx)

	done := make(chan struct{})
	go func() {
		defer close(done)
		h.collectImagesFromResponses(c, []byte(`{"stream":true}`), "b64_json")
	}()

	select {
	case <-executor.started:
	case <-time.After(time.Second):
		cancel()
		<-done
		t.Fatalf("stream executor did not start")
	}

	deadline := time.After(3 * time.Second)
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-deadline:
			cancel()
			<-done
			t.Fatalf("expected json shell prefix before blocked stream dispatch returns, got body head %q", recorder.Body.String())
		case <-tick.C:
			if strings.Contains(recorder.Body.String(), "_proxy_me_progress") {
				cancel()
				<-done
				return
			}
		}
	}
}

func TestCollectImagesFromResponsesStreamUsesOutputItemDoneImageWhenCompletedOutputIsEmpty(t *testing.T) {
	ctx := context.Background()
	data := make(chan []byte, 3)
	errs := make(chan *interfaces.ErrorMessage)

	data <- []byte("data: {\"type\":\"response.output_item.done\",\"item\":{\"id\":\"ig_123\",\"type\":\"image_generation_call\",\"output_format\":\"png\",\"result\":\"aGVsbG8=\"}}\n\n")
	data <- []byte("data: {\"type\":\"response.completed\",\"response\":{\"created_at\":1700000000,\"output\":[],\"tool_usage\":{\"image_gen\":{\"num_images\":1}}}}\n\n")
	close(data)
	close(errs)

	out, summary, errMsg := collectImagesFromResponsesStream(ctx, data, errs, "b64_json")
	if errMsg != nil {
		t.Fatalf("unexpected error: %+v", errMsg)
	}

	if got := gjson.GetBytes(out, "data.0.b64_json").String(); got != "aGVsbG8=" {
		t.Fatalf("data.0.b64_json = %q, want aGVsbG8=; out=%s", got, out)
	}
	if got := gjson.GetBytes(out, "output_format").String(); got != "png" {
		t.Fatalf("output_format = %q, want png; out=%s", got, out)
	}
	if got := gjson.GetBytes(out, "usage.num_images").Int(); got != 1 {
		t.Fatalf("usage.num_images = %d, want 1; out=%s", got, out)
	}
	if summary.ImageCount != 1 || summary.UsageImages != 1 || summary.OutputFormat != "png" {
		t.Fatalf("unexpected summary: %+v", summary)
	}
}

func TestCollectImagesFromResponsesStreamDedupesOutputItemDoneAndCompletedImage(t *testing.T) {
	ctx := context.Background()
	data := make(chan []byte, 3)
	errs := make(chan *interfaces.ErrorMessage)

	data <- []byte("data: {\"type\":\"response.output_item.done\",\"item\":{\"id\":\"ig_123\",\"type\":\"image_generation_call\",\"output_format\":\"png\",\"result\":\"aGVsbG8=\"}}\n\n")
	data <- []byte("data: {\"type\":\"response.completed\",\"response\":{\"created_at\":1700000000,\"output\":[{\"type\":\"image_generation_call\",\"output_format\":\"png\",\"result\":\"aGVsbG8=\"}]}}\n\n")
	close(data)
	close(errs)

	out, summary, errMsg := collectImagesFromResponsesStream(ctx, data, errs, "b64_json")
	if errMsg != nil {
		t.Fatalf("unexpected error: %+v", errMsg)
	}

	if got := len(gjson.GetBytes(out, "data").Array()); got != 1 {
		t.Fatalf("data length = %d, want 1; out=%s", got, out)
	}
	if summary.ImageCount != 1 || summary.OutputFormat != "png" {
		t.Fatalf("unexpected summary: %+v", summary)
	}
}

func TestCollectImagesFromResponsesStreamReturnsOutputItemDoneOnDisconnect(t *testing.T) {
	ctx := context.Background()
	data := make(chan []byte, 1)
	errs := make(chan *interfaces.ErrorMessage)

	data <- []byte("data: {\"type\":\"response.output_item.done\",\"item\":{\"id\":\"ig_123\",\"type\":\"image_generation_call\",\"output_format\":\"png\",\"result\":\"aGVsbG8=\"}}\n\n")
	close(data)
	close(errs)

	out, summary, errMsg := collectImagesFromResponsesStream(ctx, data, errs, "b64_json")
	if errMsg != nil {
		t.Fatalf("unexpected error: %+v", errMsg)
	}

	if got := gjson.GetBytes(out, "data.0.b64_json").String(); got != "aGVsbG8=" {
		t.Fatalf("data.0.b64_json = %q, want aGVsbG8=; out=%s", got, out)
	}
	if got := gjson.GetBytes(out, "error").Exists(); got {
		t.Fatalf("unexpected error object in fallback response: %s", out)
	}
	if summary.ImageCount != 1 || summary.OutputFormat != "png" {
		t.Fatalf("unexpected summary: %+v", summary)
	}
}

func TestCollectImagesFromResponsesStreamReturnsPartialImageOnDisconnect(t *testing.T) {
	ctx := context.Background()
	data := make(chan []byte, 2)
	errs := make(chan *interfaces.ErrorMessage)

	data <- []byte("data: {\"type\":\"response.image_generation_call.partial_image\",\"item_id\":\"ig_123\",\"output_format\":\"png\",\"partial_image_b64\":\"cGFydGlhbDE=\",\"partial_image_index\":0}\n\n")
	data <- []byte("data: {\"type\":\"response.image_generation_call.partial_image\",\"item_id\":\"ig_123\",\"output_format\":\"png\",\"partial_image_b64\":\"cGFydGlhbDI=\",\"partial_image_index\":1}\n\n")
	close(data)
	close(errs)

	out, summary, errMsg := collectImagesFromResponsesStream(ctx, data, errs, "b64_json")
	if errMsg != nil {
		t.Fatalf("unexpected error: %+v", errMsg)
	}

	if got := gjson.GetBytes(out, "data.0.b64_json").String(); got != "cGFydGlhbDI=" {
		t.Fatalf("data.0.b64_json = %q, want latest partial; out=%s", got, out)
	}
	if got := gjson.GetBytes(out, "output_format").String(); got != "png" {
		t.Fatalf("output_format = %q, want png; out=%s", got, out)
	}
	if summary.ImageCount != 1 || summary.OutputFormat != "png" {
		t.Fatalf("unexpected summary: %+v", summary)
	}
}
