package agent

import (
	"context"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/itsivag/suprclaw/pkg/bus"
	"github.com/itsivag/suprclaw/pkg/config"
	"github.com/itsivag/suprclaw/pkg/providers"
	providerscommon "github.com/itsivag/suprclaw/pkg/providers/common"
	"github.com/itsivag/suprclaw/pkg/routing"
	"github.com/itsivag/suprclaw/pkg/tools"
)

func testUsageResponse(
	resp *providers.LLMResponse,
	messages []providers.Message,
	tools []providers.ToolDefinition,
	model string,
	options map[string]any,
) *providers.LLMResponse {
	if resp.Usage != nil {
		return resp
	}
	promptTokens := providerscommon.EstimateTokenCount(messages, tools, model, options)
	if promptTokens <= 0 {
		promptTokens = 1
	}
	completionTokens := 0
	if strings.TrimSpace(resp.Content) != "" {
		completionTokens = providerscommon.EstimateTokenCount(
			[]providers.Message{{Role: "assistant", Content: resp.Content}},
			nil,
			model,
			nil,
		)
	}
	resp.Usage = &providers.UsageInfo{
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		TotalTokens:      promptTokens + completionTokens,
	}
	return resp
}

type deferredHiddenTool struct {
	name  string
	calls atomic.Int32
}

func (t *deferredHiddenTool) Name() string        { return t.name }
func (t *deferredHiddenTool) Description() string { return "deferred hidden test tool" }
func (t *deferredHiddenTool) UsageContract() tools.ToolUsageContract {
	return tools.ToolUsageContract{
		UseWhen:      "test hidden deferred execution",
		DoNotUseWhen: "test hidden deferred non-usage",
		HardRequirements: []string{
			"test requirement",
		},
	}
}
func (t *deferredHiddenTool) Parameters() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}
}
func (t *deferredHiddenTool) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	t.calls.Add(1)
	return tools.SilentResult("hidden tool executed")
}

type captureToolsProvider struct {
	mu        sync.Mutex
	snapshots [][]string
}

func (p *captureToolsProvider) Chat(
	ctx context.Context,
	messages []providers.Message,
	toolDefs []providers.ToolDefinition,
	model string,
	opts map[string]any,
) (*providers.LLMResponse, error) {
	p.mu.Lock()
	p.snapshots = append(p.snapshots, toolDefNames(toolDefs))
	p.mu.Unlock()
	return testUsageResponse(&providers.LLMResponse{
		Content: "done",
	}, messages, toolDefs, model, opts), nil
}

func (p *captureToolsProvider) GetDefaultModel() string { return "capture-tools-provider" }
func (p *captureToolsProvider) CountTokens(
	ctx context.Context,
	messages []providers.Message,
	tools []providers.ToolDefinition,
	model string,
	options map[string]any,
) (int, error) {
	_ = ctx
	return providerscommon.EstimateTokenCount(messages, tools, model, options), nil
}

type discoveryThenHiddenProvider struct {
	mu        sync.Mutex
	callCount int
	snapshots [][]string
}

func (p *discoveryThenHiddenProvider) Chat(
	ctx context.Context,
	messages []providers.Message,
	toolDefs []providers.ToolDefinition,
	model string,
	opts map[string]any,
) (*providers.LLMResponse, error) {
	p.mu.Lock()
	p.callCount++
	call := p.callCount
	p.snapshots = append(p.snapshots, toolDefNames(toolDefs))
	p.mu.Unlock()

	switch call {
	case 1:
		return testUsageResponse(&providers.LLMResponse{
			ToolCalls: []providers.ToolCall{
				{
					ID:   "tc-discovery",
					Name: "tool_search_tool_bm25",
					Arguments: map[string]any{
						"query": "deferred hidden test tool",
					},
				},
			},
		}, messages, toolDefs, model, opts), nil
	case 2:
		return testUsageResponse(&providers.LLMResponse{
			ToolCalls: []providers.ToolCall{
				{
					ID:   "tc-hidden",
					Name: "hidden_test_tool",
					Arguments: map[string]any{
						"ok": true,
					},
				},
			},
		}, messages, toolDefs, model, opts), nil
	default:
		return testUsageResponse(&providers.LLMResponse{Content: "done"}, messages, toolDefs, model, opts), nil
	}
}

