package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/itsivag/suprclaw/pkg/bus"
	"github.com/itsivag/suprclaw/pkg/channels"
	"github.com/itsivag/suprclaw/pkg/config"
	mcppkg "github.com/itsivag/suprclaw/pkg/mcp"
	"github.com/itsivag/suprclaw/pkg/media"
	"github.com/itsivag/suprclaw/pkg/providers"
	"github.com/itsivag/suprclaw/pkg/routing"
	"github.com/itsivag/suprclaw/pkg/tools"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

type fakeChannel struct{ id string }

func (f *fakeChannel) Name() string                                            { return "fake" }
func (f *fakeChannel) Start(ctx context.Context) error                         { return nil }
func (f *fakeChannel) Stop(ctx context.Context) error                          { return nil }
func (f *fakeChannel) Send(ctx context.Context, msg bus.OutboundMessage) error { return nil }
func (f *fakeChannel) IsRunning() bool                                         { return true }
func (f *fakeChannel) IsAllowed(string) bool                                   { return true }
func (f *fakeChannel) IsAllowedSender(sender bus.SenderInfo) bool              { return true }
func (f *fakeChannel) ReasoningChannelID() string                              { return f.id }

type fakeLoopMCPManager struct {
	loadErr    error
	loadCalls  int
	closeCalls int
	servers    map[string]*mcppkg.ServerConnection
}

func (m *fakeLoopMCPManager) LoadFromMCPConfig(
	ctx context.Context,
	mcpCfg config.MCPConfig,
	workspacePath string,
) error {
	m.loadCalls++
	return m.loadErr
}

func (m *fakeLoopMCPManager) GetServers() map[string]*mcppkg.ServerConnection {
	if m.servers == nil {
		return map[string]*mcppkg.ServerConnection{}
	}
	return m.servers
}

func (m *fakeLoopMCPManager) CallTool(
	ctx context.Context,
	serverName, toolName string,
	arguments map[string]any,
) (*sdkmcp.CallToolResult, error) {
	return &sdkmcp.CallToolResult{}, nil
}

func (m *fakeLoopMCPManager) Close() error {
	m.closeCalls++
	return nil
}

func newFakeLoopMCPManagerWithSupabaseTool(toolName string) *fakeLoopMCPManager {
	return &fakeLoopMCPManager{
		servers: map[string]*mcppkg.ServerConnection{
			"supabase": {
				Name: "supabase",
				Tools: []*sdkmcp.Tool{
					{
						Name:        toolName,
						Description: "fake supabase tool",
						InputSchema: map[string]any{
							"type":       "object",
							"properties": map[string]any{},
						},
					},
				},
			},
		},
	}
}

func newTestConfigWithEnabledMCP(workspace string) *config.Config {
	return &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         workspace,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
		Tools: config.ToolsConfig{
			MCP: config.MCPConfig{
				ToolConfig: config.ToolConfig{
					Enabled: true,
				},
				Servers: map[string]config.MCPServerConfig{
					"supabase": {
						Enabled: true,
						Command: "fake-mcp-command",
					},
				},
			},
		},
	}
}

func newTestReloadConfigWithWriter(workspace string) *config.Config {
	cfg := newTestConfigWithEnabledMCP(workspace)
	cfg.Agents.List = []config.AgentConfig{
		{
			ID:        "main",
			Default:   true,
			Workspace: workspace,
			Model: &config.AgentModelConfig{
				Primary: "test-model",
			},
		},
		{
			ID:        "writer",
			Workspace: filepath.Join(workspace, "writer"),
			Model: &config.AgentModelConfig{
				Primary: "test-model",
			},
		},
	}
	return cfg
}

type recordingProvider struct {
	lastMessages []providers.Message
}

func (r *recordingProvider) Chat(
	ctx context.Context,
	messages []providers.Message,
	tools []providers.ToolDefinition,
	model string,
	opts map[string]any,
) (*providers.LLMResponse, error) {
	r.lastMessages = append([]providers.Message(nil), messages...)
	return &providers.LLMResponse{
		Content:   "Mock response",
		ToolCalls: []providers.ToolCall{},
	}, nil
}

func (r *recordingProvider) GetDefaultModel() string {
	return "mock-model"
}

type toolCallThenFinalProvider struct {
	callCount int
}

func (p *toolCallThenFinalProvider) Chat(
	ctx context.Context,
	messages []providers.Message,
	tools []providers.ToolDefinition,
	model string,
	opts map[string]any,
) (*providers.LLMResponse, error) {
	p.callCount++
	if p.callCount == 1 {
		return &providers.LLMResponse{
			ToolCalls: []providers.ToolCall{
				{
					ID:   "tc-1",
					Name: "noop_tool",
					Arguments: map[string]any{
						"value": "x",
					},
				},
			},
		}, nil
	}
	return &providers.LLMResponse{
		Content:   "done",
		ToolCalls: []providers.ToolCall{},
	}, nil
}

func (p *toolCallThenFinalProvider) GetDefaultModel() string { return "mock-model" }

type messageToolThenDoneProvider struct {
	callCount int
}

func (p *messageToolThenDoneProvider) Chat(
	ctx context.Context,
	messages []providers.Message,
	tools []providers.ToolDefinition,
	model string,
	opts map[string]any,
) (*providers.LLMResponse, error) {
	p.callCount++
	if p.callCount == 1 {
		return &providers.LLMResponse{
			ToolCalls: []providers.ToolCall{
				{
					ID:   "tc-message-1",
					Name: "message",
					Arguments: map[string]any{
						"content": "sent via message tool",
					},
				},
			},
		}, nil
	}
	return &providers.LLMResponse{
		Content:   "",
		ToolCalls: []providers.ToolCall{},
	}, nil
}

func (p *messageToolThenDoneProvider) GetDefaultModel() string { return "mock-model" }

type noopTool struct{}

func (t *noopTool) Name() string        { return "noop_tool" }
func (t *noopTool) Description() string { return "no-op test tool" }
func (t *noopTool) Parameters() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}
}
func (t *noopTool) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	return tools.NewToolResult("ok")
}

type failingTool struct{}

func (t *failingTool) Name() string        { return "noop_tool" }
func (t *failingTool) Description() string { return "failing test tool" }
func (t *failingTool) Parameters() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}
}
func (t *failingTool) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	return tools.ErrorResult("tool exploded")
}

type alwaysFailProvider struct {
	err error
}

func (p *alwaysFailProvider) Chat(
	ctx context.Context,
	messages []providers.Message,
	tools []providers.ToolDefinition,
	model string,
	opts map[string]any,
) (*providers.LLMResponse, error) {
	return nil, p.err
}

func (p *alwaysFailProvider) GetDefaultModel() string { return "mock-model" }

func collectActivityEventsUntilTerminal(t *testing.T, msgBus *bus.MessageBus, timeout time.Duration) []bus.OutboundActivityEvent {
	t.Helper()

	deadline := time.After(timeout)
	events := make([]bus.OutboundActivityEvent, 0, 16)
	for {
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for terminal activity event, collected=%d", len(events))
		case evt, ok := <-msgBus.OutboundActivityChan():
			if !ok {
				t.Fatalf("outbound activity channel closed unexpectedly")
			}
			events = append(events, evt)
			switch evt.Event.EventType {
			case "run.completed", "run.failed":
				return events
			}
		}
	}
}

func newTestAgentLoop(
	t *testing.T,
) (al *AgentLoop, cfg *config.Config, msgBus *bus.MessageBus, provider *mockProvider, cleanup func()) {
	t.Helper()
	tmpDir, err := os.MkdirTemp("", "agent-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	cfg = &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
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
	msgBus = bus.NewMessageBus()
	provider = &mockProvider{}
	al = NewAgentLoop(cfg, msgBus, provider)
	return al, cfg, msgBus, provider, func() { os.RemoveAll(tmpDir) }
}

func TestProcessMessage_IncludesCurrentSenderInDynamicContext(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "agent-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
	}

	msgBus := bus.NewMessageBus()
	provider := &recordingProvider{}
	al := NewAgentLoop(cfg, msgBus, provider)

	response, _, err := al.processMessage(context.Background(), bus.InboundMessage{
		Channel:  "discord",
		SenderID: "discord:123",
		Sender: bus.SenderInfo{
			DisplayName: "Alice",
		},
		ChatID:  "group-1",
		Content: "hello",
	})
	if err != nil {
		t.Fatalf("processMessage() error = %v", err)
	}
	if response != "Mock response" {
		t.Fatalf("processMessage() response = %q, want %q", response, "Mock response")
	}
	if len(provider.lastMessages) == 0 {
		t.Fatal("provider did not receive any messages")
	}

	systemPrompt := provider.lastMessages[0].Content
	wantSender := "## Current Sender\nCurrent sender: Alice (ID: discord:123)"
	if !strings.Contains(systemPrompt, wantSender) {
		t.Fatalf("system prompt missing sender context %q:\n%s", wantSender, systemPrompt)
	}

	lastMessage := provider.lastMessages[len(provider.lastMessages)-1]
	if lastMessage.Role != "user" || lastMessage.Content != "hello" {
		t.Fatalf("last provider message = %+v, want unchanged user message", lastMessage)
	}
}

func TestProcessMessage_ExplicitUnknownAgentReturnsTypedError(t *testing.T) {
	al, _, _, _, cleanup := newTestAgentLoop(t)
	defer cleanup()

	_, _, err := al.processMessage(context.Background(), bus.InboundMessage{
		Channel:  "supr",
		SenderID: "supr-user",
		ChatID:   "supr:test",
		Content:  "hello",
		Metadata: map[string]string{
			metadataKeyRequestedAgentID: "missing-agent",
		},
	})

	var reqErr *RequestError
	if !errors.As(err, &reqErr) {
		t.Fatalf("error type = %T, want *RequestError", err)
	}
	if reqErr.Code != ErrCodeAgentNotFound {
		t.Fatalf("request error code = %q, want %q", reqErr.Code, ErrCodeAgentNotFound)
	}
}

func TestProcessMessage_ExplicitAgentRouteIsPreserved(t *testing.T) {
	al, _, _, _, cleanup := newTestAgentLoop(t)
	defer cleanup()

	_, _, routeMeta, err := al.processMessageDetailed(context.Background(), bus.InboundMessage{
		Channel:  "supr",
		SenderID: "supr-user",
		ChatID:   "supr:test",
		Content:  "hello",
		Metadata: map[string]string{
			metadataKeyRequestedAgentID: "main",
		},
	})
	if err != nil {
		t.Fatalf("processMessageDetailed() error = %v", err)
	}
	if routeMeta == nil {
		t.Fatal("route metadata is nil")
	}
	if routeMeta.ResolvedAgentID != "main" {
		t.Fatalf("resolved agent = %q, want %q", routeMeta.ResolvedAgentID, "main")
	}
	if routeMeta.RouteMatchedBy != "explicit" {
		t.Fatalf("matched by = %q, want %q", routeMeta.RouteMatchedBy, "explicit")
	}
}

func TestProcessMessage_ExplicitAgentRouteEmitsCanonicalAgentID(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Workspace = t.TempDir()
	cfg.Agents.Defaults.Model = "test-model"
	cfg.Agents.Defaults.MaxToolIterations = 4
	cfg.Agents.List = []config.AgentConfig{
		{ID: "main", Default: true},
		{ID: "writer"},
	}

	msgBus := bus.NewMessageBus()
	al := NewAgentLoop(cfg, msgBus, &mockProvider{})

	response, _, err := al.processMessage(context.Background(), bus.InboundMessage{
		Channel:  "supr",
		SenderID: "supr-user",
		ChatID:   "supr:test",
		Content:  "hello",
		Metadata: map[string]string{
			metadataKeyRequestedAgentID: "writer",
		},
	})
	if err != nil {
		t.Fatalf("processMessage() error = %v", err)
	}
	if response != "Mock response" {
		t.Fatalf("response = %q, want %q", response, "Mock response")
	}

	events := collectActivityEventsUntilTerminal(t, msgBus, 2*time.Second)
	if len(events) == 0 {
		t.Fatal("expected activity events")
	}

	requiredEvents := map[string]bool{
		"run.started":       false,
		"message.completed": false,
		"run.completed":     false,
	}
	for _, evt := range events {
		if evt.Event.AgentID != "writer" {
			t.Fatalf("event %q agent_id = %q, want %q", evt.Event.EventType, evt.Event.AgentID, "writer")
		}
		if got, _ := evt.Event.Data["agent_id"].(string); got != "writer" {
			t.Fatalf("event %q data.agent_id = %q, want %q", evt.Event.EventType, got, "writer")
		}
		if _, ok := requiredEvents[evt.Event.EventType]; ok {
			requiredEvents[evt.Event.EventType] = true
		}
	}
	for eventType, seen := range requiredEvents {
		if !seen {
			t.Fatalf("missing %s event", eventType)
		}
	}
}

