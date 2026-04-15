package gateway

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/itsivag/suprclaw/pkg/agent"
	"github.com/itsivag/suprclaw/pkg/bus"
	"github.com/itsivag/suprclaw/pkg/config"
)

func TestAdminUpsertAgent_SyncsRuntimeRegistry(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.json")

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{
				{ID: "main", Default: true, Scope: config.AgentScopeWorkforce},
			},
		},
	}
	if err := config.SaveConfig(cfgPath, cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	loop := agent.NewAgentLoop(cfg, bus.NewMessageBus(), &gatewayMockProvider{response: "ok"})
	h := &adminHandler{configPath: cfgPath, secret: "test-secret", agentLoop: loop}
	mux := http.NewServeMux()
	h.registerRoutes(mux)

	body := []byte(`{"agentId":"writer","workspacePath":"` + tmpDir + `/workspace-writer","model":"test-model","scope":"workforce"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/admin/agents", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-secret")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}

	if _, ok := loop.GetRegistry().GetAgent("writer"); !ok {
		t.Fatal("writer should be present in runtime registry after upsert")
	}

	updated, err := config.LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if !updated.Heartbeat.Enabled {
		t.Fatal("heartbeat.enabled should be true after creating a new agent")
	}
	if updated.Heartbeat.MinimumGapMinutes != 5 {
		t.Fatalf("heartbeat.minimum_gap_minutes = %d, want 5", updated.Heartbeat.MinimumGapMinutes)
	}
	job, found := findHeartbeatJobByAgentID(updated.Heartbeat.Jobs, "writer")
	if !found {
		t.Fatal("writer heartbeat job should be created on new agent add")
	}
	if job.IntervalMinutes != 30 || job.IdleWindowMinutes != 15 {
		t.Fatalf("unexpected default heartbeat job: %+v", job)
	}
}

func TestAdminUpsertAgent_RejectsMissingScope(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.json")

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{
				{ID: "main", Default: true, Scope: config.AgentScopeWorkforce},
			},
		},
	}
	if err := config.SaveConfig(cfgPath, cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	loop := agent.NewAgentLoop(cfg, bus.NewMessageBus(), &gatewayMockProvider{response: "ok"})
	h := &adminHandler{configPath: cfgPath, secret: "test-secret", agentLoop: loop}
	mux := http.NewServeMux()
	h.registerRoutes(mux)

	body := []byte(`{"agentId":"writer","workspacePath":"` + tmpDir + `/workspace-writer","model":"test-model"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/admin/agents", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-secret")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "scope is required") {
		t.Fatalf("expected missing scope error, got body=%s", rec.Body.String())
	}
}

func TestAdminWakeAgent_PassesPathAgentIDToCLI(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{
				{ID: "main", Default: true, Scope: config.AgentScopeWorkforce},
				{ID: "writer", Scope: config.AgentScopeWorkforce},
			},
		},
	}

	loop := agent.NewAgentLoop(cfg, bus.NewMessageBus(), &gatewayMockProvider{response: "ok"})
	var capturedName string
	var capturedArgs []string
	h := &adminHandler{
		secret:    "test-secret",
		agentLoop: loop,
		commandRunner: func(name string, args ...string) ([]byte, error) {
			capturedName = name
			capturedArgs = append([]string{}, args...)
			return []byte("wake-output"), nil
		},
	}
	mux := http.NewServeMux()
	h.registerRoutes(mux)

	body := []byte(`{"sessionKey":"agent:writer:writer","message":"hello"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/admin/agents/writer/wake", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-secret")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if capturedName == "" {
		t.Fatal("expected command runner to be invoked")
	}
	if !slices.Contains(capturedArgs, "--agent-id") {
		t.Fatalf("expected --agent-id flag in args, got %v", capturedArgs)
	}
	for i := 0; i < len(capturedArgs)-1; i++ {
		if capturedArgs[i] == "--agent-id" {
			if capturedArgs[i+1] != "writer" {
				t.Fatalf("--agent-id value = %q, want %q", capturedArgs[i+1], "writer")
			}
			return
		}
	}
	t.Fatalf("did not find --agent-id value in args: %v", capturedArgs)
}

func TestAdminMcpConfigure_RejectsLegacyFlatPayload(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.json")

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{
				{ID: "main", Default: true, Scope: config.AgentScopeWorkforce},
			},
		},
	}
	if err := config.SaveConfig(cfgPath, cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	loop := agent.NewAgentLoop(cfg, bus.NewMessageBus(), &gatewayMockProvider{response: "ok"})
	h := &adminHandler{configPath: cfgPath, secret: "test-secret", agentLoop: loop}
	mux := http.NewServeMux()
	h.registerRoutes(mux)

	body := []byte(`{"memory":{"enabled":true,"auth_mode":"static_headers","command":"fake"}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/admin/mcp/configure", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-secret")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "unknown field") {
		t.Fatalf("expected unknown field error, got body=%s", rec.Body.String())
	}
}

