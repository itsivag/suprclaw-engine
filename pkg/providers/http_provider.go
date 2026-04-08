// SuprClaw - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: Elastic License 2.0
//
// Copyright (c) 2026 SuprClaw contributors

package providers

import (
	"context"
	"time"

	"github.com/itsivag/suprclaw/pkg/providers/openai_compat"
)

type HTTPProvider struct {
	delegate         *openai_compat.Provider
	supportsThinking bool
}

func NewHTTPProvider(apiKey, apiBase, proxy string) *HTTPProvider {
	return &HTTPProvider{
		delegate: openai_compat.NewProvider(apiKey, apiBase, proxy),
	}
}

func NewHTTPProviderWithMaxTokensField(apiKey, apiBase, proxy, maxTokensField string) *HTTPProvider {
	return NewHTTPProviderWithMaxTokensFieldAndRequestTimeout(apiKey, apiBase, proxy, maxTokensField, 0)
}

func NewHTTPProviderWithMaxTokensFieldAndRequestTimeout(
	apiKey, apiBase, proxy, maxTokensField string,
	requestTimeoutSeconds int,
) *HTTPProvider {
	return &HTTPProvider{
		delegate: openai_compat.NewProvider(
			apiKey,
			apiBase,
			proxy,
			openai_compat.WithMaxTokensField(maxTokensField),
			openai_compat.WithRequestTimeout(time.Duration(requestTimeoutSeconds)*time.Second),
		),
	}
}

// NewHTTPProviderWithReasoningEffortSupport creates an OpenAI-compatible provider
// that maps thinking_level to reasoning_effort for LiteLLM-compatible endpoints.
func NewHTTPProviderWithReasoningEffortSupport(
	apiKey, apiBase, proxy, maxTokensField string,
	requestTimeoutSeconds int,
) *HTTPProvider {
	return &HTTPProvider{
		delegate: openai_compat.NewProvider(
			apiKey,
			apiBase,
			proxy,
			openai_compat.WithMaxTokensField(maxTokensField),
			openai_compat.WithRequestTimeout(time.Duration(requestTimeoutSeconds)*time.Second),
			openai_compat.WithReasoningEffortFromThinking(true),
			openai_compat.WithAllowedOpenAIParamsForReasoningEffort(true),
		),
		supportsThinking: true,
	}
}

// NewHTTPProviderWithOpenAIReasoningEffortSupport creates an OpenAI-compatible
// provider that maps thinking_level to reasoning_effort for direct OpenAI
// chat-completions calls. It does not include LiteLLM-specific allowlist fields.
func NewHTTPProviderWithOpenAIReasoningEffortSupport(
	apiKey, apiBase, proxy, maxTokensField string,
	requestTimeoutSeconds int,
) *HTTPProvider {
	return &HTTPProvider{
		delegate: openai_compat.NewProvider(
			apiKey,
			apiBase,
			proxy,
			openai_compat.WithMaxTokensField(maxTokensField),
			openai_compat.WithRequestTimeout(time.Duration(requestTimeoutSeconds)*time.Second),
			openai_compat.WithReasoningEffortFromThinking(true),
		),
		supportsThinking: true,
	}
}

func (p *HTTPProvider) Chat(
	ctx context.Context,
	messages []Message,
	tools []ToolDefinition,
	model string,
	options map[string]any,
) (*LLMResponse, error) {
	resp, err := p.delegate.Chat(ctx, messages, tools, model, options)
	if err != nil {
		return nil, err
	}
	ensureUsageContract(resp, messages, tools, model, options)
	return resp, nil
}

func (p *HTTPProvider) CountTokens(
	ctx context.Context,
	messages []Message,
	tools []ToolDefinition,
	model string,
	options map[string]any,
) (int, error) {
	return p.delegate.CountTokens(ctx, messages, tools, model, options)
}

func (p *HTTPProvider) GetDefaultModel() string {
	return ""
}

// SupportsThinking implements providers.ThinkingCapable.
func (p *HTTPProvider) SupportsThinking() bool {
	return p.supportsThinking
}