func TestProcessMessage_MessageToolPublishesResolvedAgentID(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Workspace = t.TempDir()
	cfg.Agents.Defaults.Model = "test-model"
	cfg.Agents.Defaults.MaxToolIterations = 4
	cfg.Tools.Message.Enabled = true
	cfg.Agents.List = []config.AgentConfig{
		{ID: "main", Default: true},
		{ID: "writer"},
	}

	msgBus := bus.NewMessageBus()
	al := NewAgentLoop(cfg, msgBus, &messageToolThenDoneProvider{})

	if _, _, err := al.processMessage(context.Background(), bus.InboundMessage{
		Channel:  "supr",
		SenderID: "supr-user",
		ChatID:   "supr:test",
		Content:  "send via tool",
		Metadata: map[string]string{
			metadataKeyRequestedAgentID: "writer",
		},
	}); err != nil {
		t.Fatalf("processMessage() error = %v", err)
	}

	select {
	case out := <-msgBus.OutboundChan():
		if out.Content != "sent via message tool" {
			t.Fatalf("outbound content = %q, want %q", out.Content, "sent via message tool")
		}
		if out.ResolvedAgentID != "writer" {
			t.Fatalf("resolved_agent_id = %q, want %q", out.ResolvedAgentID, "writer")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected outbound message from message tool")
	}
}

func TestProcessMessage_NoExplicitAgentUsesDefaultRoute(t *testing.T) {
	al, _, _, _, cleanup := newTestAgentLoop(t)
	defer cleanup()

	_, _, routeMeta, err := al.processMessageDetailed(context.Background(), bus.InboundMessage{
		Channel:  "supr",
		SenderID: "supr-user",
		ChatID:   "supr:test",
		Content:  "hello",
	})
	if err != nil {
		t.Fatalf("processMessageDetailed() error = %v", err)
	}
	if routeMeta == nil {
		t.Fatal("route metadata is nil")
	}
	if routeMeta.ResolvedAgentID != "main" {
		t.Fatalf("resolved agent = %q, want %q", routeMeta.ResolvedAgentID, "main")
	}
	if routeMeta.RouteMatchedBy == "explicit" {
		t.Fatalf("matched by = %q, expected non-explicit route", routeMeta.RouteMatchedBy)
	}
}

func TestProcessMessage_ToolStatusEmitsEvenWhenStatusUpdatesDisabled(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 4,
				StatusUpdates:     false,
			},
		},
	}
	msgBus := bus.NewMessageBus()
	provider := &toolCallThenFinalProvider{}
	al := NewAgentLoop(cfg, msgBus, provider)
	defaultAgent := al.GetRegistry().GetDefaultAgent()
	if defaultAgent == nil {
		t.Fatal("default agent missing")
	}
	defaultAgent.StatusUpdates = false
	defaultAgent.Tools.Register(&noopTool{})

	response, _, err := al.processMessage(context.Background(), bus.InboundMessage{
		Channel:  "supr",
		SenderID: "supr-user",
		ChatID:   "supr:test",
		Content:  "hello",
	})
	if err != nil {
		t.Fatalf("processMessage() error = %v", err)
	}
	if response != "done" {
		t.Fatalf("response = %q, want %q", response, "done")
	}

	select {
	case status := <-msgBus.OutboundStatusChan():
		if status.Kind != bus.StatusKindToolStart {
			t.Fatalf("status kind = %q, want %q", status.Kind, bus.StatusKindToolStart)
		}
		if len(status.ToolNames) == 0 || status.ToolNames[0] != "noop_tool" {
			t.Fatalf("tool names = %v, expected noop_tool", status.ToolNames)
		}
	default:
		t.Fatal("expected tool status update to be published")
	}
}

func TestProcessMessage_SuprPublishesStructuredActivityEvents(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 4,
			},
		},
	}
	msgBus := bus.NewMessageBus()
	provider := &toolCallThenFinalProvider{}
	al := NewAgentLoop(cfg, msgBus, provider)

	defaultAgent := al.GetRegistry().GetDefaultAgent()
	if defaultAgent == nil {
		t.Fatal("default agent missing")
	}
	expectedAgentID := defaultAgent.ID
	defaultAgent.Tools.Register(&noopTool{})

	response, _, err := al.processMessage(context.Background(), bus.InboundMessage{
		Channel:  "supr",
		SenderID: "supr-user",
		ChatID:   "supr:test",
		Content:  "hello",
	})
	if err != nil {
		t.Fatalf("processMessage() error = %v", err)
	}
	if response != "done" {
		t.Fatalf("response = %q, want %q", response, "done")
	}

	events := collectActivityEventsUntilTerminal(t, msgBus, 2*time.Second)
	if len(events) == 0 {
		t.Fatal("expected activity events")
	}

	runID := events[0].Event.RunID
	if runID == "" {
		t.Fatal("run_id should not be empty")
	}

	lastSeq := 0
	seen := map[string]bool{}
	for _, evt := range events {
		e := evt.Event
		if e.V != "1.0" {
			t.Fatalf("schema version = %q, want 1.0", e.V)
		}
		if e.EventID == "" {
			t.Fatalf("event_id is empty for event %q", e.EventType)
		}
		if e.EventType == "" {
			t.Fatal("event_type should not be empty")
		}
		if e.Timestamp == "" {
			t.Fatalf("timestamp is empty for event %q", e.EventType)
		}
		if e.Sequence <= lastSeq {
			t.Fatalf("sequence not strictly increasing: prev=%d current=%d", lastSeq, e.Sequence)
		}
		lastSeq = e.Sequence
		if e.SessionID != "test" {
			t.Fatalf("session_id = %q, want %q", e.SessionID, "test")
		}
		if e.RunID != runID {
			t.Fatalf("run_id mismatch: got=%q want=%q", e.RunID, runID)
		}
		if e.AgentID != expectedAgentID {
			t.Fatalf("event %q agent_id = %q, want %q", e.EventType, e.AgentID, expectedAgentID)
		}
		if got, _ := e.Data["agent_id"].(string); got != expectedAgentID {
			t.Fatalf("event %q data.agent_id = %q, want %q", e.EventType, got, expectedAgentID)
		}
		if e.IdempotencyKey == "" {
			t.Fatalf("idempotency_key is empty for event %q", e.EventType)
		}
		seen[e.EventType] = true
	}

	for _, name := range []string{
		"run.started",
		"step.started",
		"step.updated",
		"reasoning.summary",
		"tool.called",
		"tool.progress",
		"tool.completed",
		"message.started",
		"message.completed",
		"run.completed",
	} {
		if !seen[name] {
			t.Fatalf("expected event_type %q to be emitted", name)
		}
	}

	var foundToolCalled bool
	var foundMessageCompleted bool
	for _, evt := range events {
		switch evt.Event.EventType {
		case "reasoning.summary":
			if stepID, _ := evt.Event.Data["step_id"].(string); stepID == "" {
				t.Fatalf("reasoning.summary missing step_id")
			}
			if text, _ := evt.Event.Data["text"].(string); text != "🧠 thinking" {
				t.Fatalf("reasoning.summary text = %q, want %q", text, "🧠 thinking")
			}
		case "tool.called":
			foundToolCalled = true
			if stepID, _ := evt.Event.Data["step_id"].(string); stepID == "" {
				t.Fatalf("tool.called missing step_id")
			}
			if preview, _ := evt.Event.Data["arg_preview"].(string); preview == "" {
				t.Fatalf("tool.called missing arg_preview")
			}
			if _, hasArgs := evt.Event.Data["args"]; hasArgs {
				t.Fatalf("tool.called should not expose raw args in UI payload")
			}
		case "tool.progress":
			if stepID, _ := evt.Event.Data["step_id"].(string); stepID == "" {
				t.Fatalf("tool.progress missing step_id")
			}
			if callID, _ := evt.Event.Data["tool_call_id"].(string); callID == "" {
				t.Fatalf("tool.progress missing tool_call_id")
			}
		case "tool.completed":
			if stepID, _ := evt.Event.Data["step_id"].(string); stepID == "" {
				t.Fatalf("tool.completed missing step_id")
			}
		case "message.completed":
			foundMessageCompleted = true
			if text, _ := evt.Event.Data["text"].(string); text != "done" {
				t.Fatalf("message.completed text = %q, want %q", text, "done")
			}
		}
	}
	if !foundToolCalled {
		t.Fatal("missing tool.called event")
	}
	if !foundMessageCompleted {
		t.Fatal("missing message.completed event")
	}
}

func TestProcessMessage_SuprPublishesRunFailedOnProviderError(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 4,
			},
		},
	}
	msgBus := bus.NewMessageBus()
	provider := &alwaysFailProvider{err: errors.New("provider exploded")}
	al := NewAgentLoop(cfg, msgBus, provider)

	_, _, err := al.processMessage(context.Background(), bus.InboundMessage{
		Channel:  "supr",
		SenderID: "supr-user",
		ChatID:   "supr:test",
		Content:  "hello",
	})
	if err == nil {
		t.Fatal("expected processMessage error")
	}

	events := collectActivityEventsUntilTerminal(t, msgBus, 2*time.Second)
	seen := map[string]bool{}
	for _, evt := range events {
		seen[evt.Event.EventType] = true
	}
	if !seen["run.started"] {
		t.Fatal("expected run.started event")
	}
	if !seen["error.raised"] {
		t.Fatal("expected error.raised event")
	}
	if !seen["run.failed"] {
		t.Fatal("expected run.failed event")
	}
	if seen["run.completed"] {
		t.Fatal("run.completed should not be emitted on failure")
	}
}

func TestProcessMessage_SuprPublishesToolScopedErrorRaised(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 4,
			},
		},
	}
	msgBus := bus.NewMessageBus()
	provider := &toolCallThenFinalProvider{}
	al := NewAgentLoop(cfg, msgBus, provider)

	defaultAgent := al.GetRegistry().GetDefaultAgent()
	if defaultAgent == nil {
		t.Fatal("default agent missing")
	}
	defaultAgent.Tools.Register(&failingTool{})

	response, _, err := al.processMessage(context.Background(), bus.InboundMessage{
		Channel:  "supr",
		SenderID: "supr-user",
		ChatID:   "supr:test",
		Content:  "hello",
	})
	if err != nil {
		t.Fatalf("processMessage() error = %v", err)
	}
	if response != "done" {
		t.Fatalf("response = %q, want %q", response, "done")
	}

	events := collectActivityEventsUntilTerminal(t, msgBus, 2*time.Second)
	var foundToolFailed bool
	var foundErrorRaised bool
	for _, evt := range events {
		if evt.Event.EventType == "tool.failed" {
			foundToolFailed = true
			if stepID, _ := evt.Event.Data["step_id"].(string); stepID == "" {
				t.Fatal("tool.failed missing step_id")
			}
			if toolCallID, _ := evt.Event.Data["tool_call_id"].(string); toolCallID == "" {
				t.Fatal("tool.failed missing tool_call_id")
			}
		}
		if evt.Event.EventType == "error.raised" {
			scope, _ := evt.Event.Data["scope"].(string)
			if scope == "tool" {
				foundErrorRaised = true
				if stepID, _ := evt.Event.Data["step_id"].(string); stepID == "" {
					t.Fatal("tool-scoped error.raised missing step_id")
				}
				if toolCallID, _ := evt.Event.Data["tool_call_id"].(string); toolCallID == "" {
					t.Fatal("tool-scoped error.raised missing tool_call_id")
				}
			}
		}
	}
	if !foundToolFailed {
		t.Fatal("expected tool.failed event")
	}
	if !foundErrorRaised {
		t.Fatal("expected tool-scoped error.raised event")
	}
}

func TestRecordLastChannel(t *testing.T) {
	al, cfg, msgBus, provider, cleanup := newTestAgentLoop(t)
	defer cleanup()

	testChannel := "test-channel"
	if err := al.RecordLastChannel(testChannel); err != nil {
		t.Fatalf("RecordLastChannel failed: %v", err)
	}
	if got := al.state.GetLastChannel(); got != testChannel {
		t.Errorf("Expected channel '%s', got '%s'", testChannel, got)
	}
	al2 := NewAgentLoop(cfg, msgBus, provider)
	if got := al2.state.GetLastChannel(); got != testChannel {
		t.Errorf("Expected persistent channel '%s', got '%s'", testChannel, got)
	}
}

func TestRecordLastChatID(t *testing.T) {
	al, cfg, msgBus, provider, cleanup := newTestAgentLoop(t)
	defer cleanup()

	testChatID := "test-chat-id-123"
	if err := al.RecordLastChatID(testChatID); err != nil {
		t.Fatalf("RecordLastChatID failed: %v", err)
	}
	if got := al.state.GetLastChatID(); got != testChatID {
		t.Errorf("Expected chat ID '%s', got '%s'", testChatID, got)
	}
	al2 := NewAgentLoop(cfg, msgBus, provider)
	if got := al2.state.GetLastChatID(); got != testChatID {
		t.Errorf("Expected persistent chat ID '%s', got '%s'", testChatID, got)
	}
}

func TestNewAgentLoop_StateInitialized(t *testing.T) {
	// Create temp workspace
	tmpDir, err := os.MkdirTemp("", "agent-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create test config
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
	}

	// Create agent loop
	msgBus := bus.NewMessageBus()
	provider := &mockProvider{}
	al := NewAgentLoop(cfg, msgBus, provider)

	// Verify state manager is initialized
	if al.state == nil {
		t.Error("Expected state manager to be initialized")
	}

	// Verify state directory was created
	stateDir := filepath.Join(tmpDir, "state")
	if _, err := os.Stat(stateDir); os.IsNotExist(err) {
		t.Error("Expected state directory to exist")
	}
}

// TestToolRegistry_ToolRegistration verifies tools can be registered and retrieved
func TestToolRegistry_ToolRegistration(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "agent-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
	}

	msgBus := bus.NewMessageBus()
	provider := &mockProvider{}
	al := NewAgentLoop(cfg, msgBus, provider)

	// Register a custom tool
	customTool := &mockCustomTool{}
	al.RegisterTool(customTool)

	// Verify tool is registered by checking it doesn't panic on GetStartupInfo
	// (actual tool retrieval is tested in tools package tests)
	info := al.GetStartupInfo()
	toolsInfo := info["tools"].(map[string]any)
	toolsList := toolsInfo["names"].([]string)

	// Check that our custom tool name is in the list
	found := slices.Contains(toolsList, "mock_custom")
	if !found {
		t.Error("Expected custom tool to be registered")
	}
}

// TestToolContext_Updates verifies tool context helpers work correctly
func TestToolContext_Updates(t *testing.T) {
	ctx := tools.WithToolContext(context.Background(), "telegram", "chat-42")

	if got := tools.ToolChannel(ctx); got != "telegram" {
		t.Errorf("expected channel 'telegram', got %q", got)
	}
	if got := tools.ToolChatID(ctx); got != "chat-42" {
		t.Errorf("expected chatID 'chat-42', got %q", got)
	}

	// Empty context returns empty strings
	if got := tools.ToolChannel(context.Background()); got != "" {
		t.Errorf("expected empty channel from bare context, got %q", got)
	}
}

// TestToolRegistry_GetDefinitions verifies tool definitions can be retrieved
func TestToolRegistry_GetDefinitions(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "agent-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
	}

	msgBus := bus.NewMessageBus()
	provider := &mockProvider{}
	al := NewAgentLoop(cfg, msgBus, provider)

	// Register a test tool and verify it shows up in startup info
	testTool := &mockCustomTool{}
	al.RegisterTool(testTool)

	info := al.GetStartupInfo()
	toolsInfo := info["tools"].(map[string]any)
	toolsList := toolsInfo["names"].([]string)

	// Check that our custom tool name is in the list
	found := slices.Contains(toolsList, "mock_custom")
	if !found {
		t.Error("Expected custom tool to be registered")
	}
}

