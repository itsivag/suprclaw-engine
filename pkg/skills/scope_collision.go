package skills

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const SkillScopeCollisionCode = "SKILL_SCOPE_COLLISION"

type SkillScope string

const (
	SkillScopeWorkspace SkillScope = "workspace"
	SkillScopeGlobal    SkillScope = "global"
	SkillScopeBuiltin   SkillScope = "builtin"
)

type SkillScopeRef struct {
	Name    string     `json:"name"`
	Source  string     `json:"source"`
	Scope   SkillScope `json:"scope"`
	Path    string     `json:"path"`
	AgentID string     `json:"agent_id,omitempty"`
}

type SkillScopeCollision struct {
	Name     string        `json:"name"`
	Winner   SkillScopeRef `json:"winner"`
	Conflict SkillScopeRef `json:"conflict"`
}

type SkillScopeCollisionError struct {
	Code       string                `json:"code"`
	ErrMessage string                `json:"error"`
	Collisions []SkillScopeCollision `json:"collisions"`
}

func (e *SkillScopeCollisionError) Error() string {
	if e == nil {
		return ""
	}
	if e.ErrMessage != "" {
		return e.ErrMessage
	}
	return "skill scope collision detected"
}

func NewSkillScopeCollisionError(collisions []SkillScopeCollision) *SkillScopeCollisionError {
	if len(collisions) == 0 {
		return nil
	}

	copied := make([]SkillScopeCollision, len(collisions))
	copy(copied, collisions)
	sort.Slice(copied, func(i, j int) bool {
		if copied[i].Name != copied[j].Name {
			return copied[i].Name < copied[j].Name
		}
		if copied[i].Conflict.AgentID != copied[j].Conflict.AgentID {
			return copied[i].Conflict.AgentID < copied[j].Conflict.AgentID
		}
		return copied[i].Conflict.Path < copied[j].Conflict.Path
	})

	return &SkillScopeCollisionError{
		Code:       SkillScopeCollisionCode,
		ErrMessage: "skill scope collision detected",
		Collisions: copied,
	}
}

func AsSkillScopeCollisionError(err error) *SkillScopeCollisionError {
	if err == nil {
		return nil
	}
	var target *SkillScopeCollisionError
	if errors.As(err, &target) {
		return target
	}
	return nil
}

type SkillScopeInventory struct {
	GlobalSkills           []SkillScopeRef            `json:"global_skills"`
	WorkspaceSkillsByAgent map[string][]SkillScopeRef `json:"workspace_skills_by_agent"`
	Collisions             []SkillScopeCollision      `json:"collisions"`
}

func BuildSkillScopeInventory(globalDir string, workspaceByAgent map[string]string) (*SkillScopeInventory, error) {
	inv := &SkillScopeInventory{
		GlobalSkills:           make([]SkillScopeRef, 0),
		WorkspaceSkillsByAgent: make(map[string][]SkillScopeRef),
		Collisions:             make([]SkillScopeCollision, 0),
	}

	globalSkills, err := scanSkillScopeDir(globalDir, SkillScopeGlobal, "")
	if err != nil {
		return nil, err
	}
	inv.GlobalSkills = globalSkills

	agentIDs := make([]string, 0, len(workspaceByAgent))
	for agentID := range workspaceByAgent {
		agentIDs = append(agentIDs, agentID)
	}
	sort.Strings(agentIDs)

	for _, agentID := range agentIDs {
		workspace := strings.TrimSpace(workspaceByAgent[agentID])
		if workspace == "" {
			continue
		}
		workspaceSkillsDir := filepath.Join(workspace, "skills")
		workspaceSkills, scanErr := scanSkillScopeDir(workspaceSkillsDir, SkillScopeWorkspace, agentID)
		if scanErr != nil {
			return nil, scanErr
		}
		inv.WorkspaceSkillsByAgent[agentID] = workspaceSkills
	}

	inv.Collisions = detectCrossScopeCollisions(inv.GlobalSkills, inv.WorkspaceSkillsByAgent)
	return inv, nil
}

func (inv *SkillScopeInventory) CollisionError() *SkillScopeCollisionError {
	if inv == nil || len(inv.Collisions) == 0 {
		return nil
	}
	return NewSkillScopeCollisionError(inv.Collisions)
}

func scanSkillScopeDir(rootDir string, scope SkillScope, agentID string) ([]SkillScopeRef, error) {
	rootDir = strings.TrimSpace(rootDir)
	if rootDir == "" {
		return []SkillScopeRef{}, nil
	}

	entries, err := os.ReadDir(rootDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []SkillScopeRef{}, nil
		}
		return nil, fmt.Errorf("read skill scope dir %q: %w", rootDir, err)
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})

	skillsOut := make([]SkillScopeRef, 0)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		skillFile := filepath.Join(rootDir, entry.Name(), "SKILL.md")
		if _, statErr := os.Stat(skillFile); statErr != nil {
			continue
		}

		skillName := entry.Name()
		if meta := ReadSkillMetadata(skillFile); meta != nil && strings.TrimSpace(meta.Name) != "" {
			skillName = strings.TrimSpace(meta.Name)
		}

		// Keep compatibility with existing loader behavior: ignore invalid names.
		info := SkillInfo{
			Name:        skillName,
			Description: "scope-scan",
		}
		if validateErr := info.validate(); validateErr != nil {
			continue
		}

		skillsOut = append(skillsOut, SkillScopeRef{
			Name:    skillName,
			Source:  string(scope),
			Scope:   scope,
			Path:    skillFile,
			AgentID: strings.TrimSpace(agentID),
		})
	}

	sort.Slice(skillsOut, func(i, j int) bool {
		if skillsOut[i].Name != skillsOut[j].Name {
			return skillsOut[i].Name < skillsOut[j].Name
		}
		return skillsOut[i].Path < skillsOut[j].Path
	})
	return skillsOut, nil
}

func detectCrossScopeCollisions(global []SkillScopeRef, workspaceByAgent map[string][]SkillScopeRef) []SkillScopeCollision {
	if len(global) == 0 || len(workspaceByAgent) == 0 {
		return []SkillScopeCollision{}
	}

	globalByName := make(map[string][]SkillScopeRef)
	for _, g := range global {
		globalByName[g.Name] = append(globalByName[g.Name], g)
	}

	collisions := make([]SkillScopeCollision, 0)
	agentIDs := make([]string, 0, len(workspaceByAgent))
	for agentID := range workspaceByAgent {
		agentIDs = append(agentIDs, agentID)
	}
	sort.Strings(agentIDs)

	for _, agentID := range agentIDs {
		workspaceSkills := workspaceByAgent[agentID]
		for _, wsSkill := range workspaceSkills {
			conflicts, ok := globalByName[wsSkill.Name]
			if !ok {
				continue
			}
			for _, gSkill := range conflicts {
				collisions = append(collisions, SkillScopeCollision{
					Name:     wsSkill.Name,
					Winner:   wsSkill,
					Conflict: gSkill,
				})
			}
		}
	}

	sort.Slice(collisions, func(i, j int) bool {
		if collisions[i].Name != collisions[j].Name {
			return collisions[i].Name < collisions[j].Name
		}
		if collisions[i].Winner.AgentID != collisions[j].Winner.AgentID {
			return collisions[i].Winner.AgentID < collisions[j].Winner.AgentID
		}
		return collisions[i].Winner.Path < collisions[j].Winner.Path
	})
	return collisions
}