func (p *discoveryThenHiddenProvider) GetDefaultModel() string {
	return "discovery-then-hidden-provider"
}
func (p *discoveryThenHiddenProvider) CountTokens(
	ctx context.Context,
	messages []providers.Message,
	tools []providers.ToolDefinition,
	model string,
	options map[string]any,
) (int, error) {
	_ = ctx
	return providerscommon.EstimateTokenCount(messages, tools, model, options), nil
}

type hiddenWithoutExposureProvider struct {
	mu            sync.Mutex
	callCount     int
	sawGateResult bool
}

func (p *hiddenWithoutExposureProvider) Chat(
	ctx context.Context,
	messages []providers.Message,
	toolDefs []providers.ToolDefinition,
	model string,
	opts map[string]any,
) (*providers.LLMResponse, error) {
	p.mu.Lock()
	p.callCount++
	call := p.callCount
	p.mu.Unlock()

	if call == 1 {
		return testUsageResponse(&providers.LLMResponse{
			ToolCalls: []providers.ToolCall{
				{
					ID:        "tc-hidden-direct",
					Name:      "hidden_test_tool",
					Arguments: map[string]any{},
				},
			},
		}, messages, toolDefs, model, opts), nil
	}

	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if msg.Role == "tool" && msg.Content != "" {
			if containsAll(msg.Content, "not exposed in this request", "tool_search_tool_bm25") {
				p.mu.Lock()
				p.sawGateResult = true
				p.mu.Unlock()
				break
			}
		}
	}
	return testUsageResponse(&providers.LLMResponse{Content: "done"}, messages, toolDefs, model, opts), nil
}

func (p *hiddenWithoutExposureProvider) GetDefaultModel() string {
	return "hidden-without-exposure-provider"
}
func (p *hiddenWithoutExposureProvider) CountTokens(
	ctx context.Context,
	messages []providers.Message,
	tools []providers.ToolDefinition,
	model string,
	options map[string]any,
) (int, error) {
	_ = ctx
	return providerscommon.EstimateTokenCount(messages, tools, model, options), nil
}

func containsAll(haystack string, needles ...string) bool {
	for _, needle := range needles {
		if !strings.Contains(haystack, needle) {
			return false
		}
	}
	return true
}

func newDeferredTestConfig(t *testing.T) *config.Config {
	t.Helper()
	return &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         t.TempDir(),
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
				ContextGuard: config.ContextGuardConfig{
					Enabled: false,
				},
			},
		},
	}
}

func toolDefNames(defs []providers.ToolDefinition) []string {
	names := make([]string, 0, len(defs))
	for _, def := range defs {
		if def.Function.Name != "" {
			names = append(names, def.Function.Name)
		}
	}
	return names
}

func TestDeferredTools_HiddenAbsentByDefault(t *testing.T) {
	provider := &captureToolsProvider{}
	al := NewAgentLoop(newDeferredTestConfig(t), bus.NewMessageBus(), provider)
	defer al.Close()

	defaultAgent := al.GetRegistry().GetDefaultAgent()
	if defaultAgent == nil {
		t.Fatal("expected default agent")
	}
	defaultAgent.Tools.RegisterHidden(&deferredHiddenTool{name: "hidden_test_tool"})

	if _, err := al.ProcessDirectWithChannel(
		context.Background(),
		"hello",
		"deferred-session-1",
		"cli",
		"direct",
	); err != nil {
		t.Fatalf("ProcessDirectWithChannel failed: %v", err)
	}

	if len(provider.snapshots) == 0 {
		t.Fatal("expected provider to receive at least one tool snapshot")
	}
	if slices.Contains(provider.snapshots[0], "hidden_test_tool") {
		t.Fatalf("hidden tool should not be exposed in first request payload, got %v", provider.snapshots[0])
	}
}