// TestAgentLoop_GetStartupInfo verifies startup info contains tools
func TestAgentLoop_GetStartupInfo(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "agent-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Workspace = tmpDir
	cfg.Agents.Defaults.Model = "test-model"
	cfg.Agents.Defaults.MaxTokens = 4096
	cfg.Agents.Defaults.MaxToolIterations = 10

	msgBus := bus.NewMessageBus()
	provider := &mockProvider{}
	al := NewAgentLoop(cfg, msgBus, provider)

	info := al.GetStartupInfo()

	// Verify tools info exists
	toolsInfo, ok := info["tools"]
	if !ok {
		t.Fatal("Expected 'tools' key in startup info")
	}

	toolsMap, ok := toolsInfo.(map[string]any)
	if !ok {
		t.Fatal("Expected 'tools' to be a map")
	}

	count, ok := toolsMap["count"]
	if !ok {
		t.Fatal("Expected 'count' in tools info")
	}

	// Should have default tools registered
	if count.(int) == 0 {
		t.Error("Expected at least some tools to be registered")
	}
}

func TestRegisterSharedTools_SpawnPerAgentOverrideDisablesGlobalSpawn(t *testing.T) {
	boolPtr := func(v bool) *bool { return &v }

	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Workspace = t.TempDir()
	cfg.Tools.Spawn.Enabled = true
	cfg.Tools.Subagent.Enabled = true
	cfg.Agents.List = []config.AgentConfig{
		{
			ID:      "alpha",
			Default: true,
			Tools: &config.AgentToolsConfig{
				Spawn: boolPtr(false),
			},
		},
		{
			ID: "beta",
		},
	}

	al := NewAgentLoop(cfg, bus.NewMessageBus(), &mockProvider{})
	registry := al.GetRegistry()

	alpha, ok := registry.GetAgent("alpha")
	if !ok {
		t.Fatal("expected alpha agent")
	}
	if _, exists := alpha.Tools.Get("spawn"); exists {
		t.Fatal("expected spawn tool to be disabled for alpha via per-agent override")
	}

	beta, ok := registry.GetAgent("beta")
	if !ok {
		t.Fatal("expected beta agent")
	}
	if _, exists := beta.Tools.Get("spawn"); !exists {
		t.Fatal("expected spawn tool to remain enabled for beta via global config fallback")
	}
}

func TestRegisterSharedTools_SpawnPerAgentOverrideEnablesWhenGlobalDisabled(t *testing.T) {
	boolPtr := func(v bool) *bool { return &v }

	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Workspace = t.TempDir()
	cfg.Tools.Spawn.Enabled = false
	cfg.Tools.Subagent.Enabled = true
	cfg.Agents.List = []config.AgentConfig{
		{
			ID:      "alpha",
			Default: true,
			Tools: &config.AgentToolsConfig{
				Spawn: boolPtr(true),
			},
		},
		{
			ID: "beta",
		},
	}

	al := NewAgentLoop(cfg, bus.NewMessageBus(), &mockProvider{})
	registry := al.GetRegistry()

	alpha, ok := registry.GetAgent("alpha")
	if !ok {
		t.Fatal("expected alpha agent")
	}
	if _, exists := alpha.Tools.Get("spawn"); !exists {
		t.Fatal("expected spawn tool to be enabled for alpha via per-agent override")
	}

	beta, ok := registry.GetAgent("beta")
	if !ok {
		t.Fatal("expected beta agent")
	}
	if _, exists := beta.Tools.Get("spawn"); exists {
		t.Fatal("expected spawn tool to remain disabled for beta via global config")
	}
}

func TestRegisterSharedTools_SpawnStatusPerAgentOverrideDisablesGlobalSpawnStatus(t *testing.T) {
	boolPtr := func(v bool) *bool { return &v }

	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Workspace = t.TempDir()
	cfg.Tools.Spawn.Enabled = false
	cfg.Tools.SpawnStatus.Enabled = true
	cfg.Tools.Subagent.Enabled = true
	cfg.Agents.List = []config.AgentConfig{
		{
			ID:      "alpha",
			Default: true,
			Tools: &config.AgentToolsConfig{
				SpawnStatus: boolPtr(false),
			},
		},
		{
			ID: "beta",
		},
	}

	al := NewAgentLoop(cfg, bus.NewMessageBus(), &mockProvider{})
	registry := al.GetRegistry()

	alpha, ok := registry.GetAgent("alpha")
	if !ok {
		t.Fatal("expected alpha agent")
	}
	if _, exists := alpha.Tools.Get("spawn_status"); exists {
		t.Fatal("expected spawn_status tool to be disabled for alpha via per-agent override")
	}

	beta, ok := registry.GetAgent("beta")
	if !ok {
		t.Fatal("expected beta agent")
	}
	if _, exists := beta.Tools.Get("spawn_status"); !exists {
		t.Fatal("expected spawn_status tool to remain enabled for beta via global config fallback")
	}
}

func TestRegisterSharedTools_SpawnStatusPerAgentOverrideEnablesWhenGlobalDisabled(t *testing.T) {
	boolPtr := func(v bool) *bool { return &v }

	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Workspace = t.TempDir()
	cfg.Tools.Spawn.Enabled = false
	cfg.Tools.SpawnStatus.Enabled = false
	cfg.Tools.Subagent.Enabled = true
	cfg.Agents.List = []config.AgentConfig{
		{
			ID:      "alpha",
			Default: true,
			Tools: &config.AgentToolsConfig{
				SpawnStatus: boolPtr(true),
			},
		},
		{
			ID: "beta",
		},
	}

	al := NewAgentLoop(cfg, bus.NewMessageBus(), &mockProvider{})
	registry := al.GetRegistry()

	alpha, ok := registry.GetAgent("alpha")
	if !ok {
		t.Fatal("expected alpha agent")
	}
	if _, exists := alpha.Tools.Get("spawn_status"); !exists {
		t.Fatal("expected spawn_status tool to be enabled for alpha via per-agent override")
	}

	beta, ok := registry.GetAgent("beta")
	if !ok {
		t.Fatal("expected beta agent")
	}
	if _, exists := beta.Tools.Get("spawn_status"); exists {
		t.Fatal("expected spawn_status tool to remain disabled for beta via global config")
	}
}

// TestAgentLoop_Stop verifies Stop() sets running to false
func TestAgentLoop_Stop(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "agent-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
	}

	msgBus := bus.NewMessageBus()
	provider := &mockProvider{}
	al := NewAgentLoop(cfg, msgBus, provider)

	// Note: running is only set to true when Run() is called
	// We can't test that without starting the event loop
	// Instead, verify the Stop method can be called safely
	al.Stop()

	// Verify running is false (initial state or after Stop)
	if al.running.Load() {
		t.Error("Expected agent to be stopped (or never started)")
	}
}

func TestAgentLoopRun_AllowsParallelRunsAcrossDifferentAgents(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Agents.MaxParallelRuns = 2
	cfg.Agents.Defaults.Workspace = t.TempDir()
	cfg.Agents.Defaults.Model = "gated-model"
	cfg.Agents.Defaults.MaxToolIterations = 2
	cfg.Agents.Defaults.ContextGuard.Enabled = false
	cfg.Agents.List = []config.AgentConfig{
		{ID: "main", Default: true},
		{ID: "writer"},
	}

	msgBus := bus.NewMessageBus()
	release := make(chan struct{})
	provider := &gatedProvider{
		started: make(chan string, 4),
		release: release,
	}
	al := NewAgentLoop(cfg, msgBus, provider)

	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runDone := make(chan error, 1)
	go func() { runDone <- al.Run(runCtx) }()

	if err := msgBus.PublishInbound(context.Background(), bus.InboundMessage{
		Channel:  "telegram",
		SenderID: "user-1",
		ChatID:   "chat-main",
		Content:  "hello-main",
		Metadata: map[string]string{
			metadataKeyRequestedAgentID: "main",
		},
	}); err != nil {
		t.Fatalf("PublishInbound(main) error = %v", err)
	}
	if err := msgBus.PublishInbound(context.Background(), bus.InboundMessage{
		Channel:  "telegram",
		SenderID: "user-2",
		ChatID:   "chat-writer",
		Content:  "hello-writer",
		Metadata: map[string]string{
			metadataKeyRequestedAgentID: "writer",
		},
	}); err != nil {
		t.Fatalf("PublishInbound(writer) error = %v", err)
	}

	startedSeen := map[string]bool{}
	startDeadline := time.After(2 * time.Second)
	for len(startedSeen) < 2 {
		select {
		case started := <-provider.started:
			startedSeen[started] = true
		case <-startDeadline:
			t.Fatalf("expected two concurrent provider starts, got %v", startedSeen)
		}
	}
	if !startedSeen["hello-main"] || !startedSeen["hello-writer"] {
		t.Fatalf("started set = %v, want both hello-main and hello-writer", startedSeen)
	}

	close(release)

	gotChats := map[string]bool{}
	responseDeadline := time.After(2 * time.Second)
	for len(gotChats) < 2 {
		select {
		case out := <-msgBus.OutboundChan():
			if out.ErrorCode != "" {
				t.Fatalf("unexpected outbound error: %+v", out)
			}
			gotChats[out.ChatID] = true
		case <-responseDeadline:
			t.Fatalf("expected 2 outbound responses, got chats=%v", gotChats)
		}
	}
	if !gotChats["chat-main"] || !gotChats["chat-writer"] {
		t.Fatalf("outbound chats = %v, want chat-main and chat-writer", gotChats)
	}

	cancel()
	select {
	case err := <-runDone:
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run() did not stop after cancel")
	}
}

func TestAgentLoopRun_BusyAgentReturnsRunInProgress(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Agents.MaxParallelRuns = 2
	cfg.Agents.Defaults.Workspace = t.TempDir()
	cfg.Agents.Defaults.Model = "gated-model"
	cfg.Agents.Defaults.MaxToolIterations = 2
	cfg.Agents.Defaults.ContextGuard.Enabled = false

	msgBus := bus.NewMessageBus()
	release := make(chan struct{})
	provider := &gatedProvider{
		started: make(chan string, 2),
		release: release,
	}
	al := NewAgentLoop(cfg, msgBus, provider)

	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runDone := make(chan error, 1)
	go func() { runDone <- al.Run(runCtx) }()

	if err := msgBus.PublishInbound(context.Background(), bus.InboundMessage{
		Channel:  "telegram",
		SenderID: "user-1",
		ChatID:   "chat-1",
		Content:  "first",
		Metadata: map[string]string{
			metadataKeyRequestedAgentID: "main",
		},
	}); err != nil {
		t.Fatalf("PublishInbound(first) error = %v", err)
	}

	select {
	case <-provider.started:
	case <-time.After(2 * time.Second):
		t.Fatal("first run did not reach provider")
	}

	if err := msgBus.PublishInbound(context.Background(), bus.InboundMessage{
		Channel:  "telegram",
		SenderID: "user-2",
		ChatID:   "chat-2",
		Content:  "second",
		Metadata: map[string]string{
			metadataKeyRequestedAgentID: "main",
		},
	}); err != nil {
		t.Fatalf("PublishInbound(second) error = %v", err)
	}

	gotBusy := false
	busyDeadline := time.After(2 * time.Second)
	for !gotBusy {
		select {
		case out := <-msgBus.OutboundChan():
			if out.ChatID != "chat-2" {
				continue
			}
			if out.ErrorCode != ErrCodeRunInProgress {
				t.Fatalf("chat-2 ErrorCode = %q, want %q", out.ErrorCode, ErrCodeRunInProgress)
			}
			gotBusy = true
		case <-busyDeadline:
			t.Fatal("timed out waiting for RUN_IN_PROGRESS on second request")
		}
	}

	close(release)

	gotFirstResponse := false
	responseDeadline := time.After(2 * time.Second)
	for !gotFirstResponse {
		select {
		case out := <-msgBus.OutboundChan():
			if out.ChatID != "chat-1" {
				continue
			}
			if out.ErrorCode != "" {
				t.Fatalf("chat-1 unexpected error: %+v", out)
			}
			gotFirstResponse = true
		case <-responseDeadline:
			t.Fatal("timed out waiting for first request completion")
		}
	}

	cancel()
	select {
	case err := <-runDone:
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run() did not stop after cancel")
	}
}

func TestAgentLoopRun_BusyAgentSameSessionReturnsRunInProgress(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Agents.MaxParallelRuns = 2
	cfg.Agents.Defaults.Workspace = t.TempDir()
	cfg.Agents.Defaults.Model = "gated-model"
	cfg.Agents.Defaults.MaxToolIterations = 2
	cfg.Agents.Defaults.ContextGuard.Enabled = false

	msgBus := bus.NewMessageBus()
	release := make(chan struct{})
	provider := &gatedProvider{
		started: make(chan string, 2),
		release: release,
	}
	al := NewAgentLoop(cfg, msgBus, provider)

	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runDone := make(chan error, 1)
	go func() { runDone <- al.Run(runCtx) }()

	firstMessage := bus.InboundMessage{
		Channel:    "telegram",
		SenderID:   "user-1",
		ChatID:     "chat-shared",
		SessionKey: "session-shared",
		Content:    "first",
		Metadata: map[string]string{
			metadataKeyRequestedAgentID: "main",
		},
	}
	if err := msgBus.PublishInbound(context.Background(), firstMessage); err != nil {
		t.Fatalf("PublishInbound(first) error = %v", err)
	}

	select {
	case <-provider.started:
	case <-time.After(2 * time.Second):
		t.Fatal("first run did not reach provider")
	}

	secondMessage := firstMessage
	secondMessage.Content = "second"
	if err := msgBus.PublishInbound(context.Background(), secondMessage); err != nil {
		t.Fatalf("PublishInbound(second) error = %v", err)
	}

	gotBusy := false
	busyDeadline := time.After(2 * time.Second)
	for !gotBusy {
		select {
		case out := <-msgBus.OutboundChan():
			if out.ErrorCode == ErrCodeRunInProgress {
				gotBusy = true
			}
		case <-busyDeadline:
			t.Fatal("timed out waiting for RUN_IN_PROGRESS on same-session request")
		}
	}

	close(release)

	gotFinal := false
	finalDeadline := time.After(2 * time.Second)
	for !gotFinal {
		select {
		case out := <-msgBus.OutboundChan():
			if out.ChatID != "chat-shared" {
				continue
			}
			if out.ErrorCode == "" {
				gotFinal = true
			}
		case <-finalDeadline:
			t.Fatal("timed out waiting for first same-session request completion")
		}
	}

	cancel()
	select {
	case err := <-runDone:
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run() did not stop after cancel")
	}
}

