package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
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
	}, nil
}

func (p *budgetRecordingProvider) GetDefaultModel() string {
	return "mock-budget-model"
}

type requiresNonSystemProvider struct {
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