func TestAdminWakeAgent_UnknownAgentReturnsNotFound(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{
				{ID: "main", Default: true, Scope: config.AgentScopeWorkforce},
			},
		},
	}

	loop := agent.NewAgentLoop(cfg, bus.NewMessageBus(), &gatewayMockProvider{response: "ok"})
	called := false
	h := &adminHandler{
		secret:    "test-secret",
		agentLoop: loop,
		commandRunner: func(name string, args ...string) ([]byte, error) {
			called = true
			return []byte("unexpected"), nil
		},
	}
	mux := http.NewServeMux()
	h.registerRoutes(mux)

	body := []byte(`{"sessionKey":"agent:missing:missing","message":"hello"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/admin/agents/missing/wake", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-secret")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body=%s", rec.Code, rec.Body.String())
	}
	if called {
		t.Fatal("command runner should not be invoked for unknown agent")
	}
}

func TestAdminWakeAgentDetached_ReturnsAccepted(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{
				{ID: "main", Default: true, Scope: config.AgentScopeWorkforce},
				{ID: "writer", Scope: config.AgentScopeWorkforce},
			},
		},
	}

	loop := agent.NewAgentLoop(cfg, bus.NewMessageBus(), &gatewayMockProvider{response: "ok"})
	h := &adminHandler{
		secret:    "test-secret",
		agentLoop: loop,
		commandRunner: func(name string, args ...string) ([]byte, error) {
			t.Fatalf("command runner must not be called for detached wake")
			return nil, nil
		},
	}
	mux := http.NewServeMux()
	h.registerRoutes(mux)

	body := []byte(`{"sessionKey":"hook:task:task-1","message":"hello detached"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/admin/agents/writer/wake-detached", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-secret")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202, body=%s", rec.Code, rec.Body.String())
	}
}

func TestAdminWakeAgentDetached_RejectsNonHookSessionKey(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{
				{ID: "main", Default: true, Scope: config.AgentScopeWorkforce},
				{ID: "writer", Scope: config.AgentScopeWorkforce},
			},
		},
	}
	loop := agent.NewAgentLoop(cfg, bus.NewMessageBus(), &gatewayMockProvider{response: "ok"})
	h := &adminHandler{secret: "test-secret", agentLoop: loop}
	mux := http.NewServeMux()
	h.registerRoutes(mux)

	body := []byte(`{"sessionKey":"agent:writer:main","message":"hello"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/admin/agents/writer/wake-detached", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-secret")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "hook: namespace") {
		t.Fatalf("unexpected body: %s", rec.Body.String())
	}
}

func TestAdminWakeAgentDetached_UnknownAgentReturnsNotFound(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{
				{ID: "main", Default: true, Scope: config.AgentScopeWorkforce},
			},
		},
	}
	loop := agent.NewAgentLoop(cfg, bus.NewMessageBus(), &gatewayMockProvider{response: "ok"})
	h := &adminHandler{secret: "test-secret", agentLoop: loop}
	mux := http.NewServeMux()
	h.registerRoutes(mux)

	body := []byte(`{"sessionKey":"hook:task:missing","message":"hello"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/admin/agents/missing/wake-detached", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-secret")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body=%s", rec.Code, rec.Body.String())
	}
}

