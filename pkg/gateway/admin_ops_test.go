package gateway

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/itsivag/suprclaw/pkg/agent"
	"github.com/itsivag/suprclaw/pkg/bus"
	"github.com/itsivag/suprclaw/pkg/config"
)

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
				{ID: "main", Default: true},
				{ID: "writer"},
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
				{ID: "main", Default: true},
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
				{ID: "main", Default: true},
				{ID: "writer"},
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
