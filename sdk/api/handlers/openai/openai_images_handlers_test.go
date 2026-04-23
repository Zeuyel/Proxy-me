package openai

import (
	"testing"

	"github.com/tidwall/gjson"
)

func TestBuildImagesResponsesRequestUsesNativeImageGenerationTool(t *testing.T) {
	req := buildImagesResponsesRequest("draw a cat", nil, []byte(`{"type":"image_generation","output_format":"png"}`))

	if got := gjson.GetBytes(req, "tool_choice.type").String(); got != "image_generation" {
		t.Fatalf("tool_choice.type = %q, want image_generation", got)
	}
	if got := gjson.GetBytes(req, "tools.0.type").String(); got != "image_generation" {
		t.Fatalf("tools.0.type = %q, want image_generation", got)
	}
	if gjson.GetBytes(req, "tools.0.model").Exists() {
		t.Fatalf("tools.0.model must not be set for the native image_generation tool")
	}
	if gjson.GetBytes(req, "tools.0.action").Exists() {
		t.Fatalf("tools.0.action must not be set for the native image_generation tool")
	}
	if got := gjson.GetBytes(req, "tools.0.output_format").String(); got != "png" {
		t.Fatalf("tools.0.output_format = %q, want png", got)
	}
}
