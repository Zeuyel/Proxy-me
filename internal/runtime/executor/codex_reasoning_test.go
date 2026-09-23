package executor

import (
	"testing"

	"github.com/tidwall/gjson"
)

func TestStripCodexReasoningItems(t *testing.T) {
	input := []byte(`{"model":"gpt-5.2","input":[{"type":"reasoning","id":"rs_stale","encrypted_content":"invalid"},{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]},{"type":"function_call","call_id":"call_1"}]}`)

	got := stripCodexReasoningItems(input)
	items := gjson.GetBytes(got, "input").Array()
	if len(items) != 2 {
		t.Fatalf("input item count = %d, want 2", len(items))
	}
	if items[0].Get("type").String() != "message" {
		t.Fatalf("first item type = %q, want message", items[0].Get("type").String())
	}
	if items[1].Get("type").String() != "function_call" {
		t.Fatalf("second item type = %q, want function_call", items[1].Get("type").String())
	}
}

func TestIsInvalidCodexEncryptedContent(t *testing.T) {
	if !isInvalidCodexEncryptedContent(400, []byte(`{"error":{"code":"invalid_encrypted_content"}}`)) {
		t.Fatal("expected invalid encrypted content to be detected")
	}
	if isInvalidCodexEncryptedContent(500, []byte(`{"error":{"code":"invalid_encrypted_content"}}`)) {
		t.Fatal("unexpected detection for non-400 response")
	}
}