func TestDeferredTools_DiscoveryPersistsAndEnablesNextIteration(t *testing.T) {
	provider := &discoveryThenHiddenProvider{}
	al := NewAgentLoop(newDeferredTestConfig(t), bus.NewMessageBus(), provider)
	defer al.Close()

	defaultAgent := al.GetRegistry().GetDefaultAgent()
	if defaultAgent == nil {
		t.Fatal("expected default agent")
	}
	hidden := &deferredHiddenTool{name: "hidden_test_tool"}
	defaultAgent.Tools.RegisterHidden(hidden)
	defaultAgent.Tools.Register(tools.NewBM25SearchTool(defaultAgent.Tools, 5))

	sessionKey := routing.BuildAgentMainSessionKey(defaultAgent.ID)
	if _, err := al.ProcessDirectWithChannel(
		context.Background(),
		"use deferred tool",
		sessionKey,
		"cli",
		"direct",
	); err != nil {
		t.Fatalf("ProcessDirectWithChannel failed: %v", err)
	}

	if len(provider.snapshots) < 2 {
		t.Fatalf("expected at least two provider calls, got %d", len(provider.snapshots))
	}
	if slices.Contains(provider.snapshots[0], "hidden_test_tool") {
		t.Fatalf("hidden tool must be absent before discovery, got %v", provider.snapshots[0])
	}
	if !slices.Contains(provider.snapshots[1], "hidden_test_tool") {
		t.Fatalf("hidden tool should be exposed on next iteration after discovery, got %v", provider.snapshots[1])
	}
	if hidden.calls.Load() != 1 {
		t.Fatalf("expected hidden tool to execute once, got %d", hidden.calls.Load())
	}

	discovered := defaultAgent.Sessions.GetDiscoveredTools(sessionKey)
	if !slices.Contains(discovered, "hidden_test_tool") {
		t.Fatalf("expected discovered tools to persist hidden_test_tool, got %v", discovered)
	}
}

func TestDeferredTools_NonExposedCallIsRejected(t *testing.T) {
	provider := &hiddenWithoutExposureProvider{}
	al := NewAgentLoop(newDeferredTestConfig(t), bus.NewMessageBus(), provider)
	defer al.Close()

	defaultAgent := al.GetRegistry().GetDefaultAgent()
	if defaultAgent == nil {
		t.Fatal("expected default agent")
	}
	hidden := &deferredHiddenTool{name: "hidden_test_tool"}
	defaultAgent.Tools.RegisterHidden(hidden)

	if _, err := al.ProcessDirectWithChannel(
		context.Background(),
		"call hidden directly",
		routing.BuildAgentMainSessionKey(defaultAgent.ID),
		"cli",
		"direct",
	); err != nil {
		t.Fatalf("ProcessDirectWithChannel failed: %v", err)
	}

	if hidden.calls.Load() != 0 {
		t.Fatalf("hidden tool should not execute when not exposed, got %d calls", hidden.calls.Load())
	}
	if !provider.sawGateResult {
		t.Fatal("expected tool gate error message in follow-up model input")
	}
}

func TestDeferredTools_ResetSessionClearsDiscoveredTools(t *testing.T) {
	provider := &captureToolsProvider{}
	al := NewAgentLoop(newDeferredTestConfig(t), bus.NewMessageBus(), provider)
	defer al.Close()

	defaultAgent := al.GetRegistry().GetDefaultAgent()
	if defaultAgent == nil {
		t.Fatal("expected default agent")
	}
	defaultAgent.Tools.RegisterHidden(&deferredHiddenTool{name: "hidden_test_tool"})

	sessionKey := routing.BuildAgentMainSessionKey(defaultAgent.ID)
	defaultAgent.Sessions.SetDiscoveredTools(sessionKey, []string{"hidden_test_tool"})
	if err := defaultAgent.Sessions.Save(sessionKey); err != nil {
		t.Fatalf("failed to save discovered tools: %v", err)
	}

	if _, err := al.ProcessDirectWithChannel(
		context.Background(),
		"before reset",
		sessionKey,
		"cli",
		"direct",
	); err != nil {
		t.Fatalf("ProcessDirectWithChannel before reset failed: %v", err)
	}

	if !slices.Contains(provider.snapshots[0], "hidden_test_tool") {
		t.Fatalf("hidden tool should be exposed when discovered is set, got %v", provider.snapshots[0])
	}

	if err := al.ResetSession(defaultAgent.ID, sessionKey); err != nil {
		t.Fatalf("ResetSession failed: %v", err)
	}
	if got := defaultAgent.Sessions.GetDiscoveredTools(sessionKey); len(got) != 0 {
		t.Fatalf("expected discovered tools to be cleared after reset, got %v", got)
	}

	if _, err := al.ProcessDirectWithChannel(
		context.Background(),
		"after reset",
		sessionKey,
		"cli",
		"direct",
	); err != nil {
		t.Fatalf("ProcessDirectWithChannel after reset failed: %v", err)
	}

	if len(provider.snapshots) < 2 {
		t.Fatalf("expected two snapshots, got %d", len(provider.snapshots))
	}
	if slices.Contains(provider.snapshots[1], "hidden_test_tool") {
		t.Fatalf("hidden tool should not be exposed after reset, got %v", provider.snapshots[1])
	}
}