func TestAgentLoopRun_RejectsReservedHeartbeatSessionKey(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Agents.MaxParallelRuns = 1
	cfg.Agents.Defaults.Workspace = t.TempDir()
	cfg.Agents.Defaults.Model = "mock-model"
	cfg.Agents.Defaults.ContextGuard.Enabled = false

	msgBus := bus.NewMessageBus()
	provider := &recordingProvider{}
	al := NewAgentLoop(cfg, msgBus, provider)

	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runDone := make(chan error, 1)
	go func() { runDone <- al.Run(runCtx) }()

	if err := msgBus.PublishInbound(context.Background(), bus.InboundMessage{
		Channel:    "telegram",
		SenderID:   "user-1",
		ChatID:     "chat-1",
		SessionKey: routing.BuildAgentHeartbeatSessionKey("main"),
		Content:    "hello",
		Metadata: map[string]string{
			metadataKeyRequestedAgentID: "main",
		},
	}); err != nil {
		t.Fatalf("PublishInbound() error = %v", err)
	}

	select {
	case out := <-msgBus.OutboundChan():
		if out.ErrorCode != ErrCodeInvalidSessionKey {
			t.Fatalf("ErrorCode = %q, want %q", out.ErrorCode, ErrCodeInvalidSessionKey)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for invalid session key error")
	}

	if err := msgBus.PublishInbound(context.Background(), bus.InboundMessage{
		Channel:    "telegram",
		SenderID:   "user-2",
		ChatID:     "chat-2",
		SessionKey: routing.BuildAgentHeartbeatRunSessionKey("main", "run-1"),
		Content:    "hello again",
		Metadata: map[string]string{
			metadataKeyRequestedAgentID: "main",
		},
	}); err != nil {
		t.Fatalf("PublishInbound(run-session) error = %v", err)
	}

	select {
	case out := <-msgBus.OutboundChan():
		if out.ErrorCode != ErrCodeInvalidSessionKey {
			t.Fatalf("ErrorCode(run-session) = %q, want %q", out.ErrorCode, ErrCodeInvalidSessionKey)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for invalid run-session key error")
	}

	cancel()
	select {
	case err := <-runDone:
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run() did not stop after cancel")
	}
}

func TestProcessHeartbeat_UsesHeartbeatSessionIsolation(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Workspace = t.TempDir()
	cfg.Agents.Defaults.Model = "mock-model"
	cfg.Agents.Defaults.ContextGuard.Enabled = false

	al := NewAgentLoop(cfg, bus.NewMessageBus(), &recordingProvider{})
	heartbeatSession := routing.BuildAgentHeartbeatRunSessionKey("main", "test-run")

	if _, err := al.ProcessHeartbeat(context.Background(), "main", heartbeatSession, "heartbeat prompt", "telegram", "chat-1", 0); err != nil {
		t.Fatalf("ProcessHeartbeat() error = %v", err)
	}

	agent := al.GetRegistry().GetDefaultAgent()
	if agent == nil {
		t.Fatal("default agent is nil")
	}
	mainSession := routing.BuildAgentMainSessionKey(agent.ID)

	if got := len(agent.Sessions.GetHistory(heartbeatSession)); got == 0 {
		t.Fatalf("heartbeat session history len = %d, want > 0", got)
	}
	if got := len(agent.Sessions.GetHistory(mainSession)); got != 0 {
		t.Fatalf("main session history len = %d, want 0", got)
	}
}

func TestAgentLoopRun_HeartbeatAndChatSameAgentRunConcurrently(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Agents.MaxParallelRuns = 2
	cfg.Agents.Defaults.Workspace = t.TempDir()
	cfg.Agents.Defaults.Model = "gated-model"
	cfg.Agents.Defaults.MaxToolIterations = 2
	cfg.Agents.Defaults.ContextGuard.Enabled = false
	cfg.Session.DMScope = "main"

	msgBus := bus.NewMessageBus()
	release := make(chan struct{})
	provider := &gatedProvider{
		started: make(chan string, 4),
		release: release,
	}
	al := NewAgentLoop(cfg, msgBus, provider)

	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runDone := make(chan error, 1)
	go func() { runDone <- al.Run(runCtx) }()

	heartbeatDone := make(chan error, 1)
	go func() {
		_, err := al.ProcessHeartbeat(
			context.Background(),
			"main",
			routing.BuildAgentHeartbeatRunSessionKey("main", "hb-concurrent"),
			"heartbeat probe",
			"telegram",
			"chat-main",
			0,
		)
		heartbeatDone <- err
	}()

	waitForStartedMessage(t, provider.started, "heartbeat probe", 2*time.Second)

	if err := msgBus.PublishInbound(context.Background(), bus.InboundMessage{
		Channel:  "telegram",
		SenderID: "user-2",
		ChatID:   "chat-main",
		Content:  "live user chat",
		Metadata: map[string]string{
			metadataKeyRequestedAgentID: "main",
		},
	}); err != nil {
		t.Fatalf("PublishInbound(chat) error = %v", err)
	}

	waitForStartedMessage(t, provider.started, "live user chat", 2*time.Second)

	select {
	case out := <-msgBus.OutboundChan():
		if out.ChatID == "chat-main" && out.ErrorCode == ErrCodeRunInProgress {
			t.Fatalf("unexpected run-in-progress while heartbeat active: %+v", out)
		}
	case <-time.After(200 * time.Millisecond):
	}

	close(release)

	select {
	case err := <-heartbeatDone:
		if err != nil {
			t.Fatalf("heartbeat error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for heartbeat completion")
	}

	cancel()
	select {
	case err := <-runDone:
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run() did not stop after cancel")
	}
}

func TestAgentLoopRunStop_OnlyCancelsChatWhenHeartbeatOverlaps(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Agents.MaxParallelRuns = 2
	cfg.Agents.Defaults.Workspace = t.TempDir()
	cfg.Agents.Defaults.Model = "gated-model"
	cfg.Agents.Defaults.MaxToolIterations = 2
	cfg.Agents.Defaults.ContextGuard.Enabled = false
	cfg.Session.DMScope = "main"

	msgBus := bus.NewMessageBus()
	release := make(chan struct{})
	provider := &gatedProvider{
		started: make(chan string, 4),
		release: release,
	}
	al := NewAgentLoop(cfg, msgBus, provider)

	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runDone := make(chan error, 1)
	go func() { runDone <- al.Run(runCtx) }()

	heartbeatDone := make(chan error, 1)
	go func() {
		_, err := al.ProcessHeartbeat(
			context.Background(),
			"main",
			routing.BuildAgentHeartbeatRunSessionKey("main", "hb-overlap"),
			"heartbeat overlap",
			"supr",
			"supr:sess-shared",
			0,
		)
		heartbeatDone <- err
	}()

	waitForStartedMessage(t, provider.started, "heartbeat overlap", 2*time.Second)

	if err := msgBus.PublishInbound(context.Background(), bus.InboundMessage{
		Channel:  "supr",
		SenderID: "user-3",
		ChatID:   "supr:sess-shared",
		Content:  "chat overlap",
		Metadata: map[string]string{
			metadataKeyRequestedAgentID: "main",
		},
	}); err != nil {
		t.Fatalf("PublishInbound(chat) error = %v", err)
	}

	waitForStartedMessage(t, provider.started, "chat overlap", 2*time.Second)

	cancelled, _, err := al.CancelRun("supr", "supr:sess-shared", "", "stop chat")
	if err != nil {
		t.Fatalf("CancelRun() error = %v", err)
	}
	if !cancelled {
		t.Fatal("CancelRun() cancelled = false, want true")
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		_, _, err := al.CancelRun("supr", "supr:sess-shared", "", "probe")
		if err != nil {
			var controlErr *channels.RunControlError
			if errors.As(err, &controlErr) && controlErr.Code == "no_active_run" {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for chat run to clear active run state")
		}
		time.Sleep(20 * time.Millisecond)
	}

	select {
	case err := <-heartbeatDone:
		t.Fatalf("heartbeat finished early after chat cancel: %v", err)
	case <-time.After(200 * time.Millisecond):
	}

	close(release)

	select {
	case err := <-heartbeatDone:
		if err != nil {
			t.Fatalf("heartbeat error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for heartbeat completion")
	}

	cancel()
	select {
	case err := <-runDone:
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run() did not stop after cancel")
	}
}

func TestAgentLoopRun_WorkersShutdownOnCancelAndBusClose(t *testing.T) {
	baseConfig := func(tmp string) *config.Config {
		cfg := config.DefaultConfig()
		cfg.Agents.MaxParallelRuns = 2
		cfg.Agents.Defaults.Workspace = tmp
		cfg.Agents.Defaults.Model = "test-model"
		cfg.Agents.Defaults.ContextGuard.Enabled = false
		return cfg
	}

	t.Run("context cancel", func(t *testing.T) {
		msgBus := bus.NewMessageBus()
		al := NewAgentLoop(baseConfig(t.TempDir()), msgBus, &mockProvider{})

		runCtx, cancel := context.WithCancel(context.Background())
		runDone := make(chan error, 1)
		go func() { runDone <- al.Run(runCtx) }()

		cancel()
		select {
		case err := <-runDone:
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("Run() did not stop after context cancellation")
		}
	})

	t.Run("bus close", func(t *testing.T) {
		msgBus := bus.NewMessageBus()
		al := NewAgentLoop(baseConfig(t.TempDir()), msgBus, &mockProvider{})

		runDone := make(chan error, 1)
		go func() { runDone <- al.Run(context.Background()) }()

		msgBus.Close()
		select {
		case err := <-runDone:
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("Run() did not stop after bus close")
		}
	})
}

func TestAgentLoopRun_MessageToolSuppressesFinalOutbound(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Agents.MaxParallelRuns = 1
	cfg.Agents.Defaults.Workspace = t.TempDir()
	cfg.Agents.Defaults.Model = "test-model"
	cfg.Agents.Defaults.MaxToolIterations = 4
	cfg.Agents.Defaults.ContextGuard.Enabled = false
	cfg.Tools.Message.Enabled = true

	msgBus := bus.NewMessageBus()
	al := NewAgentLoop(cfg, msgBus, &messageToolThenDoneProvider{})

	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runDone := make(chan error, 1)
	go func() { runDone <- al.Run(runCtx) }()

	if err := msgBus.PublishInbound(context.Background(), bus.InboundMessage{
		Channel:  "telegram",
		SenderID: "user",
		ChatID:   "chat-msg",
		Content:  "send",
	}); err != nil {
		t.Fatalf("PublishInbound() error = %v", err)
	}

	var outboundForChat []string
	deadline := time.After(2 * time.Second)
collect:
	for {
		select {
		case out := <-msgBus.OutboundChan():
			if out.ChatID != "chat-msg" {
				continue
			}
			if out.ErrorCode != "" {
				t.Fatalf("unexpected error outbound: %+v", out)
			}
			outboundForChat = append(outboundForChat, out.Content)
			if len(outboundForChat) == 1 {
				if out.Content != "sent via message tool" {
					t.Fatalf("first outbound content = %q, want %q", out.Content, "sent via message tool")
				}
				// Wait briefly for potential leaked final response.
				time.Sleep(250 * time.Millisecond)
				break collect
			}
		case <-deadline:
			t.Fatal("timed out waiting for outbound message")
		}
	}

	// Drain any remaining immediate messages for this chat.
drain:
	for {
		select {
		case out := <-msgBus.OutboundChan():
			if out.ChatID == "chat-msg" && out.ErrorCode == "" {
				outboundForChat = append(outboundForChat, out.Content)
			}
		default:
			break drain
		}
	}

	if len(outboundForChat) != 1 {
		t.Fatalf("outbound count for chat-msg = %d, want 1; contents=%v", len(outboundForChat), outboundForChat)
	}

	cancel()
	select {
	case err := <-runDone:
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run() did not stop after cancel")
	}
}

func TestAgentLoopRun_MessageStateDoesNotLeakAcrossParallelAgents(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Agents.MaxParallelRuns = 2
	cfg.Agents.Defaults.Workspace = t.TempDir()
	cfg.Agents.Defaults.Model = "test-model"
	cfg.Agents.Defaults.MaxToolIterations = 4
	cfg.Agents.Defaults.ContextGuard.Enabled = false
	cfg.Tools.Message.Enabled = true
	cfg.Agents.List = []config.AgentConfig{
		{ID: "main", Default: true},
		{ID: "writer"},
	}

	msgBus := bus.NewMessageBus()
	al := NewAgentLoop(cfg, msgBus, &messageStateIsolationProvider{})

	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runDone := make(chan error, 1)
	go func() { runDone <- al.Run(runCtx) }()

	if err := msgBus.PublishInbound(context.Background(), bus.InboundMessage{
		Channel:  "telegram",
		SenderID: "user-main",
		ChatID:   "chat-msg",
		Content:  "use-message",
		Metadata: map[string]string{
			metadataKeyRequestedAgentID: "main",
		},
	}); err != nil {
		t.Fatalf("PublishInbound(main) error = %v", err)
	}
	if err := msgBus.PublishInbound(context.Background(), bus.InboundMessage{
		Channel:  "telegram",
		SenderID: "user-writer",
		ChatID:   "chat-plain",
		Content:  "plain",
		Metadata: map[string]string{
			metadataKeyRequestedAgentID: "writer",
		},
	}); err != nil {
		t.Fatalf("PublishInbound(writer) error = %v", err)
	}

	messagesByChat := map[string][]string{}
	deadline := time.After(2 * time.Second)
	for len(messagesByChat["chat-msg"]) == 0 || len(messagesByChat["chat-plain"]) == 0 {
		select {
		case out := <-msgBus.OutboundChan():
			if out.ErrorCode != "" {
				t.Fatalf("unexpected error outbound: %+v", out)
			}
			messagesByChat[out.ChatID] = append(messagesByChat[out.ChatID], out.Content)
		case <-deadline:
			t.Fatalf("timed out waiting for expected outbounds; got=%v", messagesByChat)
		}
	}

	// Wait briefly to catch leaked/suppressed extra final outputs.
	time.Sleep(250 * time.Millisecond)
drain:
	for {
		select {
		case out := <-msgBus.OutboundChan():
			if out.ErrorCode == "" {
				messagesByChat[out.ChatID] = append(messagesByChat[out.ChatID], out.Content)
			}
		default:
			break drain
		}
	}

	if len(messagesByChat["chat-msg"]) != 1 || messagesByChat["chat-msg"][0] != "from message tool" {
		t.Fatalf("chat-msg outbounds = %v, want [from message tool]", messagesByChat["chat-msg"])
	}
	if len(messagesByChat["chat-plain"]) != 1 || messagesByChat["chat-plain"][0] != "plain-final" {
		t.Fatalf("chat-plain outbounds = %v, want [plain-final]", messagesByChat["chat-plain"])
	}

	cancel()
	select {
	case err := <-runDone:
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run() did not stop after cancel")
	}
}

func TestAgentLoopRun_NoMessageSendPublishesFinalResponse(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Agents.MaxParallelRuns = 1
	cfg.Agents.Defaults.Workspace = t.TempDir()
	cfg.Agents.Defaults.Model = "test-model"
	cfg.Agents.Defaults.ContextGuard.Enabled = false
	cfg.Tools.Message.Enabled = true

	msgBus := bus.NewMessageBus()
	al := NewAgentLoop(cfg, msgBus, &simpleMockProvider{response: "plain-final"})

	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runDone := make(chan error, 1)
	go func() { runDone <- al.Run(runCtx) }()

	if err := msgBus.PublishInbound(context.Background(), bus.InboundMessage{
		Channel:  "telegram",
		SenderID: "user",
		ChatID:   "chat-plain",
		Content:  "hello",
	}); err != nil {
		t.Fatalf("PublishInbound() error = %v", err)
	}

	select {
	case out := <-msgBus.OutboundChan():
		if out.ErrorCode != "" {
			t.Fatalf("unexpected error outbound: %+v", out)
		}
		if out.Content != "plain-final" {
			t.Fatalf("outbound content = %q, want %q", out.Content, "plain-final")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for final response outbound")
	}

	cancel()
	select {
	case err := <-runDone:
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run() did not stop after cancel")
	}
}

// Mock implementations for testing

type simpleMockProvider struct {
	response string
}

func (m *simpleMockProvider) Chat(
	ctx context.Context,
	messages []providers.Message,
	tools []providers.ToolDefinition,
	model string,
	opts map[string]any,
) (*providers.LLMResponse, error) {
	return &providers.LLMResponse{
		Content:   m.response,
		ToolCalls: []providers.ToolCall{},
	}, nil
}

func (m *simpleMockProvider) GetDefaultModel() string {
	return "mock-model"
}

type countingMockProvider struct {
	response string
	calls    int
}

func (m *countingMockProvider) Chat(
	ctx context.Context,
	messages []providers.Message,
	tools []providers.ToolDefinition,
	model string,
	opts map[string]any,
) (*providers.LLMResponse, error) {
	m.calls++
	return &providers.LLMResponse{
		Content:   m.response,
		ToolCalls: []providers.ToolCall{},
	}, nil
}

func (m *countingMockProvider) GetDefaultModel() string {
	return "counting-mock-model"
}

type thinkingCaptureProvider struct {
	response         string
	calls            int
	lastModel        string
	lastOpts         map[string]any
	supportsThinking bool
}

func (m *thinkingCaptureProvider) Chat(
	ctx context.Context,
	messages []providers.Message,
	tools []providers.ToolDefinition,
	model string,
	opts map[string]any,
) (*providers.LLMResponse, error) {
	m.calls++
	m.lastModel = model
	m.lastOpts = make(map[string]any, len(opts))
	for k, v := range opts {
		m.lastOpts[k] = v
	}
	return &providers.LLMResponse{
		Content:   m.response,
		ToolCalls: []providers.ToolCall{},
	}, nil
}

func (m *thinkingCaptureProvider) GetDefaultModel() string {
	return "thinking-capture-model"
}

func (m *thinkingCaptureProvider) SupportsThinking() bool {
	return m.supportsThinking
}

type blockingCancelProvider struct {
	startOnce sync.Once
}

func (p *blockingCancelProvider) Chat(
	ctx context.Context,
	messages []providers.Message,
	tools []providers.ToolDefinition,
	model string,
	opts map[string]any,
) (*providers.LLMResponse, error) {
	p.startOnce.Do(func() {})
	<-ctx.Done()
	return nil, ctx.Err()
}

func (p *blockingCancelProvider) GetDefaultModel() string {
	return "blocking-cancel-model"
}

type gatedProvider struct {
	started chan string
	release <-chan struct{}
}

func (p *gatedProvider) Chat(
	ctx context.Context,
	messages []providers.Message,
	tools []providers.ToolDefinition,
	model string,
	opts map[string]any,
) (*providers.LLMResponse, error) {
	lastContent := ""
	if len(messages) > 0 {
		lastContent = messages[len(messages)-1].Content
	}
	select {
	case p.started <- lastContent:
	default:
	}

	select {
	case <-p.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	return &providers.LLMResponse{
		Content:   "ok",
		ToolCalls: []providers.ToolCall{},
	}, nil
}

func (p *gatedProvider) GetDefaultModel() string {
	return "gated-provider-model"
}

func waitForStartedMessage(t *testing.T, started <-chan string, want string, timeout time.Duration) {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case got := <-started:
			if got == want {
				return
			}
		case <-deadline:
			t.Fatalf("timed out waiting for provider started message %q", want)
		}
	}
}

type messageStateIsolationProvider struct{}

func (p *messageStateIsolationProvider) Chat(
	ctx context.Context,
	messages []providers.Message,
	tools []providers.ToolDefinition,
	model string,
	opts map[string]any,
) (*providers.LLMResponse, error) {
	lastUser := ""
	hasToolResult := false
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "tool" {
			hasToolResult = true
		}
		if messages[i].Role == "user" && lastUser == "" {
			lastUser = messages[i].Content
		}
	}

	if strings.Contains(lastUser, "use-message") {
		if !hasToolResult {
			return &providers.LLMResponse{
				ToolCalls: []providers.ToolCall{
					{
						ID:   "tc-msg-state",
						Name: "message",
						Arguments: map[string]any{
							"content": "from message tool",
						},
					},
				},
			}, nil
		}
		return &providers.LLMResponse{
			Content:   "",
			ToolCalls: []providers.ToolCall{},
		}, nil
	}

	return &providers.LLMResponse{
		Content:   "plain-final",
		ToolCalls: []providers.ToolCall{},
	}, nil
}

func (p *messageStateIsolationProvider) GetDefaultModel() string {
	return "message-state-isolation-model"
}

// mockCustomTool is a simple mock tool for registration testing
type mockCustomTool struct{}

func (m *mockCustomTool) Name() string {
	return "mock_custom"
}

func (m *mockCustomTool) Description() string {
	return "Mock custom tool for testing"
}

func (m *mockCustomTool) Parameters() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}
}

func (m *mockCustomTool) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	return tools.SilentResult("Custom tool executed")
}

// testHelper executes a message and returns the response
type testHelper struct {
	al *AgentLoop
}

func (h testHelper) executeAndGetResponse(tb testing.TB, ctx context.Context, msg bus.InboundMessage) string {
	// Use a short timeout to avoid hanging
	timeoutCtx, cancel := context.WithTimeout(ctx, responseTimeout)
	defer cancel()

	response, _, err := h.al.processMessage(timeoutCtx, msg)
	if err != nil {
		tb.Fatalf("processMessage failed: %v", err)
	}
	return response
}

const responseTimeout = 3 * time.Second

func collectActivityEventsUntilRunStopped(
	t *testing.T,
	msgBus *bus.MessageBus,
	timeout time.Duration,
) []bus.OutboundActivityEvent {
	t.Helper()

	deadline := time.After(timeout)
	events := make([]bus.OutboundActivityEvent, 0, 16)
	for {
		select {
		case <-deadline:
			types := make([]string, 0, len(events))
			for _, evt := range events {
				code, _ := evt.Event.Data["error_code"].(string)
				if code != "" {
					types = append(types, fmt.Sprintf("%s(%s)", evt.Event.EventType, code))
					continue
				}
				types = append(types, evt.Event.EventType)
			}
			t.Fatalf("timed out waiting for RUN_CANCELLED run.failed, collected=%d events=%v", len(events), types)
		case evt, ok := <-msgBus.OutboundActivityChan():
			if !ok {
				t.Fatalf("outbound activity channel closed unexpectedly")
			}
			events = append(events, evt)
			if evt.Event.EventType == "run.failed" {
				if code, _ := evt.Event.Data["error_code"].(string); code == runCancelledErrorCode {
					return events
				}
			}
		}
	}
}

func waitForRunStartedEvent(
	t *testing.T,
	msgBus *bus.MessageBus,
	timeout time.Duration,
) bus.OutboundActivityEvent {
	t.Helper()

	deadline := time.After(timeout)
	for {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for run.started event")
		case evt, ok := <-msgBus.OutboundActivityChan():
			if !ok {
				t.Fatal("outbound activity channel closed unexpectedly")
			}
			if evt.Event.EventType == "run.started" {
				return evt
			}
		}
	}
}

