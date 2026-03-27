package gateway

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveSkillsWorkspace_NormalizesSkillsDirOverrideToWorkspaceRoot(t *testing.T) {
	tmpDir := t.TempDir()
	workspaceRoot := filepath.Join(tmpDir, ".suprclaw")
	skillsDir := filepath.Join(workspaceRoot, "skills")
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(skillsDir) error = %v", err)
	}

	h := &adminHandler{}
	got, err := h.resolveSkillsWorkspace("", skillsDir)
	if err != nil {
		t.Fatalf("resolveSkillsWorkspace() error = %v", err)
	}
	if got != workspaceRoot {
		t.Fatalf("workspacePath normalization mismatch: got %q, want %q", got, workspaceRoot)
	}
}

func TestAdminSkillsLoader_UsesSharedGlobalDirNotLegacyConfigDir(t *testing.T) {
	tmpDir := t.TempDir()
	homeDir := filepath.Join(tmpDir, "home")
	t.Setenv("SUPRCLAW_HOME", homeDir)

	configPath := filepath.Join(tmpDir, "config.json")
	configJSON := `{"tools":{"skills":{"global_dir":"shared-skills"}}}`
	if err := os.WriteFile(configPath, []byte(configJSON), 0o600); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}

	workspace := filepath.Join(tmpDir, "workspace")
	if err := os.MkdirAll(filepath.Join(workspace, "skills"), 0o755); err != nil {
		t.Fatalf("MkdirAll(workspace skills) error = %v", err)
	}

	// Legacy location (relative to config dir) should not be read after hard switch.
	legacySkill := filepath.Join(tmpDir, "skills", "legacy-skill")
	if err := os.MkdirAll(legacySkill, 0o755); err != nil {
		t.Fatalf("MkdirAll(legacy skill) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(legacySkill, "SKILL.md"), []byte(`---
name: legacy-skill
description: legacy
---
# Legacy`), 0o644); err != nil {
		t.Fatalf("WriteFile(legacy SKILL.md) error = %v", err)
	}

	loader, err := (&adminHandler{configPath: configPath}).newSkillsLoaderFor(workspace)
	if err != nil {
		t.Fatalf("newSkillsLoaderFor() error = %v", err)
	}

	got := loader.ListSkills()
	if len(got) != 0 {
		t.Fatalf("expected 0 skills (legacy config-dir skills should be ignored), got %d", len(got))
	}
}

func TestAdminSkillsLoader_LoadsConfiguredSharedGlobalDir(t *testing.T) {
	tmpDir := t.TempDir()
	homeDir := filepath.Join(tmpDir, "home")
	t.Setenv("SUPRCLAW_HOME", homeDir)

	configPath := filepath.Join(tmpDir, "config.json")
	configJSON := `{"tools":{"skills":{"global_dir":"shared-skills"}}}`
	if err := os.WriteFile(configPath, []byte(configJSON), 0o600); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}

	globalSkillDir := filepath.Join(homeDir, "shared-skills", "shared-skill")
	if err := os.MkdirAll(globalSkillDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(global skill) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(globalSkillDir, "SKILL.md"), []byte(`---
name: shared-skill
description: shared
---
# Shared`), 0o644); err != nil {
		t.Fatalf("WriteFile(global SKILL.md) error = %v", err)
	}

	workspace := filepath.Join(tmpDir, "workspace")
	if err := os.MkdirAll(filepath.Join(workspace, "skills"), 0o755); err != nil {
		t.Fatalf("MkdirAll(workspace skills) error = %v", err)
	}

	loader, err := (&adminHandler{configPath: configPath}).newSkillsLoaderFor(workspace)
	if err != nil {
		t.Fatalf("newSkillsLoaderFor() error = %v", err)
	}

	got := loader.ListSkills()
	if len(got) != 1 {
		t.Fatalf("expected 1 global skill, got %d", len(got))
	}
	if got[0].Name != "shared-skill" || got[0].Source != "global" {
		t.Fatalf("unexpected skill: %+v", got[0])
	}
}
