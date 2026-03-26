package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/itsivag/suprclaw/pkg/agent"
	"github.com/itsivag/suprclaw/pkg/bus"
	"github.com/itsivag/suprclaw/pkg/config"
	"github.com/itsivag/suprclaw/pkg/providers"
)

type gatewayMockProvider struct {
	response string
}

func (m *gatewayMockProvider) Chat(
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

func (m *gatewayMockProvider) GetDefaultModel() string {
	return "mock-model"
}

func newTestAdminHandler(t *testing.T) (*adminHandler, *agent.AgentLoop, *http.ServeMux) {
	t.Helper()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         t.TempDir(),
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
	}

	loop := agent.NewAgentLoop(cfg, bus.NewMessageBus(), &gatewayMockProvider{response: "summary"})
	h := &adminHandler{
		configPath: t.TempDir() + "/config.json",
		secret:     "test-secret",
		agentLoop:  loop,
	}
	mux := http.NewServeMux()
	h.registerRoutes(mux)
	return h, loop, mux
}

func TestAdminSessionNewEndpoint_ClearsSession(t *testing.T) {
	_, loop, mux := newTestAdminHandler(t)
	defaultAgent := loop.GetRegistry().GetDefaultAgent()
	if defaultAgent == nil {
		t.Fatal("default agent missing")
	}
	sessionKey := "test-session-new"
	defaultAgent.Sessions.SetHistory(sessionKey, []providers.Message{
		{Role: "user", Content: "u1"},
		{Role: "assistant", Content: "a1"},
	})
	defaultAgent.Sessions.SetSummary(sessionKey, "old summary")

	body, _ := json.Marshal(map[string]string{"sessionKey": sessionKey})
	req := httptest.NewRequest(http.MethodPost, "/api/admin/agents/main/sessions/new", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-secret")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if got := len(defaultAgent.Sessions.GetHistory(sessionKey)); got != 0 {
		t.Fatalf("history len = %d, want 0", got)
	}
	if got := defaultAgent.Sessions.GetSummary(sessionKey); got != "" {
		t.Fatalf("summary = %q, want empty", got)
	}
}

func TestAdminSessionCompactEndpoint_CompactsSession(t *testing.T) {
	_, loop, mux := newTestAdminHandler(t)
	defaultAgent := loop.GetRegistry().GetDefaultAgent()
	if defaultAgent == nil {
		t.Fatal("default agent missing")
	}
	sessionKey := "test-session-compact"
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

	body, _ := json.Marshal(map[string]string{"sessionKey": sessionKey})
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/admin/agents/main/sessions/compact",
		bytes.NewReader(body),
	)
	req.Header.Set("Authorization", "Bearer test-secret")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if got := len(defaultAgent.Sessions.GetHistory(sessionKey)); got > 4 {
		t.Fatalf("history len = %d, want <= 4", got)
	}
	if got := defaultAgent.Sessions.GetSummary(sessionKey); got == "" {
		t.Fatal("summary should not be empty after compact")
	}
}