func TestAgentLoopCancelRun_StopsActiveRunAndEmitsStoppedEvents(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "agent-cancel-test-*")
	if err != nil {
		t.Fatalf("MkdirTemp() error = %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "blocking-cancel-model",
				MaxTokens:         8192,
				MaxToolIterations: 4,
				ContextGuard: config.ContextGuardConfig{
					Enabled: false,
				},
			},
		},
	}

	msgBus := bus.NewMessageBus()
	provider := &blockingCancelProvider{}
	al := NewAgentLoop(cfg, msgBus, provider)

	msg := bus.InboundMessage{
		Channel:  "supr",
		SenderID: "supr-user",
		ChatID:   "supr:sess-cancel",
		Content:  "please run",
		Peer: bus.Peer{
			Kind: "direct",
			ID:   "supr:sess-cancel",
		},
	}

	done := make(chan struct{})
	var response string
	var modelUsed string
	var runErr error
	go func() {
		defer close(done)
		response, modelUsed, runErr = al.processMessage(context.Background(), msg)
	}()

	started := waitForRunStartedEvent(t, msgBus, 3*time.Second)
	runID := started.Event.RunID

	cancelled, resolvedRunID, err := al.CancelRun("supr", "supr:sess-cancel", runID, "")
	if err != nil {
		t.Fatalf("CancelRun() error = %v", err)
	}
	if !cancelled {
		t.Fatal("CancelRun() cancelled = false, want true")
	}
	if resolvedRunID != runID {
		t.Fatalf("resolved run id = %q, want %q", resolvedRunID, runID)
	}

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("processMessage did not return after cancellation")
	}

	if runErr != nil {
		t.Fatalf("processMessage() error = %v", runErr)
	}
	if response != "" {
		t.Fatalf("processMessage() response = %q, want empty after cancellation", response)
	}
	if modelUsed != "" {
		t.Fatalf("processMessage() modelUsed = %q, want empty after cancellation", modelUsed)
	}

	events := collectActivityEventsUntilRunStopped(t, msgBus, 2*time.Second)
	seenStoppedMessage := false
	seenRunFailed := false
	seenRunCompleted := false
	seenToolCalled := false
	messageIndex := -1
	failedIndex := -1

	for i, evt := range events {
		switch evt.Event.EventType {
		case "message.completed":
			if text, _ := evt.Event.Data["text"].(string); text == defaultRunStoppedMessage {
				seenStoppedMessage = true
				messageIndex = i
			}
		case "run.failed":
			if code, _ := evt.Event.Data["error_code"].(string); code == runCancelledErrorCode {
				seenRunFailed = true
				failedIndex = i
			}
		case "run.completed":
			seenRunCompleted = true
		case "tool.called":
			seenToolCalled = true
		}
	}

	if !seenStoppedMessage {
		t.Fatal("missing message.completed stop message")
	}
	if !seenRunFailed {
		t.Fatal("missing run.failed RUN_CANCELLED event")
	}
	if seenRunCompleted {
		t.Fatal("unexpected run.completed event for cancelled run")
	}
	if seenToolCalled {
		t.Fatal("unexpected tool.called after cancellation")
	}
	if failedIndex <= messageIndex {
		t.Fatalf("event order invalid: run.failed index=%d, message.completed index=%d", failedIndex, messageIndex)
	}

	_, _, err = al.CancelRun("supr", "supr:sess-cancel", "", "")
	if err == nil {
		t.Fatal("expected no_active_run after run completion")
	}
	var controlErr *channels.RunControlError
	if !errors.As(err, &controlErr) {
		t.Fatalf("CancelRun() error type = %T, want *channels.RunControlError", err)
	}
	if controlErr.Code != "no_active_run" {
		t.Fatalf("CancelRun() code = %q, want %q", controlErr.Code, "no_active_run")
	}
}

func TestProcessMessage_UsesRouteSessionKey(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "agent-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
	}

	msgBus := bus.NewMessageBus()
	provider := &simpleMockProvider{response: "ok"}
	al := NewAgentLoop(cfg, msgBus, provider)

	msg := bus.InboundMessage{
		Channel:  "telegram",
		SenderID: "user1",
		ChatID:   "chat1",
		Content:  "hello",
		Peer: bus.Peer{
			Kind: "direct",
			ID:   "user1",
		},
	}

	route := al.registry.ResolveRoute(routing.RouteInput{
		Channel: msg.Channel,
		Peer:    extractPeer(msg),
	})
	sessionKey := route.SessionKey

	defaultAgent := al.registry.GetDefaultAgent()
	if defaultAgent == nil {
		t.Fatal("No default agent found")
	}

	helper := testHelper{al: al}
	_ = helper.executeAndGetResponse(t, context.Background(), msg)

	history := defaultAgent.Sessions.GetHistory(sessionKey)
	if len(history) != 2 {
		t.Fatalf("expected session history len=2, got %d", len(history))
	}
	if history[0].Role != "user" || history[0].Content != "hello" {
		t.Fatalf("unexpected first message in session: %+v", history[0])
	}
}

