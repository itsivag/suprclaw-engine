package agent

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	providerscommon "github.com/itsivag/suprclaw/pkg/providers/common"
	"github.com/itsivag/suprclaw/pkg/skills"
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

	initialMsgs := cb.BuildMessages(nil, "", "hi", nil, "chat-1", "cli", "chat-1", "", "")
	if len(initialMsgs) == 0 {
		t.Fatalf("expected system message")
	}
	if !strings.Contains(initialMsgs[0].Content, "always-skill") {
		t.Fatalf("always-active skill should appear in initial system prompt")
	}
	if strings.Contains(initialMsgs[0].Content, "go-skill") {
		t.Fatalf("conditional skill should not appear before activation")
	}

	activated, deactivated := cb.RecomputeSessionSkills("chat-1", []string{filepath.Join(workspace, "src", "main.go")})
	if len(activated) != 1 || activated[0] != "go-skill" {
		t.Fatalf("expected go-skill activation, got %v", activated)
	}
	if len(deactivated) != 0 {
		t.Fatalf("expected no deactivations, got %v", deactivated)
	}

	updatedMsgs := cb.BuildMessages(nil, "", "hi again", nil, "chat-1", "cli", "chat-1", "", "")
	if !strings.Contains(updatedMsgs[0].Content, "go-skill") {
		t.Fatalf("activated conditional skill should appear in session skills section")
	}

	_, deactivated = cb.RecomputeSessionSkills("chat-1", nil)
	if len(deactivated) != 1 || deactivated[0] != "go-skill" {
		t.Fatalf("expected go-skill deactivation when evidence is absent, got %v", deactivated)
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

func TestContextBuilderClearSessionSkillState(t *testing.T) {
	workspace := t.TempDir()

	conditionalDir := filepath.Join(workspace, "skills", "go-skill")
	if err := os.MkdirAll(conditionalDir, 0o755); err != nil {
		t.Fatalf("mkdir conditional skill: %v", err)
	}
	conditionalContent := "---\nname: go-skill\ndescription: go files helper\npaths:\n  - src/**/*.go\n---\n\n# Go Skill"
	if err := os.WriteFile(filepath.Join(conditionalDir, "SKILL.md"), []byte(conditionalContent), 0o644); err != nil {
		t.Fatalf("write conditional skill: %v", err)
	}

	cb := NewContextBuilderWithSkillDirs(workspace, "", "")
	activated, _ := cb.RecomputeSessionSkills("session-1", []string{filepath.Join(workspace, "src", "main.go")})
	if len(activated) != 1 || activated[0] != "go-skill" {
		t.Fatalf("expected activation, got %v", activated)
	}

	cb.ClearSessionSkillState("session-1")
	msgs := cb.BuildMessages(nil, "", "hello", nil, "session-1", "cli", "chat", "", "")
	if strings.Contains(msgs[0].Content, "go-skill") {
		t.Fatalf("expected cleared session state to remove activated skill section")
	}
}

func TestContextBuilderRecomputeSessionSkills_ConcurrentSessions(t *testing.T) {
	workspace := t.TempDir()
	conditionalDir := filepath.Join(workspace, "skills", "go-skill")
	if err := os.MkdirAll(conditionalDir, 0o755); err != nil {
		t.Fatalf("mkdir conditional skill: %v", err)
	}
	conditionalContent := "---\nname: go-skill\ndescription: go files helper\npaths:\n  - src/**/*.go\n---\n\n# Go Skill"
	if err := os.WriteFile(filepath.Join(conditionalDir, "SKILL.md"), []byte(conditionalContent), 0o644); err != nil {
		t.Fatalf("write conditional skill: %v", err)
	}

	cb := NewContextBuilderWithSkillDirs(workspace, "", "")
	const workers = 24
	const iterations = 64

	var wg sync.WaitGroup
	wg.Add(workers)
	for w := 0; w < workers; w++ {
		go func(worker int) {
			defer wg.Done()
			session := "session-a"
			if worker%2 == 0 {
				session = "session-b"
			}
			for i := 0; i < iterations; i++ {
				if i%3 == 0 {
					cb.RecomputeSessionSkills(session, nil)
					continue
				}
				cb.RecomputeSessionSkills(session, []string{filepath.Join(workspace, "src", "main.go")})
			}
		}(w)
	}
	wg.Wait()
}

func TestRenderSkillsSummaryTokenBudgetPressure(t *testing.T) {
	workspace := t.TempDir()
	cb := NewContextBuilderWithSkillDirs(workspace, "", "")

	longDesc := strings.Repeat("detailed-description ", 120)
	skillsIn := []skills.SkillInfo{
		{Name: "alpha-skill", Description: longDesc, Path: "/tmp/alpha", Source: "workspace"},
		{Name: "beta-skill", Description: longDesc, Path: "/tmp/beta", Source: "workspace"},
	}

	t.Setenv("SUPRCLAW_SKILLS_SUMMARY_TOKEN_BUDGET", "5000")
	highBudgetSummary := cb.renderSkillsSummary(skillsIn)
	highSummaryTokens := providerscommon.EstimateTokenCount(
		[]providerscommon.Message{{Role: "system", Content: highBudgetSummary}},
		nil,
		"skills-summary",
		nil,
	)
	if highSummaryTokens <= 300 {
		t.Fatalf("unexpected baseline token estimate too small: %d", highSummaryTokens)
	}

	lowBudget := highSummaryTokens - 80
	t.Setenv("SUPRCLAW_SKILLS_SUMMARY_TOKEN_BUDGET", strconv.Itoa(lowBudget))
	lowBudgetSummary := cb.renderSkillsSummary(skillsIn)

	if !strings.Contains(lowBudgetSummary, "alpha-skill") || !strings.Contains(lowBudgetSummary, "beta-skill") {
		t.Fatalf("token budget truncation must keep all skill names visible")
	}

	lowSummaryTokens := providerscommon.EstimateTokenCount(
		[]providerscommon.Message{{Role: "system", Content: lowBudgetSummary}},
		nil,
		"skills-summary",
		nil,
	)
	if lowSummaryTokens > lowBudget {
		t.Fatalf("expected low-budget summary tokens <= %d, got %d", lowBudget, lowSummaryTokens)
	}
	if lowSummaryTokens >= highSummaryTokens {
		t.Fatalf("expected token-budget pressure to reduce summary tokens: high=%d low=%d", highSummaryTokens, lowSummaryTokens)
	}
}
