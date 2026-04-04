package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/itsivag/suprclaw/pkg/bus"
	"github.com/itsivag/suprclaw/pkg/config"
	"github.com/itsivag/suprclaw/pkg/providers"
)

type budgetRecordingProvider struct {
	failFirstContext bool
	calls            int
	tokenPerCall     []int
}

func (p *budgetRecordingProvider) Chat(
	ctx context.Context,
	messages []providers.Message,
	tools []providers.ToolDefinition,
	model string,
	opts map[string]any,
) (*providers.LLMResponse, error) {
	p.calls++
	p.tokenPerCall = append(p.tokenPerCall, estimateMessageTokens(messages))
	if p.failFirstContext && p.calls == 1 {
		return nil, fmt.Errorf("context_length_exceeded: mock")
	}
	return &providers.LLMResponse{
		Content:   "ok",
		ToolCalls: []providers.ToolCall{},
		Usage: &providers.UsageInfo{
			PromptTokens:     100,
			CompletionTokens: 20,
			TotalTokens:      120,
		},
	}, nil
}

func (p *budgetRecordingProvider) GetDefaultModel() string {
	return "mock-budget-model"
}

type requiresNonSystemProvider struct {
	calls int
}

type budgetCountAwareProvider struct {
	chatCalls           int32
	countCalls          int32
	timeoutOnFirstChat  bool
	contextOnFirstChat  bool
	providerTokenResult int
}

type toolThenFinalUsageProvider struct {
	calls int
}

func (p *requiresNonSystemProvider) Chat(
	ctx context.Context,
	messages []providers.Message,
	tools []providers.ToolDefinition,
	model string,
	opts map[string]any,
) (*providers.LLMResponse, error) {
	p.calls++
	for _, m := range messages {
		if m.Role != "system" {
			return &providers.LLMResponse{Content: "ok"}, nil
		}
	}
	return nil, fmt.Errorf("provider requires at least one non-system message")
}

func (p *requiresNonSystemProvider) GetDefaultModel() string {
	return "mock-nonsystem-model"
}

func (p *budgetCountAwareProvider) Chat(
	ctx context.Context,
	messages []providers.Message,
	tools []providers.ToolDefinition,
	model string,
	opts map[string]any,
) (*providers.LLMResponse, error) {
	call := atomic.AddInt32(&p.chatCalls, 1)
	if p.timeoutOnFirstChat && call == 1 {
		return nil, context.DeadlineExceeded
	}
	if p.contextOnFirstChat && call == 1 {
		return nil, fmt.Errorf("context_length_exceeded: mock")
	}
	return &providers.LLMResponse{
		Content: "ok",
		Usage: &providers.UsageInfo{
			PromptTokens:     90,
			CompletionTokens: 10,
			TotalTokens:      100,
		},
	}, nil
}

func (p *budgetCountAwareProvider) GetDefaultModel() string {
	return "mock-count-aware-model"
}

func (p *budgetCountAwareProvider) CountTokens(
	ctx context.Context,
	messages []providers.Message,
	tools []providers.ToolDefinition,
	model string,
	opts map[string]any,
) (int, error) {
	_ = ctx
	_ = messages
	_ = tools
	_ = model
	_ = opts
	atomic.AddInt32(&p.countCalls, 1)
	if p.providerTokenResult > 0 {
		return p.providerTokenResult, nil
	}
	return 1000, nil
}

func (p *toolThenFinalUsageProvider) Chat(
	ctx context.Context,
	messages []providers.Message,
	tools []providers.ToolDefinition,
	model string,
	opts map[string]any,
) (*providers.LLMResponse, error) {
	p.calls++
	if p.calls == 1 {
		return &providers.LLMResponse{
			Content: "calling tool",
			ToolCalls: []providers.ToolCall{
				{
					ID:   "tc-1",
					Name: "noop_tool",
					Arguments: map[string]any{
						"text": "hello",
					},
				},
			},
			Usage: &providers.UsageInfo{
				PromptTokens:     150,
				CompletionTokens: 25,
				TotalTokens:      175,
			},
		}, nil
	}
	return &providers.LLMResponse{
		Content: "done",
		Usage: &providers.UsageInfo{
			PromptTokens:     200,
			CompletionTokens: 30,
			TotalTokens:      230,
		},
	}, nil
}

