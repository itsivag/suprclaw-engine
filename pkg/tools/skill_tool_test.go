package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/itsivag/suprclaw/pkg/skills"
)

func TestSkillToolExecute(t *testing.T) {
	workspace := t.TempDir()
	skillDir := filepath.Join(workspace, "skills", "demo-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir skill dir: %v", err)
	}
	content := "---\nname: demo-skill\ndescription: demo description\n---\n\n# Demo\n\nSkill body"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}

	loader := skills.NewSkillsLoader(workspace, "", "")
	tool := NewSkillTool(loader)

	res := tool.Execute(context.Background(), map[string]any{"skill_name": "demo-skill", "args": "--dry-run"})
	if res.IsError {
		t.Fatalf("expected success result, got error: %s", res.ForLLM)
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(res.ForLLM), &payload); err != nil {
		t.Fatalf("tool payload must be json: %v", err)
	}
	if payload["type"] != "skill_invocation" {
		t.Fatalf("unexpected payload type: %v", payload["type"])
	}
	if payload["skill_name"] != "demo-skill" {
		t.Fatalf("unexpected skill_name: %v", payload["skill_name"])
	}
}

func TestSkillToolExecuteMissingSkill(t *testing.T) {
	workspace := t.TempDir()
	loader := skills.NewSkillsLoader(workspace, "", "")
	tool := NewSkillTool(loader)

	res := tool.Execute(context.Background(), map[string]any{"skill_name": "missing-skill"})
	if !res.IsError {
		t.Fatalf("expected missing skill to return error")
	}
}
