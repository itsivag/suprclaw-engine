package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestContextBuilderDynamicSkillActivation(t *testing.T) {
	workspace := t.TempDir()

	alwaysDir := filepath.Join(workspace, "skills", "always-skill")
	if err := os.MkdirAll(alwaysDir, 0o755); err != nil {
		t.Fatalf("mkdir always skill: %v", err)
	}
	alwaysContent := "---\nname: always-skill\ndescription: always available\n---\n\n# Always"
	if err := os.WriteFile(filepath.Join(alwaysDir, "SKILL.md"), []byte(alwaysContent), 0o644); err != nil {
		t.Fatalf("write always skill: %v", err)
	}

	conditionalDir := filepath.Join(workspace, "skills", "go-skill")
	if err := os.MkdirAll(conditionalDir, 0o755); err != nil {
		t.Fatalf("mkdir conditional skill: %v", err)
	}
	conditionalContent := "---\nname: go-skill\ndescription: go files helper\npaths:\n  - src/**/*.go\n---\n\n# Go Skill"
	if err := os.WriteFile(filepath.Join(conditionalDir, "SKILL.md"), []byte(conditionalContent), 0o644); err != nil {
		t.Fatalf("write conditional skill: %v", err)
	}

	cb := NewContextBuilderWithSkillDirs(workspace, "", "")

	initialMsgs := cb.BuildMessages(nil, "", "hi", nil, "cli", "chat-1", "", "")
	if len(initialMsgs) == 0 {
		t.Fatalf("expected system message")
	}
	if !strings.Contains(initialMsgs[0].Content, "always-skill") {
		t.Fatalf("always-active skill should appear in initial system prompt")
	}
	if strings.Contains(initialMsgs[0].Content, "go-skill") {
		t.Fatalf("conditional skill should not appear before activation")
	}

	activated := cb.ActivateSkillsForPaths("cli", "chat-1", []string{filepath.Join(workspace, "src", "main.go")})
	if len(activated) != 1 || activated[0] != "go-skill" {
		t.Fatalf("expected go-skill activation, got %v", activated)
	}

	updatedMsgs := cb.BuildMessages(nil, "", "hi again", nil, "cli", "chat-1", "", "")
	if !strings.Contains(updatedMsgs[0].Content, "go-skill") {
		t.Fatalf("activated conditional skill should appear in session skills section")
	}
}

func TestContextBuilderSectionedBoundaryMarker(t *testing.T) {
	workspace := t.TempDir()
	cb := NewContextBuilderWithSkillDirs(workspace, "", "")

	prompt := cb.BuildSystemPrompt()
	if !strings.Contains(prompt, systemPromptDynamicBoundary) {
		t.Fatalf("expected system prompt to include dynamic boundary marker")
	}
}