func (p *toolThenFinalUsageProvider) GetDefaultModel() string {
	return "mock-tool-usage-model"
}

func estimateMessageTokens(messages []providers.Message) int {
	total := 0
	for _, m := range messages {
		total += len([]rune(m.Content))
	}
	return total * 2 / 5
}

func newBudgetTestConfig(workspace string) *config.Config {
	return &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         workspace,
				Model:             "test-model",
				MaxTokens:         512,
				MaxToolIterations: 5,
				ContextGuard: config.ContextGuardConfig{
					Enabled:                true,
					SafetyMarginTokens:     64,
					TargetInputRatio:       0.78,
					PrecheckTriggerRatio:   0.85,
					EmergencyInputRatio:    0.60,
					MaxCompactionPasses:    3,
					PreserveRecentMessages: 4,
				},
			},
		},
	}
}

func TestAgentLoop_PreDispatchCompactionBeforeProviderCall(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "agent-budget-*")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	provider := &budgetRecordingProvider{}
	al := NewAgentLoop(newBudgetTestConfig(tmpDir), bus.NewMessageBus(), provider)

	defaultAgent := al.registry.GetDefaultAgent()
	if defaultAgent == nil {
		t.Fatal("default agent missing")
	}
	defaultAgent.ContextWindow = 4096

	sessionKey := "budget-pre-dispatch"
	var history []providers.Message
	for i := 0; i < 12; i++ {
		history = append(history,
			providers.Message{Role: "user", Content: fmt.Sprintf("u%d", i)},
			providers.Message{Role: "assistant", Content: strings.Repeat("A", 3000)},
		)
	}
	defaultAgent.Sessions.SetHistory(sessionKey, history)

	_, err = al.ProcessDirectWithChannel(context.Background(), "latest user intent", sessionKey, "cli", "direct")
	if err != nil {
		t.Fatalf("ProcessDirectWithChannel() error: %v", err)
	}

	if provider.calls != 1 {
		t.Fatalf("provider calls = %d, want 1", provider.calls)
	}
	if len(provider.tokenPerCall) != 1 {
		t.Fatalf("tokenPerCall len = %d, want 1", len(provider.tokenPerCall))
	}

	// effective_input_limit = 4096 - 512 - 64 = 3520
	if provider.tokenPerCall[0] > 3520 {
		t.Fatalf("provider received oversized context: %d > 3520", provider.tokenPerCall[0])
	}
}

func TestAgentLoop_ContextBudgetUnfitFailsWithoutProviderCall(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "agent-budget-*")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	provider := &budgetRecordingProvider{}
	al := NewAgentLoop(newBudgetTestConfig(tmpDir), bus.NewMessageBus(), provider)
	defaultAgent := al.registry.GetDefaultAgent()
	if defaultAgent == nil {
		t.Fatal("default agent missing")
	}
	defaultAgent.ContextWindow = 1200

	_, err = al.ProcessDirectWithChannel(
		context.Background(),
		strings.Repeat("X", 12000),
		"budget-unfit",
		"cli",
		"direct",
	)
	if err == nil {
		t.Fatal("expected context budget failure")
	}

	var reqErr *RequestError
	if !errors.As(err, &reqErr) {
		t.Fatalf("error type = %T, want *RequestError", err)
	}
	if reqErr.Code != ErrCodeContextBudgetUnfit {
		t.Fatalf("request error code = %q, want %q", reqErr.Code, ErrCodeContextBudgetUnfit)
	}
	if provider.calls != 0 {
		t.Fatalf("provider calls = %d, want 0", provider.calls)
	}
}

