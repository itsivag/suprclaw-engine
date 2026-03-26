package skills

import (
	"path/filepath"
	"testing"

	"github.com/itsivag/suprclaw/pkg/config"
)

func TestResolveSkillsLoaderPaths_UsesSharedGlobalDirResolver(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("SUPRCLAW_HOME", filepath.Join(tmpDir, "home"))
	t.Setenv("SUPRCLAW_CONFIG", filepath.Join(tmpDir, "custom", "config.json"))

	cfg := &config.Config{
		Tools: config.ToolsConfig{
			Skills: config.SkillsToolsConfig{
				GlobalDir: "shared-skills",
			},
		},
	}

	globalSkillsDir, builtinSkillsDir := resolveSkillsLoaderPaths(cfg)

	wantGlobal := filepath.Join(tmpDir, "home", "shared-skills")
	if globalSkillsDir != wantGlobal {
		t.Fatalf("globalSkillsDir = %q, want %q", globalSkillsDir, wantGlobal)
	}

	wantBuiltin := filepath.Join(tmpDir, "custom", "suprclaw", "skills")
	if builtinSkillsDir != wantBuiltin {
		t.Fatalf("builtinSkillsDir = %q, want %q", builtinSkillsDir, wantBuiltin)
	}
}