func TestProcessMessage_CommandOutcomes(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "agent-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
		Session: config.SessionConfig{
			DMScope: "per-channel-peer",
		},
	}

	msgBus := bus.NewMessageBus()
	provider := &countingMockProvider{response: "LLM reply"}
	al := NewAgentLoop(cfg, msgBus, provider)
	helper := testHelper{al: al}

	baseMsg := bus.InboundMessage{
		Channel:  "whatsapp",
		SenderID: "user1",
		ChatID:   "chat1",
		Peer: bus.Peer{
			Kind: "direct",
			ID:   "user1",
		},
	}

	showResp := helper.executeAndGetResponse(t, context.Background(), bus.InboundMessage{
		Channel:  baseMsg.Channel,
		SenderID: baseMsg.SenderID,
		ChatID:   baseMsg.ChatID,
		Content:  "/show channel",
		Peer:     baseMsg.Peer,
	})
	if showResp != "Current Channel: whatsapp" {
		t.Fatalf("unexpected /show reply: %q", showResp)
	}
	if provider.calls != 0 {
		t.Fatalf("LLM should not be called for handled command, calls=%d", provider.calls)
	}

	fooResp := helper.executeAndGetResponse(t, context.Background(), bus.InboundMessage{
		Channel:  baseMsg.Channel,
		SenderID: baseMsg.SenderID,
		ChatID:   baseMsg.ChatID,
		Content:  "/foo",
		Peer:     baseMsg.Peer,
	})
	if fooResp != "LLM reply" {
		t.Fatalf("unexpected /foo reply: %q", fooResp)
	}
	if provider.calls != 1 {
		t.Fatalf("LLM should be called exactly once after /foo passthrough, calls=%d", provider.calls)
	}

	newResp := helper.executeAndGetResponse(t, context.Background(), bus.InboundMessage{
		Channel:  baseMsg.Channel,
		SenderID: baseMsg.SenderID,
		ChatID:   baseMsg.ChatID,
		Content:  "/new",
		Peer:     baseMsg.Peer,
	})
	if newResp != "Started a new conversation." {
		t.Fatalf("unexpected /new reply: %q", newResp)
	}
	if provider.calls != 1 {
		t.Fatalf("LLM should not be called for handled /new command, calls=%d", provider.calls)
	}
}

func TestProcessMessage_CompactCommandCompactsSession(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "agent-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
		Session: config.SessionConfig{
			DMScope: "per-channel-peer",
		},
	}

	msgBus := bus.NewMessageBus()
	provider := &simpleMockProvider{response: "summary result"}
	al := NewAgentLoop(cfg, msgBus, provider)
	helper := testHelper{al: al}

	defaultAgent := al.registry.GetDefaultAgent()
	if defaultAgent == nil {
		t.Fatal("No default agent found")
	}
	sessionKey := "agent:main:whatsapp:direct:user1"
	defaultAgent.Sessions.SetHistory(sessionKey, []providers.Message{
		{Role: "user", Content: "u1"},
		{Role: "assistant", Content: "a1"},
		{Role: "user", Content: "u2"},
		{Role: "assistant", Content: "a2"},
		{Role: "user", Content: "u3"},
		{Role: "assistant", Content: "a3"},
		{Role: "user", Content: "u4"},
		{Role: "assistant", Content: "a4"},
	})

	resp := helper.executeAndGetResponse(t, context.Background(), bus.InboundMessage{
		Channel:  "whatsapp",
		SenderID: "user1",
		ChatID:   "chat1",
		Content:  "/compact",
		Peer: bus.Peer{
			Kind: "direct",
			ID:   "user1",
		},
	})
	if resp != "Conversation compacted!" {
		t.Fatalf("unexpected /compact reply: %q", resp)
	}

	history := defaultAgent.Sessions.GetHistory(sessionKey)
	if len(history) > 4 {
		t.Fatalf("expected compacted history <= 4 messages, got %d", len(history))
	}
	if defaultAgent.Sessions.GetSummary(sessionKey) == "" {
		t.Fatal("expected non-empty summary after /compact")
	}
}

func TestProcessMessage_SwitchModelShowModelConsistency(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "agent-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Provider:          "openai",
				Model:             "before-switch",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
	}

	msgBus := bus.NewMessageBus()
	provider := &countingMockProvider{response: "LLM reply"}
	al := NewAgentLoop(cfg, msgBus, provider)
	helper := testHelper{al: al}

	switchResp := helper.executeAndGetResponse(t, context.Background(), bus.InboundMessage{
		Channel:  "telegram",
		SenderID: "user1",
		ChatID:   "chat1",
		Content:  "/switch model to after-switch",
		Peer: bus.Peer{
			Kind: "direct",
			ID:   "user1",
		},
	})
	if !strings.Contains(switchResp, "Switched model from before-switch to after-switch") {
		t.Fatalf("unexpected /switch reply: %q", switchResp)
	}

	showResp := helper.executeAndGetResponse(t, context.Background(), bus.InboundMessage{
		Channel:  "telegram",
		SenderID: "user1",
		ChatID:   "chat1",
		Content:  "/show model",
		Peer: bus.Peer{
			Kind: "direct",
			ID:   "user1",
		},
	})
	if !strings.Contains(showResp, "Current Model: after-switch (Provider: openai)") {
		t.Fatalf("unexpected /show model reply after switch: %q", showResp)
	}

	if provider.calls != 0 {
		t.Fatalf("LLM should not be called for /switch and /show, calls=%d", provider.calls)
	}
}

func TestProcessMessage_ReasoningOverrideAppliesPerTurn(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "agent-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "base-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
	}

	msgBus := bus.NewMessageBus()
	provider := &thinkingCaptureProvider{response: "LLM reply", supportsThinking: true}
	al := NewAgentLoop(cfg, msgBus, provider)
	helper := testHelper{al: al}

	defaultAgent := al.registry.GetDefaultAgent()
	if defaultAgent == nil {
		t.Fatal("No default agent found")
	}
	defaultAgent.ThinkingLevel = ThinkingHigh

	resp := helper.executeAndGetResponse(t, context.Background(), bus.InboundMessage{
		Channel:  "telegram",
		SenderID: "user1",
		ChatID:   "chat1",
		Content:  "hello",
		Peer: bus.Peer{
			Kind: "direct",
			ID:   "user1",
		},
		Metadata: map[string]string{
			metadataKeyReasoningOverride: "low",
		},
	})
	if resp != "LLM reply" {
		t.Fatalf("unexpected response: %q", resp)
	}
	if got, ok := provider.lastOpts["thinking_level"].(string); !ok || got != "low" {
		t.Fatalf("thinking_level override not applied, got=%v", provider.lastOpts["thinking_level"])
	}
	if defaultAgent.ThinkingLevel != ThinkingHigh {
		t.Fatalf("agent thinking level mutated unexpectedly: got=%q want=%q", defaultAgent.ThinkingLevel, ThinkingHigh)
	}
}

func TestProcessMessage_ModelAndReasoningOverrideApplyTogether(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "agent-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "base-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
	}

	msgBus := bus.NewMessageBus()
	provider := &thinkingCaptureProvider{response: "LLM reply", supportsThinking: true}
	al := NewAgentLoop(cfg, msgBus, provider)
	helper := testHelper{al: al}

	_ = helper.executeAndGetResponse(t, context.Background(), bus.InboundMessage{
		Channel:  "telegram",
		SenderID: "user1",
		ChatID:   "chat1",
		Content:  "hello",
		Peer: bus.Peer{
			Kind: "direct",
			ID:   "user1",
		},
		Metadata: map[string]string{
			metadataKeyModelOverride:     "override-model",
			metadataKeyReasoningOverride: "xhigh",
		},
	})
	if provider.lastModel != "override-model" {
		t.Fatalf("model override not applied, got=%q", provider.lastModel)
	}
	if got, ok := provider.lastOpts["thinking_level"].(string); !ok || got != "xhigh" {
		t.Fatalf("thinking_level override not applied, got=%v", provider.lastOpts["thinking_level"])
	}
}

func TestProcessMessage_SwitchReasoningPersistsOnAgent(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "agent-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "base-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
	}

	msgBus := bus.NewMessageBus()
	provider := &thinkingCaptureProvider{response: "LLM reply", supportsThinking: true}
	al := NewAgentLoop(cfg, msgBus, provider)
	helper := testHelper{al: al}

	switchResp := helper.executeAndGetResponse(t, context.Background(), bus.InboundMessage{
		Channel:  "telegram",
		SenderID: "user1",
		ChatID:   "chat1",
		Content:  "/switch reasoning to high",
		Peer: bus.Peer{
			Kind: "direct",
			ID:   "user1",
		},
	})
	if !strings.Contains(switchResp, "Switched reasoning from off to high") {
		t.Fatalf("unexpected /switch reasoning reply: %q", switchResp)
	}
	if provider.calls != 0 {
		t.Fatalf("LLM should not be called for /switch reasoning, calls=%d", provider.calls)
	}

	_ = helper.executeAndGetResponse(t, context.Background(), bus.InboundMessage{
		Channel:  "telegram",
		SenderID: "user1",
		ChatID:   "chat1",
		Content:  "hello",
		Peer: bus.Peer{
			Kind: "direct",
			ID:   "user1",
		},
	})
	if got, ok := provider.lastOpts["thinking_level"].(string); !ok || got != "high" {
		t.Fatalf("thinking_level after /switch reasoning = %v, want high", provider.lastOpts["thinking_level"])
	}
}

func TestProcessMessage_SwitchReasoningRejectsInvalidValue(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "agent-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "base-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
	}

	msgBus := bus.NewMessageBus()
	provider := &thinkingCaptureProvider{response: "LLM reply", supportsThinking: true}
	al := NewAgentLoop(cfg, msgBus, provider)
	helper := testHelper{al: al}

	reply := helper.executeAndGetResponse(t, context.Background(), bus.InboundMessage{
		Channel:  "telegram",
		SenderID: "user1",
		ChatID:   "chat1",
		Content:  "/switch reasoning to invalid",
		Peer: bus.Peer{
			Kind: "direct",
			ID:   "user1",
		},
	})
	if !strings.Contains(reply, "invalid reasoning level") {
		t.Fatalf("unexpected /switch reasoning error reply: %q", reply)
	}
	if provider.calls != 0 {
		t.Fatalf("LLM should not be called for invalid /switch reasoning, calls=%d", provider.calls)
	}
}

// TestToolResult_SilentToolDoesNotSendUserMessage verifies silent tools don't trigger outbound
func TestToolResult_SilentToolDoesNotSendUserMessage(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "agent-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
	}

	msgBus := bus.NewMessageBus()
	provider := &simpleMockProvider{response: "File operation complete"}
	al := NewAgentLoop(cfg, msgBus, provider)
	helper := testHelper{al: al}

	// ReadFileTool returns SilentResult, which should not send user message
	ctx := context.Background()
	msg := bus.InboundMessage{
		Channel:    "test",
		SenderID:   "user1",
		ChatID:     "chat1",
		Content:    "read test.txt",
		SessionKey: "test-session",
	}

	response := helper.executeAndGetResponse(t, ctx, msg)

	// Silent tool should return the LLM's response directly
	if response != "File operation complete" {
		t.Errorf("Expected 'File operation complete', got: %s", response)
	}
}

// TestToolResult_UserFacingToolDoesSendMessage verifies user-facing tools trigger outbound
func TestToolResult_UserFacingToolDoesSendMessage(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "agent-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
	}

	msgBus := bus.NewMessageBus()
	provider := &simpleMockProvider{response: "Command output: hello world"}
	al := NewAgentLoop(cfg, msgBus, provider)
	helper := testHelper{al: al}

	// ExecTool returns UserResult, which should send user message
	ctx := context.Background()
	msg := bus.InboundMessage{
		Channel:    "test",
		SenderID:   "user1",
		ChatID:     "chat1",
		Content:    "run hello",
		SessionKey: "test-session",
	}

	response := helper.executeAndGetResponse(t, ctx, msg)

	// User-facing tool should include the output in final response
	if response != "Command output: hello world" {
		t.Errorf("Expected 'Command output: hello world', got: %s", response)
	}
}

// failFirstMockProvider fails on the first N calls with a specific error
type failFirstMockProvider struct {
	failures    int
	currentCall int
	failError   error
	successResp string
}

func (m *failFirstMockProvider) Chat(
	ctx context.Context,
	messages []providers.Message,
	tools []providers.ToolDefinition,
	model string,
	opts map[string]any,
) (*providers.LLMResponse, error) {
	m.currentCall++
	if m.currentCall <= m.failures {
		return nil, m.failError
	}
	return &providers.LLMResponse{
		Content:   m.successResp,
		ToolCalls: []providers.ToolCall{},
	}, nil
}

func (m *failFirstMockProvider) GetDefaultModel() string {
	return "mock-fail-model"
}

// TestAgentLoop_ContextExhaustionRetry verify that the agent retries on context errors
func TestAgentLoop_ContextExhaustionRetry(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "agent-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
	}

	msgBus := bus.NewMessageBus()

	// Create a provider that fails once with a context error
	contextErr := fmt.Errorf("InvalidParameter: Total tokens of image and text exceed max message tokens")
	provider := &failFirstMockProvider{
		failures:    1,
		failError:   contextErr,
		successResp: "Recovered from context error",
	}

	al := NewAgentLoop(cfg, msgBus, provider)

	// Inject some history to simulate a full context
	sessionKey := "test-session-context"
	// Create dummy history
	history := []providers.Message{
		{Role: "system", Content: "System prompt"},
		{Role: "user", Content: "Old message 1"},
		{Role: "assistant", Content: "Old response 1"},
		{Role: "user", Content: "Old message 2"},
		{Role: "assistant", Content: "Old response 2"},
		{Role: "user", Content: "Trigger message"},
	}
	defaultAgent := al.registry.GetDefaultAgent()
	if defaultAgent == nil {
		t.Fatal("No default agent found")
	}
	defaultAgent.ContextWindow = 16384
	defaultAgent.Sessions.SetHistory(sessionKey, history)

	// Call ProcessDirectWithChannel
	// Note: ProcessDirectWithChannel calls processMessage which will execute runLLMIteration
	response, err := al.ProcessDirectWithChannel(
		context.Background(),
		"Trigger message",
		sessionKey,
		"test",
		"test-chat",
	)
	if err != nil {
		t.Fatalf("Expected success after retry, got error: %v", err)
	}

	if response != "Recovered from context error" {
		t.Errorf("Expected 'Recovered from context error', got '%s'", response)
	}

	// We expect 2 calls: 1st failed, 2nd succeeded
	if provider.currentCall != 2 {
		t.Errorf("Expected 2 calls (1 fail + 1 success), got %d", provider.currentCall)
	}

	// Check final history length
	finalHistory := defaultAgent.Sessions.GetHistory(sessionKey)
	// We verify that the history has been modified (compressed)
	// Original length: 6
	// Expected behavior: compression drops ~50% of history (mid slice)
	// We can assert that the length is NOT what it would be without compression.
	// Without compression: 6 + 1 (new user msg) + 1 (assistant msg) = 8
	if len(finalHistory) >= 8 {
		t.Errorf("Expected history to be compressed (len < 8), got %d", len(finalHistory))
	}
}