func TestAgentLoop_ContextErrorRetryRecompacts(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "agent-budget-*")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	provider := &budgetRecordingProvider{failFirstContext: true}
	al := NewAgentLoop(newBudgetTestConfig(tmpDir), bus.NewMessageBus(), provider)
	defaultAgent := al.registry.GetDefaultAgent()
	if defaultAgent == nil {
		t.Fatal("default agent missing")
	}
	defaultAgent.ContextWindow = 4096

	sessionKey := "budget-retry"
	var history []providers.Message
	for i := 0; i < 8; i++ {
		history = append(history,
			providers.Message{Role: "user", Content: fmt.Sprintf("u%d", i)},
			providers.Message{Role: "assistant", Content: strings.Repeat("B", 2500)},
		)
	}
	defaultAgent.Sessions.SetHistory(sessionKey, history)

	_, err = al.ProcessDirectWithChannel(context.Background(), "retry intent", sessionKey, "cli", "direct")
	if err != nil {
		t.Fatalf("ProcessDirectWithChannel() error: %v", err)
	}

	if provider.calls != 2 {
		t.Fatalf("provider calls = %d, want 2", provider.calls)
	}
	if len(provider.tokenPerCall) != 2 {
		t.Fatalf("tokenPerCall len = %d, want 2", len(provider.tokenPerCall))
	}
	if provider.tokenPerCall[1] > provider.tokenPerCall[0] {
		t.Fatalf("retry payload should not grow after compaction: first=%d second=%d", provider.tokenPerCall[0], provider.tokenPerCall[1])
	}
}

func TestAgentLoop_InjectsNonSystemMessageWhenCompactionProducesSystemOnly(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "agent-budget-*")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	provider := &requiresNonSystemProvider{}
	al := NewAgentLoop(newBudgetTestConfig(tmpDir), bus.NewMessageBus(), provider)
	defaultAgent := al.registry.GetDefaultAgent()
	if defaultAgent == nil {
		t.Fatal("default agent missing")
	}

	sessionKey := "budget-system-only"
	defaultAgent.Sessions.SetSummary(sessionKey, "summary only context")
	defaultAgent.Sessions.SetHistory(sessionKey, nil)

	_, err = al.ProcessDirectWithChannel(context.Background(), "", sessionKey, "cli", "direct")
	if err != nil {
		t.Fatalf("ProcessDirectWithChannel() error: %v", err)
	}
	if provider.calls != 1 {
		t.Fatalf("provider calls = %d, want 1", provider.calls)
	}
}

func TestAgentLoop_BudgetCheckSkipsProviderCountInSafeZone(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "agent-budget-*")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	provider := &budgetCountAwareProvider{providerTokenResult: 100}
	al := NewAgentLoop(newBudgetTestConfig(tmpDir), bus.NewMessageBus(), provider)
	defaultAgent := al.registry.GetDefaultAgent()
	if defaultAgent == nil {
		t.Fatal("default agent missing")
	}
	defaultAgent.ContextWindow = 4096

	_, err = al.ProcessDirectWithChannel(context.Background(), "short message", "budget-safe-zone", "cli", "direct")
	if err != nil {
		t.Fatalf("ProcessDirectWithChannel() error: %v", err)
	}

	if got := atomic.LoadInt32(&provider.countCalls); got != 0 {
		t.Fatalf("provider count calls = %d, want 0 (safe-zone estimator path)", got)
	}
}

func TestAgentLoop_AnchoredBelowThresholdCompletesWithoutCompaction(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "agent-budget-*")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	provider := &budgetRecordingProvider{}
	al := NewAgentLoop(newBudgetTestConfig(tmpDir), bus.NewMessageBus(), provider)
	defaultAgent := al.registry.GetDefaultAgent()
	if defaultAgent == nil {
		t.Fatal("default agent missing")
	}
	defaultAgent.ContextWindow = 4096

	sessionKey := "budget-anchor-low"
	defaultAgent.Sessions.SetHistory(sessionKey, []providers.Message{
		{Role: "user", Content: "hello"},
		{
			Role:    "assistant",
			Content: "anchor",
			Usage: &providers.UsageInfo{
				PromptTokens:     350,
				CompletionTokens: 50,
				TotalTokens:      400,
			},
		},
		{Role: "user", Content: "small tail"},
	})

	_, err = al.ProcessDirectWithChannel(context.Background(), "short prompt", sessionKey, "cli", "direct")
	if err != nil {
		t.Fatalf("ProcessDirectWithChannel() error: %v", err)
	}
	if provider.calls != 1 {
		t.Fatalf("provider calls = %d, want 1", provider.calls)
	}
	if got := al.compactionTriggeredTotal.Load(); got != 0 {
		t.Fatalf("compactionTriggeredTotal = %d, want 0", got)
	}
}

