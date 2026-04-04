package agent

import (
	"strings"
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

func TestNormalizeContextGuard_DefaultsPrecheckTriggerRatio(t *testing.T) {
	guard := normalizeContextGuard(config.ContextGuardConfig{})
	if guard.PrecheckTriggerRatio != 0.85 {
		t.Fatalf("PrecheckTriggerRatio = %v, want 0.85", guard.PrecheckTriggerRatio)
	}

	invalid := normalizeContextGuard(config.ContextGuardConfig{
		Enabled:              true,
		PrecheckTriggerRatio: 2,
	})
	if invalid.PrecheckTriggerRatio != 0.85 {
		t.Fatalf("invalid PrecheckTriggerRatio normalized = %v, want 0.85", invalid.PrecheckTriggerRatio)
	}
}

func TestDecideContextBudgetPrecheck(t *testing.T) {
	al := &AgentLoop{}
	agent := &AgentInstance{
		MaxTokens:     512,
		ContextWindow: 4096,
		ContextGuard: config.ContextGuardConfig{
			Enabled:                true,
			SafetyMarginTokens:     64,
			TargetInputRatio:       0.78,
			PrecheckTriggerRatio:   0.85,
			EmergencyInputRatio:    0.60,
			MaxCompactionPasses:    3,
			PreserveRecentMessages: 4,
		},
	}
	llmOpts := map[string]any{"max_tokens": 512}

	belowThreshold := []providers.Message{
		{
			Role:    "assistant",
			Content: "anchor",
			Usage: &providers.UsageInfo{
				PromptTokens:     350,
				CompletionTokens: 50,
				TotalTokens:      400,
			},
		},
		{Role: "user", Content: "small"},
	}
	decision := al.decideContextBudgetPrecheck(agent, belowThreshold, llmOpts, nil, false)
	if decision.ShouldRunPrecheck {
		t.Fatal("ShouldRunPrecheck = true, want false for anchored payload below threshold")
	}

	atOrAboveThreshold := []providers.Message{
		{
			Role:    "assistant",
			Content: "anchor",
			Usage: &providers.UsageInfo{
				PromptTokens:     2600,
				CompletionTokens: 200,
				TotalTokens:      2800,
			},
		},
		{Role: "user", Content: strings.Repeat("X", 2000)},
	}
	decision = al.decideContextBudgetPrecheck(agent, atOrAboveThreshold, llmOpts, nil, false)
	if !decision.ShouldRunPrecheck {
		t.Fatal("ShouldRunPrecheck = false, want true at/above threshold")
	}

	noAnchor := []providers.Message{
		{Role: "user", Content: strings.Repeat("Y", 80)},
	}
	decision = al.decideContextBudgetPrecheck(agent, noAnchor, llmOpts, nil, false)
	if !decision.ShouldRunPrecheck {
		t.Fatal("ShouldRunPrecheck = false, want true when no usage anchor exists")
	}

	decision = al.decideContextBudgetPrecheck(agent, belowThreshold, llmOpts, nil, true)
	if !decision.ShouldRunPrecheck {
		t.Fatal("ShouldRunPrecheck = false, want true when forceEmergency is set")
	}
}
