package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/itsivag/suprclaw/pkg/skills"
	"github.com/itsivag/suprclaw/pkg/utils"
)

// SkillTool loads skill content on-demand so skills do not need to be eagerly
// embedded into every system prompt.
type SkillTool struct {
	loader *skills.SkillsLoader
}

func NewSkillTool(loader *skills.SkillsLoader) *SkillTool {
	return &SkillTool{loader: loader}
}

func (t *SkillTool) Name() string {
	return "skill"
}

func (t *SkillTool) Description() string {
	return "Load a skill by name for on-demand execution. Returns normalized skill metadata and prompt content."
}

func (t *SkillTool) UsageContract() ToolUsageContract {
	return ToolUsageContract{
		UseWhen:      "you need to execute a specific skill and require its exact SKILL.md instructions.",
		DoNotUseWhen: "the task does not map to a known installed skill.",
		HardRequirements: []string{
			"skill_name must be a valid identifier.",
			"Skill loading failures must be returned as explicit errors.",
			"Returned payload must include the exact resolved skill content.",
		},
	}
}

func (t *SkillTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"skill_name": map[string]any{
				"type":        "string",
				"description": "Installed skill name (e.g. commit, review-pr, find-skills)",
			},
			"args": map[string]any{
				"type":        "string",
				"description": "Optional free-form arguments to pass into the skill execution context",
			},
		},
		"required": []string{"skill_name"},
	}
}

type skillToolPayload struct {
	Type      string `json:"type"`
	SkillName string `json:"skill_name"`
	Args      string `json:"args,omitempty"`
	Source    string `json:"source,omitempty"`
	Path      string `json:"path,omitempty"`
	Content   string `json:"content"`
}

func (t *SkillTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	skillName, _ := args["skill_name"].(string)
	skillName = strings.TrimSpace(skillName)
	if err := utils.ValidateSkillIdentifier(skillName); err != nil {
		return ErrorResult(fmt.Sprintf("invalid skill_name %q: %s", skillName, err.Error()))
	}

	skillArgs, _ := args["args"].(string)
	skillArgs = strings.TrimSpace(skillArgs)

	content, ok := t.loader.LoadSkill(skillName)
	if !ok {
		available := t.loader.ListSkills()
		names := make([]string, 0, len(available))
		for _, s := range available {
			names = append(names, s.Name)
		}
		sort.Strings(names)
		return ErrorResult(fmt.Sprintf("skill %q not found. available skills: %s", skillName, strings.Join(names, ", ")))
	}

	payload := skillToolPayload{
		Type:      "skill_invocation",
		SkillName: skillName,
		Args:      skillArgs,
		Content:   content,
	}

	for _, s := range t.loader.ListSkills() {
		if s.Name == skillName {
			payload.Source = s.Source
			payload.Path = s.Path
			break
		}
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return ErrorResult("failed to serialize skill payload")
	}

	return SilentResult(string(body))
}
