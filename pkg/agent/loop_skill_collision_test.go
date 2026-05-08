package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/itsivag/suprclaw/pkg/bus"
	"github.com/itsivag/suprclaw/pkg/config"
)

func TestAgentLoop_StartupCollisionBlocksRuntimeProcessing(t *testing.T) {
	tmpDir := t.TempDir()
	homeDir := filepath.Join(tmpDir, "home")
	t.Setenv("SUPRCLAW_HOME", homeDir)

	workspace := filepath.Join(tmpDir, "workspace")
	if err := os.MkdirAll(filepath.Join(workspace, "skills", "shared-skill"), 0o755); err != nil {
		t.Fatalf("MkdirAll(workspace skill): %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(workspace, "skills", "shared-skill", "SKILL.md"),
		[]byte("---\nname: shared-skill\ndescription: workspace\n---\n\n# shared"),
		0o644,
	); err != nil {
		t.Fatalf("WriteFile(workspace skill): %v", err)
	}
	globalSkill := filepath.Join(homeDir, "shared-skills", "shared-skill")
	if err := os.MkdirAll(globalSkill, 0o755); err != nil {
		t.Fatalf("MkdirAll(global skill): %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(globalSkill, "SKILL.md"),
		[]byte("---\nname: shared-skill\ndescription: global\n---\n\n# shared"),
		0o644,
	); err != nil {
		t.Fatalf("WriteFile(global skill): %v", err)
	}

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         workspace,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{
				{ID: "main", Default: true, Scope: config.AgentScopeWorkforce},
			},
		},
		Tools: config.ToolsConfig{
			Skills: config.SkillsToolsConfig{
				GlobalDir: "shared-skills",
			},
		},
	}

	al := NewAgentLoop(cfg, bus.NewMessageBus(), &mockProvider{})
	if al.GetSkillCollisionError() == nil {
		t.Fatal("expected startup skill collision error")
	}

	_, err := al.ProcessDirect(context.Background(), "hello", "session-1")
	if err == nil {
		t.Fatal("expected ProcessDirect to fail when collision-blocked")
	}
	var reqErr *RequestError
	if !errors.As(err, &reqErr) {
		t.Fatalf("expected RequestError, got %T (%v)", err, err)
	}
	if reqErr.Code != ErrCodeSkillScopeCollision {
		t.Fatalf("error code = %q, want %q", reqErr.Code, ErrCodeSkillScopeCollision)
	}
}
