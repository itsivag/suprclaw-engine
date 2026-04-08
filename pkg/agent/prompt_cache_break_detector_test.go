package agent

import (
	"testing"

	"github.com/itsivag/suprclaw/pkg/providers"
)

func TestPromptCacheBreakDetectorAnalyzeTracksState(t *testing.T) {
	detector := NewPromptCacheBreakDetector()
	defs := []providers.ToolDefinition{testToolDef("read_file", "read")}
	snapshot := NewPromptStateSnapshot("sys", defs, "gpt-5.4", map[string]any{"prompt_cache_key": "a"}, 0, 0)

	detector.Analyze("agent:session", snapshot, &providers.UsageInfo{PromptTokens: 1000}, PromptMutators{})
	if len(detector.records) != 1 {
		t.Fatalf("expected detector to track one record")
	}

	// Large prompt token jump should still keep state consistent.
	detector.Analyze("agent:session", snapshot, &providers.UsageInfo{PromptTokens: 5000}, PromptMutators{})
	rec, ok := detector.records["agent:session"]
	if !ok {
		t.Fatalf("expected detector record for key")
	}
	if rec.promptTokens != 5000 {
		t.Fatalf("expected prompt tokens to be updated, got %d", rec.promptTokens)
	}
}

func TestChangedToolSchemas(t *testing.T) {
	prev := map[string]string{"a": "1", "b": "2"}
	curr := map[string]string{"a": "1", "b": "3", "c": "9"}
	changed := changedToolSchemas(prev, curr)
	if len(changed) != 2 {
		t.Fatalf("expected two changed schema names, got %v", changed)
	}
	if changed[0] != "b" || changed[1] != "c" {
		t.Fatalf("unexpected changed tool list: %v", changed)
	}
}
