package skills

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBuildSkillScopeInventory_DetectsGlobalWorkspaceCollision(t *testing.T) {
	tmp := t.TempDir()
	globalDir := filepath.Join(tmp, "global")
	workspaceDir := filepath.Join(tmp, "workspace-main")

	createSkillDir := func(baseDir, skillName string) {
		t.Helper()
		dir := filepath.Join(baseDir, skillName)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("MkdirAll(%s): %v", dir, err)
		}
		content := "---\nname: " + skillName + "\ndescription: test\n---\n\n# " + skillName
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
			t.Fatalf("WriteFile(SKILL.md): %v", err)
		}
	}

	createSkillDir(globalDir, "shared-skill")
	createSkillDir(filepath.Join(workspaceDir, "skills"), "shared-skill")

	inv, err := BuildSkillScopeInventory(globalDir, map[string]string{"main": workspaceDir})
	if err != nil {
		t.Fatalf("BuildSkillScopeInventory() error: %v", err)
	}

	if len(inv.GlobalSkills) != 1 {
		t.Fatalf("global skills = %d, want 1", len(inv.GlobalSkills))
	}
	if len(inv.WorkspaceSkillsByAgent["main"]) != 1 {
		t.Fatalf("workspace skills for main = %d, want 1", len(inv.WorkspaceSkillsByAgent["main"]))
	}
	if len(inv.Collisions) != 1 {
		t.Fatalf("collisions = %d, want 1", len(inv.Collisions))
	}

	collision := inv.Collisions[0]
	if collision.Name != "shared-skill" {
		t.Fatalf("collision name = %q, want shared-skill", collision.Name)
	}
	if collision.Winner.Scope != SkillScopeWorkspace {
		t.Fatalf("winner scope = %q, want workspace", collision.Winner.Scope)
	}
	if collision.Winner.AgentID != "main" {
		t.Fatalf("winner agent_id = %q, want main", collision.Winner.AgentID)
	}
	if collision.Conflict.Scope != SkillScopeGlobal {
		t.Fatalf("conflict scope = %q, want global", collision.Conflict.Scope)
	}
}

func TestSkillScopeCollisionError_CodeAndPayload(t *testing.T) {
	err := NewSkillScopeCollisionError([]SkillScopeCollision{
		{
			Name: "shared-skill",
			Winner: SkillScopeRef{
				Name:    "shared-skill",
				Scope:   SkillScopeWorkspace,
				Source:  "workspace",
				Path:    "/workspace/skills/shared-skill/SKILL.md",
				AgentID: "main",
			},
			Conflict: SkillScopeRef{
				Name:   "shared-skill",
				Scope:  SkillScopeGlobal,
				Source: "global",
				Path:   "/global/shared-skill/SKILL.md",
			},
		},
	})

	if err == nil {
		t.Fatal("expected non-nil collision error")
	}
	if err.Code != SkillScopeCollisionCode {
		t.Fatalf("code = %q, want %q", err.Code, SkillScopeCollisionCode)
	}
	if len(err.Collisions) != 1 {
		t.Fatalf("collisions = %d, want 1", len(err.Collisions))
	}
}