func TestAdminDeleteAgent_SyncsRuntimeRegistry(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.json")

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{
				{ID: "main", Default: true, Scope: config.AgentScopeWorkforce},
				{ID: "writer", Scope: config.AgentScopeWorkforce},
			},
		},
		Heartbeat: config.HeartbeatConfig{
			Enabled:           true,
			MinimumGapMinutes: 5,
			Jobs: []config.HeartbeatJobConfig{
				{AgentID: "main", IntervalMinutes: 5},
				{AgentID: "writer", IntervalMinutes: 5},
			},
		},
	}
	if err := config.SaveConfig(cfgPath, cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	loop := agent.NewAgentLoop(cfg, bus.NewMessageBus(), &gatewayMockProvider{response: "ok"})
	h := &adminHandler{configPath: cfgPath, secret: "test-secret", agentLoop: loop}
	mux := http.NewServeMux()
	h.registerRoutes(mux)

	req := httptest.NewRequest(http.MethodDelete, "/api/admin/agents/writer", nil)
	req.Header.Set("Authorization", "Bearer test-secret")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}

	if _, ok := loop.GetRegistry().GetAgent("writer"); ok {
		t.Fatal("writer should be removed from runtime registry after delete")
	}
	if _, ok := loop.GetRegistry().GetAgent("main"); !ok {
		t.Fatal("main agent should remain after deleting writer")
	}

	updated, err := config.LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if !updated.Heartbeat.Enabled {
		t.Fatal("heartbeat.enabled should remain true when other jobs remain")
	}
	if _, found := findHeartbeatJobByAgentID(updated.Heartbeat.Jobs, "writer"); found {
		t.Fatal("writer heartbeat job should be removed on agent delete")
	}
	if _, found := findHeartbeatJobByAgentID(updated.Heartbeat.Jobs, "main"); !found {
		t.Fatal("main heartbeat job should remain after deleting writer")
	}
}

func TestAdminUpsertAgent_UpdateDoesNotDuplicateHeartbeatJob(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.json")
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{
				{ID: "main", Default: true, Scope: config.AgentScopeWorkforce},
				{ID: "writer", Scope: config.AgentScopeWorkforce},
			},
		},
		Heartbeat: config.HeartbeatConfig{
			Enabled:           true,
			MinimumGapMinutes: 5,
			Jobs: []config.HeartbeatJobConfig{
				{AgentID: "writer", IntervalMinutes: 5},
			},
		},
	}
	if err := config.SaveConfig(cfgPath, cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	loop := agent.NewAgentLoop(cfg, bus.NewMessageBus(), &gatewayMockProvider{response: "ok"})
	h := &adminHandler{configPath: cfgPath, secret: "test-secret", agentLoop: loop}
	mux := http.NewServeMux()
	h.registerRoutes(mux)

	body := []byte(`{"agentId":"writer","workspacePath":"` + tmpDir + `/workspace-writer-updated","model":"test-model","scope":"workforce"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/admin/agents", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-secret")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}

	updated, err := config.LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	count := 0
	for _, job := range updated.Heartbeat.Jobs {
		if job.AgentID == "writer" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("writer heartbeat job count = %d, want 1", count)
	}
}

func TestAdminDeleteAgent_DisablesHeartbeatWhenLastJobRemoved(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.json")
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{
				{ID: "main", Default: true, Scope: config.AgentScopeWorkforce},
				{ID: "writer", Scope: config.AgentScopeWorkforce},
			},
		},
		Heartbeat: config.HeartbeatConfig{
			Enabled:           true,
			MinimumGapMinutes: 5,
			Jobs: []config.HeartbeatJobConfig{
				{AgentID: "writer", IntervalMinutes: 5},
			},
		},
	}
	if err := config.SaveConfig(cfgPath, cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	loop := agent.NewAgentLoop(cfg, bus.NewMessageBus(), &gatewayMockProvider{response: "ok"})
	h := &adminHandler{configPath: cfgPath, secret: "test-secret", agentLoop: loop}
	mux := http.NewServeMux()
	h.registerRoutes(mux)

	req := httptest.NewRequest(http.MethodDelete, "/api/admin/agents/writer", nil)
	req.Header.Set("Authorization", "Bearer test-secret")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if _, ok := loop.GetRegistry().GetAgent("main"); !ok {
		t.Fatal("main agent should remain after deleting writer")
	}

	updated, err := config.LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if updated.Heartbeat.Enabled {
		t.Fatal("heartbeat.enabled should be false when no jobs remain")
	}
	if len(updated.Heartbeat.Jobs) != 0 {
		t.Fatalf("heartbeat.jobs len = %d, want 0", len(updated.Heartbeat.Jobs))
	}
}

func TestAdminDeleteAgent_RejectsDeletingMain(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.json")
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{
				{ID: "main", Default: true, Scope: config.AgentScopeWorkforce},
				{ID: "writer", Scope: config.AgentScopeWorkforce},
			},
		},
	}
	if err := config.SaveConfig(cfgPath, cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	loop := agent.NewAgentLoop(cfg, bus.NewMessageBus(), &gatewayMockProvider{response: "ok"})
	h := &adminHandler{configPath: cfgPath, secret: "test-secret", agentLoop: loop}
	mux := http.NewServeMux()
	h.registerRoutes(mux)

	req := httptest.NewRequest(http.MethodDelete, "/api/admin/agents/main", nil)
	req.Header.Set("Authorization", "Bearer test-secret")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `agent \"main\" cannot be deleted`) {
		t.Fatalf("response body = %s, want main-delete invariant error", rec.Body.String())
	}

	updated, err := config.LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if len(updated.Agents.List) != 2 {
		t.Fatalf("agents.list len = %d, want 2", len(updated.Agents.List))
	}
}

