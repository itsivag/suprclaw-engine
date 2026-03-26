package agent

import (
	"testing"

	"github.com/itsivag/suprclaw/pkg/config"
	"github.com/itsivag/suprclaw/pkg/providers"
)

func TestComputeContextBudget_UsesOutputAndSafety(t *testing.T) {
	guard := config.ContextGuardConfig{
		Enabled:                true,
		SafetyMarginTokens:     2048,
		TargetInputRatio:       0.78,
		EmergencyInputRatio:    0.60,
		MaxCompactionPasses:    3,
		PreserveRecentMessages: 6,
	}

	budget := computeContextBudget(196608, 4096, guard)

	if budget.EffectiveInputLimit != 190464 {
		t.Fatalf("EffectiveInputLimit = %d, want 190464", budget.EffectiveInputLimit)
	}
	base := 190464.0
	wantTarget := int(base * 0.78)
	if budget.TargetInputTokens != wantTarget {
		t.Fatalf("TargetInputTokens = %d, want %d", budget.TargetInputTokens, wantTarget)
	}
	wantEmergency := int(base * 0.60)
	if budget.EmergencyInputTokens != wantEmergency {
		t.Fatalf(
			"EmergencyInputTokens = %d, want %d",
			budget.EmergencyInputTokens,
			wantEmergency,
		)
	}
}

func TestEmergencyTrim_PreservesToolBoundaryAndLatestIntent(t *testing.T) {
	messages := []providers.Message{
		{Role: "system", Content: "system"},
		{Role: "user", Content: "older user"},
		{Role: "assistant", Content: "older assistant"},
		{Role: "assistant", ToolCalls: []providers.ToolCall{{ID: "tc-1", Name: "search"}}},
		{Role: "tool", ToolCallID: "tc-1", Content: "tool output"},
		{Role: "assistant", Content: "tool followup"},
		{Role: "user", Content: "latest intent"},
	}

	trimmed := emergencyTrimMessages(messages, 0.50, 2)
	if len(trimmed) == 0 || trimmed[0].Role != "system" {
		t.Fatalf("first message must be system, got: %+v", trimmed)
	}

	hasLatestUser := false
	hasToolBoundaryAssistant := false
	hasToolResult := false
	for _, m := range trimmed {
		if m.Role == "user" && m.Content == "latest intent" {
			hasLatestUser = true
		}
		if m.Role == "assistant" && len(m.ToolCalls) > 0 && m.ToolCalls[0].ID == "tc-1" {
			hasToolBoundaryAssistant = true
		}
		if m.Role == "tool" && m.ToolCallID == "tc-1" {
			hasToolResult = true
		}
	}

	if !hasLatestUser {
		t.Fatalf("trimmed history must preserve latest user intent: %+v", trimmed)
	}
	if !hasToolBoundaryAssistant || !hasToolResult {
		t.Fatalf("trimmed history must preserve latest tool coherence boundary: %+v", trimmed)
	}
}
