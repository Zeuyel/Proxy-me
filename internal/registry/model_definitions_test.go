package registry

import "testing"

func TestModelOverrideHeadersFromStaticDefinitions(t *testing.T) {
	const wantUA = "codex-tui/0.144.0 (Mac OS 26.5.1; arm64) iTerm.app/3.6.11 (codex-tui; 0.144.0)"

	got := ModelOverrideHeaders("gpt-5.6-luna")
	if got == nil {
		t.Fatal("ModelOverrideHeaders(gpt-5.6-luna) = nil, want headers")
	}
	if got["user-agent"] != wantUA {
		t.Fatalf("user-agent = %q, want %q", got["user-agent"], wantUA)
	}
	if got["originator"] != "codex-tui" {
		t.Fatalf("originator = %q, want codex-tui", got["originator"])
	}

	got["user-agent"] = "mutated"
	if next := ModelOverrideHeaders("gpt-5.6-luna"); next["user-agent"] != wantUA {
		t.Fatalf("ModelOverrideHeaders returned shared map, got %q", next["user-agent"])
	}
	if got := ModelOverrideHeaders("gpt-5.4"); got != nil {
		t.Fatalf("ModelOverrideHeaders(gpt-5.4) = %#v, want nil", got)
	}
}