func TestAdminUpsertAgent_RuntimeSyncFailurePersistsHeartbeatAutoSync(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.json")
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{
				{ID: "main", Default: true, Scope: config.AgentScopeWorkforce},
			},
		},
	}
	if err := config.SaveConfig(cfgPath, cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	h := &adminHandler{configPath: cfgPath, secret: "test-secret"}
	mux := http.NewServeMux()
	h.registerRoutes(mux)

	body := []byte(`{"agentId":"writer","workspacePath":"` + tmpDir + `/workspace-writer","model":"test-model","scope":"workforce"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/admin/agents", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-secret")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503, body=%s", rec.Code, rec.Body.String())
	}

	updated, err := config.LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if !updated.Heartbeat.Enabled {
		t.Fatal("heartbeat.enabled should be true after persisted new-agent sync")
	}
	if updated.Heartbeat.MinimumGapMinutes != 5 {
		t.Fatalf("heartbeat.minimum_gap_minutes = %d, want 5", updated.Heartbeat.MinimumGapMinutes)
	}
	if _, found := findHeartbeatJobByAgentID(updated.Heartbeat.Jobs, "writer"); !found {
		t.Fatal("writer heartbeat job should persist even when runtime reload fails")
	}
}

func TestVerifyAgentRegistrySync_DetectsStaleAgents(t *testing.T) {
	tmpDir := t.TempDir()

	runtimeCfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{
				{ID: "main", Default: true, Scope: config.AgentScopeWorkforce},
				{ID: "writer", Scope: config.AgentScopeWorkforce},
			},
		},
	}
	loop := agent.NewAgentLoop(runtimeCfg, bus.NewMessageBus(), &gatewayMockProvider{response: "ok"})

	expectedCfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: runtimeCfg.Agents.Defaults,
			List: []config.AgentConfig{
				{ID: "main", Default: true, Scope: config.AgentScopeWorkforce},
			},
		},
	}

	if err := verifyAgentRegistrySync(loop, expectedCfg); err == nil {
		t.Fatal("expected stale-agent sync error, got nil")
	}
}

func TestAdminReloadRuntime_ReloadsRegistryFromConfig(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.json")

	initialCfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{
				{ID: "main", Default: true, Scope: config.AgentScopeWorkforce},
			},
		},
	}
	if err := config.SaveConfig(cfgPath, initialCfg); err != nil {
		t.Fatalf("SaveConfig(initial) error = %v", err)
	}

	loop := agent.NewAgentLoop(initialCfg, bus.NewMessageBus(), &gatewayMockProvider{response: "ok"})

	updatedCfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: initialCfg.Agents.Defaults,
			List: []config.AgentConfig{
				{ID: "main", Default: true, Scope: config.AgentScopeWorkforce},
				{ID: "writer", Scope: config.AgentScopeWorkforce},
			},
		},
	}
	if err := config.SaveConfig(cfgPath, updatedCfg); err != nil {
		t.Fatalf("SaveConfig(updated) error = %v", err)
	}

	h := &adminHandler{configPath: cfgPath, secret: "test-secret", agentLoop: loop}
	mux := http.NewServeMux()
	h.registerRoutes(mux)

	req := httptest.NewRequest(http.MethodPost, "/api/admin/runtime/reload", nil)
	req.Header.Set("Authorization", "Bearer test-secret")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}

	if _, ok := loop.GetRegistry().GetAgent("writer"); !ok {
		t.Fatal("writer should be present in runtime registry after reload")
	}
}

func TestAdminVersionEndpoint_ReturnsVersionMetadata(t *testing.T) {
	oldVersion, oldGit := config.Version, config.GitCommit
	oldBuildTime, oldGoVersion := config.BuildTime, config.GoVersion
	oldGHRunCount := config.GHRunCount
	t.Cleanup(func() {
		config.Version, config.GitCommit = oldVersion, oldGit
		config.BuildTime, config.GoVersion = oldBuildTime, oldGoVersion
		config.GHRunCount = oldGHRunCount
	})

	config.Version = "v1.2.3"
	config.GitCommit = "deadbeef"
	config.BuildTime = "2026-04-04T10:00:00Z"
	config.GoVersion = "go1.23.0"
	config.GHRunCount = ""

	h := &adminHandler{secret: "test-secret"}
	mux := http.NewServeMux()
	h.registerRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/api/admin/version", nil)
	req.Header.Set("Authorization", "Bearer test-secret")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	body := decodeJSONMap(t, rec.Body.String())
	if got, _ := body["version"].(string); got != "v1.2.3" {
		t.Fatalf("version = %v, want v1.2.3", body["version"])
	}
	if got, _ := body["git_commit"].(string); got != "deadbeef" {
		t.Fatalf("git_commit = %v, want deadbeef", body["git_commit"])
	}
	if got, _ := body["build_time"].(string); got != "2026-04-04T10:00:00Z" {
		t.Fatalf("build_time = %v, want 2026-04-04T10:00:00Z", body["build_time"])
	}
	if got, _ := body["go_version"].(string); got != "go1.23.0" {
		t.Fatalf("go_version = %v, want go1.23.0", body["go_version"])
	}
	if got, ok := body["gh_run_count"].(float64); !ok || int(got) != 0 {
		t.Fatalf("gh_run_count = %v, want 0", body["gh_run_count"])
	}
	if got, _ := body["source"].(string); got != "local" {
		t.Fatalf("source = %v, want local", body["source"])
	}
}

func TestAdminVersionEndpoint_UnauthorizedWithoutBearer(t *testing.T) {
	h := &adminHandler{secret: "test-secret"}
	mux := http.NewServeMux()
	h.registerRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/api/admin/version", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401, body=%s", rec.Code, rec.Body.String())
	}
}

func TestAdminVersionEndpoint_ForbiddenWhenAdminDisabled(t *testing.T) {
	h := &adminHandler{}
	mux := http.NewServeMux()
	h.registerRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/api/admin/version", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403, body=%s", rec.Code, rec.Body.String())
	}
}

func TestAdminMCPDeleteTool_RemovesExistingTool(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.json")
	cfg := &config.Config{
		Tools: config.ToolsConfig{
			MCP: config.MCPConfig{
				Servers: map[string]config.MCPServerConfig{
					"web_scrape": {
						Enabled: true,
						Type:    "http",
						URL:     "https://example.com/mcp/web-scrape",
					},
				},
			},
		},
	}
	if err := config.SaveConfig(cfgPath, cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	h := &adminHandler{configPath: cfgPath, secret: "test-secret"}
	mux := http.NewServeMux()
	h.registerRoutes(mux)

	req := httptest.NewRequest(http.MethodDelete, "/api/admin/mcp/tools/web_scrape", nil)
	req.Header.Set("Authorization", "Bearer test-secret")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	body := decodeJSONMap(t, rec.Body.String())
	if status, _ := body["status"].(string); status != "ok" {
		t.Fatalf("status field = %v, want ok", body["status"])
	}
	if removed, _ := body["toolRemoved"].(bool); !removed {
		t.Fatalf("toolRemoved = %v, want true", body["toolRemoved"])
	}

	updated, err := config.LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if _, ok := updated.Tools.MCP.Servers["web_scrape"]; ok {
		t.Fatal("web_scrape should be removed from config")
	}
}

func TestAdminMCPDeleteTool_IsIdempotentWhenToolMissing(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.json")
	cfg := &config.Config{
		Tools: config.ToolsConfig{
			MCP: config.MCPConfig{
				Servers: map[string]config.MCPServerConfig{
					"memory": {
						Enabled: true,
						Type:    "http",
						URL:     "https://example.com/mcp/memory",
					},
				},
			},
		},
	}
	if err := config.SaveConfig(cfgPath, cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	h := &adminHandler{configPath: cfgPath, secret: "test-secret"}
	mux := http.NewServeMux()
	h.registerRoutes(mux)

	req := httptest.NewRequest(http.MethodDelete, "/api/admin/mcp/tools/web_scrape", nil)
	req.Header.Set("Authorization", "Bearer test-secret")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	body := decodeJSONMap(t, rec.Body.String())
	if status, _ := body["status"].(string); status != "ok" {
		t.Fatalf("status field = %v, want ok", body["status"])
	}
	if removed, _ := body["toolRemoved"].(bool); removed {
		t.Fatalf("toolRemoved = %v, want false", body["toolRemoved"])
	}

	updated, err := config.LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if _, ok := updated.Tools.MCP.Servers["memory"]; !ok {
		t.Fatal("memory should remain in config")
	}
}

func TestAdminMCPDeleteTool_RemovesToolAndSkill(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.json")
	workspace := filepath.Join(tmpDir, "workspace")
	skillDir := filepath.Join(workspace, "skills", "demo-skill")

	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(skillDir) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# Demo"), 0o644); err != nil {
		t.Fatalf("WriteFile(SKILL.md) error = %v", err)
	}

	cfg := &config.Config{
		Tools: config.ToolsConfig{
			MCP: config.MCPConfig{
				Servers: map[string]config.MCPServerConfig{
					"mobile_browser": {
						Enabled: true,
						Type:    "http",
						URL:     "https://example.com/mcp/mobile-browser",
					},
				},
			},
		},
	}
	if err := config.SaveConfig(cfgPath, cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	h := &adminHandler{configPath: cfgPath, secret: "test-secret"}
	mux := http.NewServeMux()
	h.registerRoutes(mux)

	req := httptest.NewRequest(
		http.MethodDelete,
		"/api/admin/mcp/tools/mobile_browser?skillName=demo-skill&workspacePath="+url.QueryEscape(workspace),
		nil,
	)
	req.Header.Set("Authorization", "Bearer test-secret")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	body := decodeJSONMap(t, rec.Body.String())
	if status, _ := body["status"].(string); status != "ok" {
		t.Fatalf("status field = %v, want ok", body["status"])
	}
	if removed, _ := body["toolRemoved"].(bool); !removed {
		t.Fatalf("toolRemoved = %v, want true", body["toolRemoved"])
	}
	if skillRemoved, _ := body["skillRemoved"].(bool); !skillRemoved {
		t.Fatalf("skillRemoved = %v, want true", body["skillRemoved"])
	}

	if _, err := os.Stat(skillDir); !os.IsNotExist(err) {
		t.Fatalf("skill directory should be deleted; stat err=%v", err)
	}

	updated, err := config.LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if _, ok := updated.Tools.MCP.Servers["mobile_browser"]; ok {
		t.Fatal("mobile_browser should be removed from config")
	}
}

func TestAdminMCPDeleteTool_PartialWhenSkillRemovalFails(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.json")
	workspace := filepath.Join(tmpDir, "workspace")
	if err := os.MkdirAll(filepath.Join(workspace, "skills"), 0o755); err != nil {
		t.Fatalf("MkdirAll(workspace skills) error = %v", err)
	}

	cfg := &config.Config{
		Tools: config.ToolsConfig{
			MCP: config.MCPConfig{
				Servers: map[string]config.MCPServerConfig{
					"mobile_browser": {
						Enabled: true,
						Type:    "http",
						URL:     "https://example.com/mcp/mobile-browser",
					},
				},
			},
		},
	}
	if err := config.SaveConfig(cfgPath, cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	h := &adminHandler{configPath: cfgPath, secret: "test-secret"}
	mux := http.NewServeMux()
	h.registerRoutes(mux)

	req := httptest.NewRequest(
		http.MethodDelete,
		"/api/admin/mcp/tools/mobile_browser?skillName=missing-skill&workspacePath="+url.QueryEscape(workspace),
		nil,
	)
	req.Header.Set("Authorization", "Bearer test-secret")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	body := decodeJSONMap(t, rec.Body.String())
	if status, _ := body["status"].(string); status != "partial" {
		t.Fatalf("status field = %v, want partial", body["status"])
	}
	if removed, _ := body["toolRemoved"].(bool); !removed {
		t.Fatalf("toolRemoved = %v, want true", body["toolRemoved"])
	}
	if skillRemoved, _ := body["skillRemoved"].(bool); skillRemoved {
		t.Fatalf("skillRemoved = %v, want false", body["skillRemoved"])
	}
	errors, _ := body["errors"].([]any)
	if len(errors) == 0 {
		t.Fatalf("errors should not be empty: %v", body["errors"])
	}
	if !strings.Contains(rec.Body.String(), "not found") {
		t.Fatalf("expected not found error in response body: %s", rec.Body.String())
	}

	updated, err := config.LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if _, ok := updated.Tools.MCP.Servers["mobile_browser"]; ok {
		t.Fatal("mobile_browser should be removed from config even when skill uninstall fails")
	}
}

func TestAdminMCPDeleteTool_PartialWhenSkillTargetMissing(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.json")
	cfg := &config.Config{
		Tools: config.ToolsConfig{
			MCP: config.MCPConfig{
				Servers: map[string]config.MCPServerConfig{
					"memory": {
						Enabled: true,
						Type:    "http",
						URL:     "https://example.com/mcp/memory",
					},
				},
			},
		},
	}
	if err := config.SaveConfig(cfgPath, cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	h := &adminHandler{configPath: cfgPath, secret: "test-secret"}
	mux := http.NewServeMux()
	h.registerRoutes(mux)

	req := httptest.NewRequest(http.MethodDelete, "/api/admin/mcp/tools/memory?skillName=demo-skill", nil)
	req.Header.Set("Authorization", "Bearer test-secret")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	body := decodeJSONMap(t, rec.Body.String())
	if status, _ := body["status"].(string); status != "partial" {
		t.Fatalf("status field = %v, want partial", body["status"])
	}
	if removed, _ := body["toolRemoved"].(bool); !removed {
		t.Fatalf("toolRemoved = %v, want true", body["toolRemoved"])
	}
	if skillRemoved, _ := body["skillRemoved"].(bool); skillRemoved {
		t.Fatalf("skillRemoved = %v, want false", body["skillRemoved"])
	}
	if !strings.Contains(rec.Body.String(), "agentId or workspacePath is required") {
		t.Fatalf("expected missing target validation in response body: %s", rec.Body.String())
	}
}

func TestAdminMCPDeleteTool_RejectsInvalidToolName(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.json")
	cfg := &config.Config{
		Tools: config.ToolsConfig{
			MCP: config.MCPConfig{
				Servers: map[string]config.MCPServerConfig{
					"web_scrape": {
						Enabled: true,
						Type:    "http",
						URL:     "https://example.com/mcp/web-scrape",
					},
				},
			},
		},
	}
	if err := config.SaveConfig(cfgPath, cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	h := &adminHandler{configPath: cfgPath, secret: "test-secret"}
	mux := http.NewServeMux()
	h.registerRoutes(mux)

	req := httptest.NewRequest(http.MethodDelete, "/api/admin/mcp/tools/bad%24tool", nil)
	req.Header.Set("Authorization", "Bearer test-secret")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body=%s", rec.Code, rec.Body.String())
	}

	updated, err := config.LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if _, ok := updated.Tools.MCP.Servers["web_scrape"]; !ok {
		t.Fatal("web_scrape should remain when request validation fails")
	}
}

func decodeJSONMap(t *testing.T, raw string) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal([]byte(raw), &body); err != nil {
		t.Fatalf("failed to decode JSON body %q: %v", raw, err)
	}
	return body
}

func findHeartbeatJobByAgentID(jobs []config.HeartbeatJobConfig, agentID string) (config.HeartbeatJobConfig, bool) {
	for _, job := range jobs {
		if job.AgentID == agentID {
			return job, true
		}
	}
	return config.HeartbeatJobConfig{}, false
}