// TestProcessDirectWithChannel_TriggersMCPInitialization verifies that
// ProcessDirectWithChannel triggers MCP initialization when MCP is enabled.
// Note: Manager is only initialized when at least one MCP server is configured
// and successfully connected.
func TestProcessDirectWithChannel_TriggersMCPInitialization(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "agent-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Test with MCP enabled but no servers - should not initialize manager
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
		Tools: config.ToolsConfig{
			MCP: config.MCPConfig{
				ToolConfig: config.ToolConfig{
					Enabled: true,
				},
				// No servers configured - manager should not be initialized
			},
		},
	}

	msgBus := bus.NewMessageBus()
	provider := &mockProvider{}
	al := NewAgentLoop(cfg, msgBus, provider)
	defer al.Close()

	if al.mcp.hasManager() {
		t.Fatal("expected MCP manager to be nil before first direct processing")
	}

	_, err = al.ProcessDirectWithChannel(
		context.Background(),
		"hello",
		"session-1",
		"cli",
		"direct",
	)
	if err != nil {
		t.Fatalf("ProcessDirectWithChannel failed: %v", err)
	}

	// Manager should not be initialized when no servers are configured
	if al.mcp.hasManager() {
		t.Fatal("expected MCP manager to be nil when no servers are configured")
	}
}

func TestReloadProviderAndConfig_RebindsMCPTools(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := newTestConfigWithEnabledMCP(tmpDir)
	msgBus := bus.NewMessageBus()
	provider := &mockProvider{}
	al := NewAgentLoop(cfg, msgBus, provider)
	defer al.Close()

	firstManager := newFakeLoopMCPManagerWithSupabaseTool("execute_sql")
	secondManager := newFakeLoopMCPManagerWithSupabaseTool("execute_sql")
	managerQueue := []mcpManagerRuntime{firstManager, secondManager}
	oldFactory := newMCPManagerRuntime
	newMCPManagerRuntime = func() mcpManagerRuntime {
		if len(managerQueue) == 0 {
			t.Fatal("no fake MCP managers left in queue")
		}
		manager := managerQueue[0]
		managerQueue = managerQueue[1:]
		return manager
	}
	t.Cleanup(func() {
		newMCPManagerRuntime = oldFactory
	})

	if _, err := al.ProcessDirectWithChannel(
		context.Background(),
		"hello",
		"session-1",
		"cli",
		"direct",
	); err != nil {
		t.Fatalf("initial direct processing failed: %v", err)
	}

	initialDefault := al.GetRegistry().GetDefaultAgent()
	if initialDefault == nil {
		t.Fatal("expected default agent to exist")
	}
	if _, ok := initialDefault.Tools.Get("mcp_supabase_execute_sql"); !ok {
		t.Fatal("expected MCP tool to be registered before reload")
	}

	reloadCfg := newTestReloadConfigWithWriter(tmpDir)
	if err := al.ReloadProviderAndConfig(context.Background(), provider, reloadCfg); err != nil {
		t.Fatalf("reload failed: %v", err)
	}

	updatedDefault := al.GetRegistry().GetDefaultAgent()
	if updatedDefault == nil {
		t.Fatal("expected default agent after reload")
	}
	if _, ok := updatedDefault.Tools.Get("mcp_supabase_execute_sql"); !ok {
		t.Fatal("expected MCP tool to remain registered after reload")
	}

	if firstManager.closeCalls != 1 {
		t.Fatalf("expected first MCP manager to be closed once, got %d", firstManager.closeCalls)
	}
	if secondManager.closeCalls != 0 {
		t.Fatalf("expected second MCP manager to remain active during test, got close count %d", secondManager.closeCalls)
	}
}

func TestReloadProviderAndConfig_RepeatedReloadsCloseOnlySupersededManagers(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := newTestConfigWithEnabledMCP(tmpDir)
	msgBus := bus.NewMessageBus()
	provider := &mockProvider{}
	al := NewAgentLoop(cfg, msgBus, provider)
	defer al.Close()

	firstManager := newFakeLoopMCPManagerWithSupabaseTool("execute_sql")
	secondManager := newFakeLoopMCPManagerWithSupabaseTool("execute_sql")
	thirdManager := newFakeLoopMCPManagerWithSupabaseTool("execute_sql")
	managerQueue := []mcpManagerRuntime{firstManager, secondManager, thirdManager}
	oldFactory := newMCPManagerRuntime
	newMCPManagerRuntime = func() mcpManagerRuntime {
		if len(managerQueue) == 0 {
			t.Fatal("no fake MCP managers left in queue")
		}
		manager := managerQueue[0]
		managerQueue = managerQueue[1:]
		return manager
	}
	t.Cleanup(func() {
		newMCPManagerRuntime = oldFactory
	})

	if _, err := al.ProcessDirectWithChannel(
		context.Background(),
		"hello",
		"session-1",
		"cli",
		"direct",
	); err != nil {
		t.Fatalf("initial direct processing failed: %v", err)
	}

	reloadCfg := newTestReloadConfigWithWriter(tmpDir)
	if err := al.ReloadProviderAndConfig(context.Background(), provider, reloadCfg); err != nil {
		t.Fatalf("first reload failed: %v", err)
	}
	if err := al.ReloadProviderAndConfig(context.Background(), provider, reloadCfg); err != nil {
		t.Fatalf("second reload failed: %v", err)
	}

	defaultAgent := al.GetRegistry().GetDefaultAgent()
	if defaultAgent == nil {
		t.Fatal("expected default agent after repeated reloads")
	}
	if _, ok := defaultAgent.Tools.Get("mcp_supabase_execute_sql"); !ok {
		t.Fatal("expected MCP tool to remain registered after repeated reloads")
	}

	if firstManager.closeCalls != 1 {
		t.Fatalf("expected first MCP manager to be closed once, got %d", firstManager.closeCalls)
	}
	if secondManager.closeCalls != 1 {
		t.Fatalf("expected second MCP manager to be closed once, got %d", secondManager.closeCalls)
	}
	if thirdManager.closeCalls != 0 {
		t.Fatalf("expected third MCP manager to remain active, got close count %d", thirdManager.closeCalls)
	}
}

func TestReloadProviderAndConfig_FailsWhenMCPInitializationFails(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := newTestConfigWithEnabledMCP(tmpDir)
	msgBus := bus.NewMessageBus()
	provider := &mockProvider{}
	al := NewAgentLoop(cfg, msgBus, provider)
	defer al.Close()

	firstManager := newFakeLoopMCPManagerWithSupabaseTool("execute_sql")
	failingReloadManager := &fakeLoopMCPManager{
		loadErr: fmt.Errorf("boom"),
	}
	managerQueue := []mcpManagerRuntime{firstManager, failingReloadManager}
	oldFactory := newMCPManagerRuntime
	newMCPManagerRuntime = func() mcpManagerRuntime {
		if len(managerQueue) == 0 {
			t.Fatal("no fake MCP managers left in queue")
		}
		manager := managerQueue[0]
		managerQueue = managerQueue[1:]
		return manager
	}
	t.Cleanup(func() {
		newMCPManagerRuntime = oldFactory
	})

	if _, err := al.ProcessDirectWithChannel(
		context.Background(),
		"hello",
		"session-1",
		"cli",
		"direct",
	); err != nil {
		t.Fatalf("initial direct processing failed: %v", err)
	}

	oldRegistry := al.GetRegistry()
	reloadCfg := newTestReloadConfigWithWriter(tmpDir)
	err := al.ReloadProviderAndConfig(context.Background(), provider, reloadCfg)
	if err == nil {
		t.Fatal("expected reload to fail when MCP initialization fails")
	}
	if !strings.Contains(err.Error(), "MCP initialization failed during reload") {
		t.Fatalf("expected MCP initialization error, got: %v", err)
	}

	if currentRegistry := al.GetRegistry(); currentRegistry != oldRegistry {
		t.Fatal("expected registry to stay unchanged after failed reload")
	}

	defaultAgent := al.GetRegistry().GetDefaultAgent()
	if defaultAgent == nil {
		t.Fatal("expected default agent to remain available")
	}
	if _, ok := defaultAgent.Tools.Get("mcp_supabase_execute_sql"); !ok {
		t.Fatal("expected previously registered MCP tool to remain available after failed reload")
	}

	if firstManager.closeCalls != 0 {
		t.Fatalf("expected active MCP manager to remain open after failed reload, got close count %d", firstManager.closeCalls)
	}
	if failingReloadManager.closeCalls != 1 {
		t.Fatalf("expected failing reload MCP manager to be closed once, got %d", failingReloadManager.closeCalls)
	}
}

func TestEnsureMCPInitialized_RetriesAfterFailure(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := newTestConfigWithEnabledMCP(tmpDir)
	msgBus := bus.NewMessageBus()
	provider := &mockProvider{}
	al := NewAgentLoop(cfg, msgBus, provider)
	defer al.Close()

	failingManager := &fakeLoopMCPManager{
		loadErr: fmt.Errorf("first load failure"),
	}
	successfulManager := newFakeLoopMCPManagerWithSupabaseTool("execute_sql")
	managerQueue := []mcpManagerRuntime{failingManager, successfulManager}
	oldFactory := newMCPManagerRuntime
	newMCPManagerRuntime = func() mcpManagerRuntime {
		if len(managerQueue) == 0 {
			t.Fatal("no fake MCP managers left in queue")
		}
		manager := managerQueue[0]
		managerQueue = managerQueue[1:]
		return manager
	}
	t.Cleanup(func() {
		newMCPManagerRuntime = oldFactory
	})

	if _, err := al.ProcessDirectWithChannel(
		context.Background(),
		"hello",
		"session-1",
		"cli",
		"direct",
	); err == nil {
		t.Fatal("expected first MCP initialization attempt to fail")
	}

	if _, err := al.ProcessDirectWithChannel(
		context.Background(),
		"hello again",
		"session-2",
		"cli",
		"direct",
	); err != nil {
		t.Fatalf("expected second MCP initialization attempt to succeed, got error: %v", err)
	}

	defaultAgent := al.GetRegistry().GetDefaultAgent()
	if defaultAgent == nil {
		t.Fatal("expected default agent")
	}
	if _, ok := defaultAgent.Tools.Get("mcp_supabase_execute_sql"); !ok {
		t.Fatal("expected MCP tool to be registered after retry")
	}

	if failingManager.closeCalls != 1 {
		t.Fatalf("expected failing MCP manager to be closed once, got %d", failingManager.closeCalls)
	}
}

func TestTargetReasoningChannelID_AllChannels(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "agent-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
	}

	al := NewAgentLoop(cfg, bus.NewMessageBus(), &mockProvider{})
	chManager, err := channels.NewManager(&config.Config{}, bus.NewMessageBus(), nil)
	if err != nil {
		t.Fatalf("Failed to create channel manager: %v", err)
	}
	for name, id := range map[string]string{
		"whatsapp": "rid-whatsapp",
		"telegram": "rid-telegram",
		"discord":  "rid-discord",
		"slack":    "rid-slack",
		"line":     "rid-line",
	} {
		chManager.RegisterChannel(name, &fakeChannel{id: id})
	}
	al.SetChannelManager(chManager)
	tests := []struct {
		channel string
		wantID  string
	}{
		{channel: "whatsapp", wantID: "rid-whatsapp"},
		{channel: "telegram", wantID: "rid-telegram"},
		{channel: "discord", wantID: "rid-discord"},
		{channel: "slack", wantID: "rid-slack"},
		{channel: "line", wantID: "rid-line"},
		{channel: "unknown", wantID: ""},
	}

	for _, tt := range tests {
		t.Run(tt.channel, func(t *testing.T) {
			got := al.targetReasoningChannelID(tt.channel)
			if got != tt.wantID {
				t.Fatalf("targetReasoningChannelID(%q) = %q, want %q", tt.channel, got, tt.wantID)
			}
		})
	}
}

