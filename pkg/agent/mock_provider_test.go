package agent

import (
	"context"

	"github.com/itsivag/suprclaw/pkg/providers"
	providerscommon "github.com/itsivag/suprclaw/pkg/providers/common"
)

type mockProvider struct{}

func (m *mockProvider) Chat(
	ctx context.Context,
	messages []providers.Message,
	tools []providers.ToolDefinition,
	model string,
	opts map[string]any,
) (*providers.LLMResponse, error) {
	return &providers.LLMResponse{
		Content:   "Mock response",
		ToolCalls: []providers.ToolCall{},
		Usage: &providers.UsageInfo{
			PromptTokens:     10,
			CompletionTokens: 5,
			TotalTokens:      15,
		},
	}, nil
}

func (m *mockProvider) GetDefaultModel() string {
	return "mock-model"
}

func (m *mockProvider) CountTokens(
	ctx context.Context,
	messages []providers.Message,
	tools []providers.ToolDefinition,
	model string,
	opts map[string]any,
) (int, error) {
	_ = ctx
	return providerscommon.EstimateTokenCount(messages, tools, model, opts), nil
}
