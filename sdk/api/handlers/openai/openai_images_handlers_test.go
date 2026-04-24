package openai

import (
	"context"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/interfaces"
	"github.com/tidwall/gjson"
)

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

func TestCollectImagesFromResponsesStreamUsesOutputItemDoneImageWhenCompletedOutputIsEmpty(t *testing.T) {
	ctx := context.Background()
	data := make(chan []byte, 3)
	errs := make(chan *interfaces.ErrorMessage)

	data <- []byte("data: {\"type\":\"response.output_item.done\",\"item\":{\"id\":\"ig_123\",\"type\":\"image_generation_call\",\"output_format\":\"png\",\"result\":\"aGVsbG8=\"}}\n\n")
	data <- []byte("data: {\"type\":\"response.completed\",\"response\":{\"created_at\":1700000000,\"output\":[],\"tool_usage\":{\"image_gen\":{\"num_images\":1}}}}\n\n")
	close(data)
	close(errs)

	out, errMsg := collectImagesFromResponsesStream(ctx, data, errs, "b64_json")
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
}

func TestCollectImagesFromResponsesStreamDedupesOutputItemDoneAndCompletedImage(t *testing.T) {
	ctx := context.Background()
	data := make(chan []byte, 3)
	errs := make(chan *interfaces.ErrorMessage)

	data <- []byte("data: {\"type\":\"response.output_item.done\",\"item\":{\"id\":\"ig_123\",\"type\":\"image_generation_call\",\"output_format\":\"png\",\"result\":\"aGVsbG8=\"}}\n\n")
	data <- []byte("data: {\"type\":\"response.completed\",\"response\":{\"created_at\":1700000000,\"output\":[{\"type\":\"image_generation_call\",\"output_format\":\"png\",\"result\":\"aGVsbG8=\"}]}}\n\n")
	close(data)
	close(errs)

	out, errMsg := collectImagesFromResponsesStream(ctx, data, errs, "b64_json")
	if errMsg != nil {
		t.Fatalf("unexpected error: %+v", errMsg)
	}

	if got := len(gjson.GetBytes(out, "data").Array()); got != 1 {
		t.Fatalf("data length = %d, want 1; out=%s", got, out)
	}
}