func TestHandleReasoning(t *testing.T) {
	newLoop := func(t *testing.T) (*AgentLoop, *bus.MessageBus) {
		t.Helper()
		tmpDir, err := os.MkdirTemp("", "agent-test-*")
		if err != nil {
			t.Fatalf("Failed to create temp dir: %v", err)
		}
		t.Cleanup(func() { _ = os.RemoveAll(tmpDir) })
		cfg := &config.Config{
			Agents: config.AgentsConfig{
				Defaults: config.AgentDefaults{
					Workspace:         tmpDir,
					Model:             "test-model",
					MaxTokens:         4096,
					MaxToolIterations: 10,
				},
			},
		}
		msgBus := bus.NewMessageBus()
		return NewAgentLoop(cfg, msgBus, &mockProvider{}), msgBus
	}

	t.Run("skips when any required field is empty", func(t *testing.T) {
		al, msgBus := newLoop(t)
		al.handleReasoning(context.Background(), "reasoning", "telegram", "")

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		for {
			select {
			case msg, ok := <-msgBus.OutboundChan():
				if !ok {
					t.Fatalf("expected no outbound message, got %+v", msg)
				}
				if msg.Content == "reasoning" {
					t.Fatalf("expected no message for empty chatID, got %+v", msg)
				}
				return
			case <-ctx.Done():
				t.Log("expected an outbound message, got none within timeout")
				return
			default:
				// Continue to check for message
				time.Sleep(5 * time.Millisecond) // Avoid busy loop
			}
		}
	})

	t.Run("publishes one message for non telegram", func(t *testing.T) {
		al, msgBus := newLoop(t)
		al.handleReasoning(context.Background(), "hello reasoning", "slack", "channel-1")

		msg, ok := <-msgBus.OutboundChan()
		if !ok {
			t.Fatal("expected an outbound message")
		}
		if msg.Channel != "slack" || msg.ChatID != "channel-1" || msg.Content != "hello reasoning" {
			t.Fatalf("unexpected outbound message: %+v", msg)
		}
	})

	t.Run("publishes one message for telegram", func(t *testing.T) {
		al, msgBus := newLoop(t)
		reasoning := "hello telegram reasoning"
		al.handleReasoning(context.Background(), reasoning, "telegram", "tg-chat")

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		for {
			select {
			case <-ctx.Done():
				t.Fatal("expected an outbound message, got none within timeout")
				return
			case msg, ok := <-msgBus.OutboundChan():
				if !ok {
					t.Fatal("expected outbound message")
				}

				if msg.Channel != "telegram" {
					t.Fatalf("expected telegram channel message, got %+v", msg)
				}
				if msg.ChatID != "tg-chat" {
					t.Fatalf("expected chatID tg-chat, got %+v", msg)
				}
				if msg.Content != reasoning {
					t.Fatalf("content mismatch: got %q want %q", msg.Content, reasoning)
				}
				return
			}
		}
	})
	t.Run("expired ctx", func(t *testing.T) {
		al, msgBus := newLoop(t)
		reasoning := "hello telegram reasoning"

		al.handleReasoning(context.Background(), reasoning, "telegram", "tg-chat")

		consumeCtx, consumeCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer consumeCancel()

		for {
			select {
			case msg, ok := <-msgBus.OutboundChan():
				if !ok {
					t.Fatalf("expected no outbound message, but received: %+v", msg)
				}
				t.Logf("Received unexpected outbound message: %+v", msg)
				return
			case <-consumeCtx.Done():
				t.Fatalf("failed: no message received within timeout")
				return
			}
		}
	})

	t.Run("returns promptly when bus is full", func(t *testing.T) {
		al, msgBus := newLoop(t)

		// Fill the outbound bus buffer until a publish would block.
		// Use a short timeout to detect when the buffer is full,
		// rather than hardcoding the buffer size.
		for i := 0; ; i++ {
			fillCtx, fillCancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			err := msgBus.PublishOutbound(fillCtx, bus.OutboundMessage{
				Channel: "filler",
				ChatID:  "filler",
				Content: fmt.Sprintf("filler-%d", i),
			})
			fillCancel()
			if err != nil {
				// Buffer is full (timed out trying to send).
				break
			}
		}

		// Use a short-deadline parent context to bound the test.
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()

		start := time.Now()
		al.handleReasoning(ctx, "should timeout", "slack", "channel-full")
		elapsed := time.Since(start)

		// handleReasoning uses a 5s internal timeout, but the parent ctx
		// expires in 500ms. It should return within ~500ms, not 5s.
		if elapsed > 2*time.Second {
			t.Fatalf("handleReasoning blocked too long (%v); expected prompt return", elapsed)
		}

		// Drain the bus and verify the reasoning message was NOT published
		// (it should have been dropped due to timeout).
		timeer := time.After(1 * time.Second)
		for {
			select {
			case <-timeer:
				t.Logf(
					"no reasoning message received after draining bus for 1s, as expected,length=%d",
					len(msgBus.OutboundChan()),
				)
				return
			case msg, ok := <-msgBus.OutboundChan():
				if !ok {
					break
				}
				if msg.Content == "should timeout" {
					t.Fatal("expected reasoning message to be dropped when bus is full, but it was published")
				}
			}
		}
	})
}

func TestResolveMediaRefs_ResolvesToBase64(t *testing.T) {
	store := media.NewFileMediaStore()
	dir := t.TempDir()

	// Create a minimal valid PNG (8-byte header is enough for filetype detection)
	pngPath := filepath.Join(dir, "test.png")
	// PNG magic: 0x89 P N G \r \n 0x1A \n + minimal IHDR
	pngHeader := []byte{
		0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, // PNG signature
		0x00, 0x00, 0x00, 0x0D, // IHDR length
		0x49, 0x48, 0x44, 0x52, // "IHDR"
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x08, 0x02, // 1x1 RGB
		0x00, 0x00, 0x00, // no interlace
		0x90, 0x77, 0x53, 0xDE, // CRC
	}
	if err := os.WriteFile(pngPath, pngHeader, 0o644); err != nil {
		t.Fatal(err)
	}
	ref, err := store.Store(pngPath, media.MediaMeta{}, "test")
	if err != nil {
		t.Fatal(err)
	}

	messages := []providers.Message{
		{Role: "user", Content: "describe this", Media: []string{ref}},
	}
	result := resolveMediaRefs(messages, store, config.DefaultMaxMediaSize)

	if len(result[0].Media) != 1 {
		t.Fatalf("expected 1 resolved media, got %d", len(result[0].Media))
	}
	if !strings.HasPrefix(result[0].Media[0], "data:image/png;base64,") {
		t.Fatalf("expected data:image/png;base64, prefix, got %q", result[0].Media[0][:40])
	}
}

func TestResolveMediaRefs_SkipsOversizedFile(t *testing.T) {
	store := media.NewFileMediaStore()
	dir := t.TempDir()

	bigPath := filepath.Join(dir, "big.png")
	// Write PNG header + padding to exceed limit
	data := make([]byte, 1024+1) // 1KB + 1 byte
	copy(data, []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A})
	if err := os.WriteFile(bigPath, data, 0o644); err != nil {
		t.Fatal(err)
	}
	ref, _ := store.Store(bigPath, media.MediaMeta{}, "test")

	messages := []providers.Message{
		{Role: "user", Content: "hi", Media: []string{ref}},
	}
	// Use a tiny limit (1KB) so the file is oversized
	result := resolveMediaRefs(messages, store, 1024)

	if len(result[0].Media) != 0 {
		t.Fatalf("expected 0 media (oversized), got %d", len(result[0].Media))
	}
}

func TestResolveMediaRefs_UnknownTypeInjectsPath(t *testing.T) {
	store := media.NewFileMediaStore()
	dir := t.TempDir()

	txtPath := filepath.Join(dir, "readme.txt")
	if err := os.WriteFile(txtPath, []byte("hello world"), 0o644); err != nil {
		t.Fatal(err)
	}
	ref, _ := store.Store(txtPath, media.MediaMeta{}, "test")

	messages := []providers.Message{
		{Role: "user", Content: "hi", Media: []string{ref}},
	}
	result := resolveMediaRefs(messages, store, config.DefaultMaxMediaSize)

	if len(result[0].Media) != 0 {
		t.Fatalf("expected 0 media entries, got %d", len(result[0].Media))
	}
	expected := "hi [file:" + txtPath + "]"
	if result[0].Content != expected {
		t.Fatalf("expected content %q, got %q", expected, result[0].Content)
	}
}

func TestResolveMediaRefs_PassesThroughNonMediaRefs(t *testing.T) {
	messages := []providers.Message{
		{Role: "user", Content: "hi", Media: []string{"https://example.com/img.png"}},
	}
	result := resolveMediaRefs(messages, nil, config.DefaultMaxMediaSize)

	if len(result[0].Media) != 1 || result[0].Media[0] != "https://example.com/img.png" {
		t.Fatalf("expected passthrough of non-media:// URL, got %v", result[0].Media)
	}
}

func TestResolveMediaRefs_DoesNotMutateOriginal(t *testing.T) {
	store := media.NewFileMediaStore()
	dir := t.TempDir()
	pngPath := filepath.Join(dir, "test.png")
	pngHeader := []byte{
		0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A,
		0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52,
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x08, 0x02,
		0x00, 0x00, 0x00, 0x90, 0x77, 0x53, 0xDE,
	}
	os.WriteFile(pngPath, pngHeader, 0o644)
	ref, _ := store.Store(pngPath, media.MediaMeta{}, "test")

	original := []providers.Message{
		{Role: "user", Content: "hi", Media: []string{ref}},
	}
	originalRef := original[0].Media[0]

	resolveMediaRefs(original, store, config.DefaultMaxMediaSize)

	if original[0].Media[0] != originalRef {
		t.Fatal("resolveMediaRefs mutated original message slice")
	}
}

func TestResolveMediaRefs_UsesMetaContentType(t *testing.T) {
	store := media.NewFileMediaStore()
	dir := t.TempDir()

	// File with JPEG content but stored with explicit content type
	jpegPath := filepath.Join(dir, "photo")
	jpegHeader := []byte{0xFF, 0xD8, 0xFF, 0xE0} // JPEG magic bytes
	os.WriteFile(jpegPath, jpegHeader, 0o644)
	ref, _ := store.Store(jpegPath, media.MediaMeta{ContentType: "image/jpeg"}, "test")

	messages := []providers.Message{
		{Role: "user", Content: "hi", Media: []string{ref}},
	}
	result := resolveMediaRefs(messages, store, config.DefaultMaxMediaSize)

	if len(result[0].Media) != 1 {
		t.Fatalf("expected 1 media, got %d", len(result[0].Media))
	}
	if !strings.HasPrefix(result[0].Media[0], "data:image/jpeg;base64,") {
		t.Fatalf("expected jpeg prefix, got %q", result[0].Media[0][:30])
	}
}

func TestResolveMediaRefs_PDFInjectsFilePath(t *testing.T) {
	store := media.NewFileMediaStore()
	dir := t.TempDir()

	pdfPath := filepath.Join(dir, "report.pdf")
	// PDF magic bytes
	os.WriteFile(pdfPath, []byte("%PDF-1.4 test content"), 0o644)
	ref, _ := store.Store(pdfPath, media.MediaMeta{ContentType: "application/pdf"}, "test")

	messages := []providers.Message{
		{Role: "user", Content: "report.pdf [file]", Media: []string{ref}},
	}
	result := resolveMediaRefs(messages, store, config.DefaultMaxMediaSize)

	if len(result[0].Media) != 0 {
		t.Fatalf("expected 0 media (non-image), got %d", len(result[0].Media))
	}
	expected := "report.pdf [file:" + pdfPath + "]"
	if result[0].Content != expected {
		t.Fatalf("expected content %q, got %q", expected, result[0].Content)
	}
}

func TestResolveMediaRefs_AudioInjectsAudioPath(t *testing.T) {
	store := media.NewFileMediaStore()
	dir := t.TempDir()

	oggPath := filepath.Join(dir, "voice.ogg")
	os.WriteFile(oggPath, []byte("fake audio"), 0o644)
	ref, _ := store.Store(oggPath, media.MediaMeta{ContentType: "audio/ogg"}, "test")

	messages := []providers.Message{
		{Role: "user", Content: "voice.ogg [audio]", Media: []string{ref}},
	}
	result := resolveMediaRefs(messages, store, config.DefaultMaxMediaSize)

	if len(result[0].Media) != 0 {
		t.Fatalf("expected 0 media, got %d", len(result[0].Media))
	}
	expected := "voice.ogg [audio:" + oggPath + "]"
	if result[0].Content != expected {
		t.Fatalf("expected content %q, got %q", expected, result[0].Content)
	}
}

func TestResolveMediaRefs_VideoInjectsVideoPath(t *testing.T) {
	store := media.NewFileMediaStore()
	dir := t.TempDir()

	mp4Path := filepath.Join(dir, "clip.mp4")
	os.WriteFile(mp4Path, []byte("fake video"), 0o644)
	ref, _ := store.Store(mp4Path, media.MediaMeta{ContentType: "video/mp4"}, "test")

	messages := []providers.Message{
		{Role: "user", Content: "clip.mp4 [video]", Media: []string{ref}},
	}
	result := resolveMediaRefs(messages, store, config.DefaultMaxMediaSize)

	if len(result[0].Media) != 0 {
		t.Fatalf("expected 0 media, got %d", len(result[0].Media))
	}
	expected := "clip.mp4 [video:" + mp4Path + "]"
	if result[0].Content != expected {
		t.Fatalf("expected content %q, got %q", expected, result[0].Content)
	}
}

func TestResolveMediaRefs_NoGenericTagAppendsPath(t *testing.T) {
	store := media.NewFileMediaStore()
	dir := t.TempDir()

	csvPath := filepath.Join(dir, "data.csv")
	os.WriteFile(csvPath, []byte("a,b,c"), 0o644)
	ref, _ := store.Store(csvPath, media.MediaMeta{ContentType: "text/csv"}, "test")

	messages := []providers.Message{
		{Role: "user", Content: "here is my data", Media: []string{ref}},
	}
	result := resolveMediaRefs(messages, store, config.DefaultMaxMediaSize)

	expected := "here is my data [file:" + csvPath + "]"
	if result[0].Content != expected {
		t.Fatalf("expected content %q, got %q", expected, result[0].Content)
	}
}

func TestResolveMediaRefs_EmptyContentGetsPathTag(t *testing.T) {
	store := media.NewFileMediaStore()
	dir := t.TempDir()

	docPath := filepath.Join(dir, "doc.docx")
	os.WriteFile(docPath, []byte("fake docx"), 0o644)
	docxMIME := "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	ref, _ := store.Store(docPath, media.MediaMeta{ContentType: docxMIME}, "test")

	messages := []providers.Message{
		{Role: "user", Content: "", Media: []string{ref}},
	}
	result := resolveMediaRefs(messages, store, config.DefaultMaxMediaSize)

	expected := "[file:" + docPath + "]"
	if result[0].Content != expected {
		t.Fatalf("expected content %q, got %q", expected, result[0].Content)
	}
}

func TestResolveMediaRefs_MixedImageAndFile(t *testing.T) {
	store := media.NewFileMediaStore()
	dir := t.TempDir()

	pngPath := filepath.Join(dir, "photo.png")
	pngHeader := []byte{
		0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A,
		0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52,
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x08, 0x02,
		0x00, 0x00, 0x00, 0x90, 0x77, 0x53, 0xDE,
	}
	os.WriteFile(pngPath, pngHeader, 0o644)
	imgRef, _ := store.Store(pngPath, media.MediaMeta{}, "test")

	pdfPath := filepath.Join(dir, "report.pdf")
	os.WriteFile(pdfPath, []byte("%PDF-1.4 test"), 0o644)
	fileRef, _ := store.Store(pdfPath, media.MediaMeta{ContentType: "application/pdf"}, "test")

	messages := []providers.Message{
		{Role: "user", Content: "check these [file]", Media: []string{imgRef, fileRef}},
	}
	result := resolveMediaRefs(messages, store, config.DefaultMaxMediaSize)

	if len(result[0].Media) != 1 {
		t.Fatalf("expected 1 media (image only), got %d", len(result[0].Media))
	}
	if !strings.HasPrefix(result[0].Media[0], "data:image/png;base64,") {
		t.Fatal("expected image to be base64 encoded")
	}
	expectedContent := "check these [file:" + pdfPath + "]"
	if result[0].Content != expectedContent {
		t.Fatalf("expected content %q, got %q", expectedContent, result[0].Content)
	}
}
