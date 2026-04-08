package providers

import (
	"strings"

	providerscommon "github.com/itsivag/suprclaw/pkg/providers/common"
)

func usageInfoIsValid(usage *UsageInfo) bool {
	if usage == nil {
		return false
	}
	if usage.PromptTokens <= 0 || usage.CompletionTokens < 0 || usage.TotalTokens <= 0 {
		return false
	}
	return usage.TotalTokens == usage.PromptTokens+usage.CompletionTokens
}

func ensureUsageContract(
	resp *LLMResponse,
	messages []Message,
	tools []ToolDefinition,
	model string,
	options map[string]any,
) {
	if resp == nil {
		return
	}
	if usageInfoIsValid(resp.Usage) {
		return
	}

	promptTokens := providerscommon.EstimateTokenCount(messages, tools, model, options)
	if promptTokens <= 0 {
		promptTokens = 1
	}

	completionContent := strings.TrimSpace(resp.Content)
	if strings.TrimSpace(resp.ReasoningContent) != "" {
		if completionContent == "" {
			completionContent = strings.TrimSpace(resp.ReasoningContent)
		} else {
			completionContent = completionContent + "\n" + strings.TrimSpace(resp.ReasoningContent)
		}
	}

	completionTokens := 0
	if completionContent != "" {
		completionTokens = providerscommon.EstimateTokenCount(
			[]Message{{Role: "assistant", Content: completionContent}},
			nil,
			model,
			nil,
		)
		if completionTokens < 0 {
			completionTokens = 0
		}
	}

	resp.Usage = &UsageInfo{
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		TotalTokens:      promptTokens + completionTokens,
	}
}