func TestAgentLoop_BudgetCheckDedupeOnTimeoutRetry(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "agent-budget-*")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	provider := &budgetCountAwareProvider{
		timeoutOnFirstChat:  true,
		providerTokenResult: 1000,
	}
	al := NewAgentLoop(newBudgetTestConfig(tmpDir), bus.NewMessageBus(), provider)
	defaultAgent := al.registry.GetDefaultAgent()
	if defaultAgent == nil {
		t.Fatal("default agent missing")
	}
	defaultAgent.ContextWindow = 4096

	longInput := strings.Repeat("X", 5000)
	_, err = al.ProcessDirectWithChannel(context.Background(), longInput, "budget-timeout-dedupe", "cli", "direct")
	if err != nil {
		t.Fatalf("ProcessDirectWithChannel() error: %v", err)
	}

	if got := atomic.LoadInt32(&provider.countCalls); got != 0 {
		t.Fatalf("provider count calls = %d, want 0 (local-only estimate path)", got)
	}
}

func TestAgentLoop_BudgetRechecksAfterContextOverflow(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "agent-budget-*")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	provider := &budgetCountAwareProvider{
		contextOnFirstChat:  true,
		providerTokenResult: 1000,
	}
	al := NewAgentLoop(newBudgetTestConfig(tmpDir), bus.NewMessageBus(), provider)
	defaultAgent := al.registry.GetDefaultAgent()
	if defaultAgent == nil {
		t.Fatal("default agent missing")
	}
	defaultAgent.ContextWindow = 4096

	longInput := strings.Repeat("Y", 5000)
	_, err = al.ProcessDirectWithChannel(context.Background(), longInput, "budget-context-recheck", "cli", "direct")
	if err != nil {
		t.Fatalf("ProcessDirectWithChannel() error: %v", err)
	}

	if got := atomic.LoadInt32(&provider.countCalls); got != 0 {
		t.Fatalf("provider count calls = %d, want 0 after context overflow emergency recheck", got)
	}
}

func TestAgentLoop_AnchoredNearThresholdStillCompactsBeforeProviderCall(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "agent-budget-*")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	provider := &budgetRecordingProvider{}
	al := NewAgentLoop(newBudgetTestConfig(tmpDir), bus.NewMessageBus(), provider)
	defaultAgent := al.registry.GetDefaultAgent()
	if defaultAgent == nil {
		t.Fatal("default agent missing")
	}
	defaultAgent.ContextWindow = 4096

	sessionKey := "budget-anchor-near-threshold"
	history := []providers.Message{
		{Role: "user", Content: "u0"},
		{
			Role:    "assistant",
			Content: "anchor",
			Usage: &providers.UsageInfo{
				PromptTokens:     2000,
				CompletionTokens: 200,
				TotalTokens:      2200,
			},
		},
		{Role: "user", Content: "u1"},
		{Role: "assistant", Content: strings.Repeat("A", 9000)},
	}
	for i := 0; i < 5; i++ {
		history = append(history,
			providers.Message{Role: "user", Content: fmt.Sprintf("u%d", i+2)},
			providers.Message{Role: "assistant", Content: "ok"},
		)
	}
	defaultAgent.Sessions.SetHistory(sessionKey, history)

	_, err = al.ProcessDirectWithChannel(context.Background(), "retry intent", sessionKey, "cli", "direct")
	if err != nil {
		t.Fatalf("ProcessDirectWithChannel() error: %v", err)
	}

	if provider.calls != 1 {
		t.Fatalf("provider calls = %d, want 1", provider.calls)
	}
	if len(provider.tokenPerCall) != 1 {
		t.Fatalf("tokenPerCall len = %d, want 1", len(provider.tokenPerCall))
	}
	// effective_input_limit = 4096 - 512 - 64 = 3520
	if provider.tokenPerCall[0] > 3520 {
		t.Fatalf("provider received oversized context: %d > 3520", provider.tokenPerCall[0])
	}
}

