package agent

import (
	"testing"

	"github.com/itsivag/suprclaw/pkg/providers"
)

func TestPromptCacheBreakDetectorAnalyzeTracksState(t *testing.T) {
	detector := NewPromptCacheBreakDetector()
	defs := []providers.ToolDefinition{testToolDef("read_file", "read")}
	snapshot := NewPromptStateSnapshot(
		"openai",
		"sys",
		defs,
		"gpt-5.4",
		map[string]any{"prompt_cache_key": "a"},
		0,
		0,
	)

	_, err := detector.Analyze(
		"agent:session",
		snapshot,
		&providers.UsageInfo{PromptTokens: 1000, CompletionTokens: 50, TotalTokens: 1050},
		PromptMutators{},
	)
	if err != nil {
		t.Fatalf("Analyze returned error: %v", err)
	}
	if len(detector.records) != 1 {
		t.Fatalf("expected detector to track one record")
	}

	// Large prompt token jump should still keep state consistent.
	_, err = detector.Analyze(
		"agent:session",
		snapshot,
		&providers.UsageInfo{PromptTokens: 5000, CompletionTokens: 75, TotalTokens: 5075},
		PromptMutators{},
	)
	if err != nil {
		t.Fatalf("Analyze returned error: %v", err)
	}
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

func TestPromptCacheBreakDetectorAnalyzeUsageContractViolation(t *testing.T) {
	detector := NewPromptCacheBreakDetector()
	snapshot := NewPromptStateSnapshot("openai", "sys", nil, "gpt-5.4", nil, 0, 0)

	_, err := detector.Analyze("k", snapshot, nil, PromptMutators{})
	if err == nil {
		t.Fatalf("expected usage contract violation")
	}
	if _, ok := err.(*PromptUsageContractViolationError); !ok {
		t.Fatalf("expected PromptUsageContractViolationError, got %T", err)
	}
}