func TestAgentLoop_OverflowAfterAnchoredSkipRecoversWithEmergencyCompaction(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "agent-budget-*")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	provider := &budgetCountAwareProvider{
		contextOnFirstChat:  true,
		providerTokenResult: 1000,
	}
	al := NewAgentLoop(newBudgetTestConfig(tmpDir), bus.NewMessageBus(), provider)
	defaultAgent := al.registry.GetDefaultAgent()
	if defaultAgent == nil {
		t.Fatal("default agent missing")
	}
	defaultAgent.ContextWindow = 4096
	sessionKey := "budget-overflow-after-skip"
	defaultAgent.Sessions.SetHistory(sessionKey, []providers.Message{
		{
			Role:    "assistant",
			Content: "anchor",
			Usage: &providers.UsageInfo{
				PromptTokens:     250,
				CompletionTokens: 50,
				TotalTokens:      300,
			},
		},
		{Role: "user", Content: "small tail"},
	})

	_, err = al.ProcessDirectWithChannel(context.Background(), "small prompt", sessionKey, "cli", "direct")
	if err != nil {
		t.Fatalf("ProcessDirectWithChannel() error: %v", err)
	}
	if got := atomic.LoadInt32(&provider.chatCalls); got != 2 {
		t.Fatalf("provider chat calls = %d, want 2 (overflow retry path)", got)
	}
	if got := atomic.LoadInt32(&provider.countCalls); got != 0 {
		t.Fatalf("provider count calls = %d, want 0", got)
	}
}

func TestAgentLoop_FinalAssistantUsagePersistsToSession(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "agent-budget-*")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	provider := &budgetRecordingProvider{}
	al := NewAgentLoop(newBudgetTestConfig(tmpDir), bus.NewMessageBus(), provider)
	defaultAgent := al.registry.GetDefaultAgent()
	if defaultAgent == nil {
		t.Fatal("default agent missing")
	}

	_, err = al.ProcessDirectWithChannel(context.Background(), "hello", "budget-final-usage", "cli", "direct")
	if err != nil {
		t.Fatalf("ProcessDirectWithChannel() error: %v", err)
	}

	history := defaultAgent.Sessions.GetHistory("agent:main:main")
	if len(history) < 2 {
		t.Fatalf("history len = %d, want at least 2", len(history))
	}
	last := history[len(history)-1]
	if last.Role != "assistant" {
		t.Fatalf("last role = %q, want assistant", last.Role)
	}
	if last.Usage == nil {
		t.Fatal("expected final assistant usage to be persisted")
	}
	if last.Usage.TotalTokens != 120 {
		t.Fatalf("final usage total = %d, want 120", last.Usage.TotalTokens)
	}
}

func TestAgentLoop_ToolCallAssistantUsagePersistsToSession(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "agent-budget-*")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	provider := &toolThenFinalUsageProvider{}
	al := NewAgentLoop(newBudgetTestConfig(tmpDir), bus.NewMessageBus(), provider)
	defaultAgent := al.registry.GetDefaultAgent()
	if defaultAgent == nil {
		t.Fatal("default agent missing")
	}
	defaultAgent.Tools.Register(&noopTool{})

	_, err = al.ProcessDirectWithChannel(context.Background(), "run tool", "budget-tool-usage", "cli", "direct")
	if err != nil {
		t.Fatalf("ProcessDirectWithChannel() error: %v", err)
	}

	history := defaultAgent.Sessions.GetHistory("agent:main:main")
	if len(history) < 4 {
		t.Fatalf("history len = %d, want at least 4", len(history))
	}
	toolAssistant := history[1]
	if toolAssistant.Role != "assistant" || len(toolAssistant.ToolCalls) != 1 {
		t.Fatalf("expected persisted tool-call assistant, got %+v", toolAssistant)
	}
	if toolAssistant.Usage == nil || toolAssistant.Usage.TotalTokens != 175 {
		t.Fatalf("tool-call usage = %+v, want total_tokens=175", toolAssistant.Usage)
	}
}
