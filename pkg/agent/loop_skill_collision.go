package agent

import (
	"fmt"
	"sort"
	"strings"

	"github.com/itsivag/suprclaw/pkg/config"
	"github.com/itsivag/suprclaw/pkg/logger"
	"github.com/itsivag/suprclaw/pkg/skills"
)

func (al *AgentLoop) GetSkillCollisionError() *skills.SkillScopeCollisionError {
	al.skillCollisionMu.RLock()
	defer al.skillCollisionMu.RUnlock()
	if al.skillCollisionErr == nil {
		return nil
	}
	copyErr := *al.skillCollisionErr
	copyErr.Collisions = append([]skills.SkillScopeCollision(nil), al.skillCollisionErr.Collisions...)
	return &copyErr
}

func (al *AgentLoop) isSkillCollisionBlocked() bool {
	al.skillCollisionMu.RLock()
	defer al.skillCollisionMu.RUnlock()
	return al.skillCollisionErr != nil
}

func (al *AgentLoop) setSkillCollisionError(collisionErr *skills.SkillScopeCollisionError) {
	al.skillCollisionMu.Lock()
	defer al.skillCollisionMu.Unlock()
	if collisionErr == nil {
		al.skillCollisionErr = nil
		return
	}
	copyErr := *collisionErr
	copyErr.Collisions = append([]skills.SkillScopeCollision(nil), collisionErr.Collisions...)
	al.skillCollisionErr = &copyErr
}

func (al *AgentLoop) skillCollisionRequestError() *RequestError {
	collisionErr := al.GetSkillCollisionError()
	if collisionErr == nil {
		return nil
	}
	al.skillCollisionPreflightRejects.Add(1)
	return &RequestError{
		Code:    ErrCodeSkillScopeCollision,
		Message: collisionErr.ErrMessage,
	}
}

func (al *AgentLoop) SkillCollisionRejectCounters() map[string]uint64 {
	return map[string]uint64{
		"preflight_rejects": al.skillCollisionPreflightRejects.Load(),
		"reload_rejects":    al.skillCollisionReloadRejects.Load(),
	}
}

func (al *AgentLoop) refreshSkillCollisionStateForRegistry(
	cfg *config.Config,
	registry *AgentRegistry,
) *skills.SkillScopeCollisionError {
	inventory, err := buildSkillScopeInventoryForRegistry(cfg, registry)
	if err != nil {
		logger.ErrorCF("agent", "Failed to evaluate skill scope collisions",
			map[string]any{"error": err.Error()},
		)
		return nil
	}
	collisionErr := inventory.CollisionError()
	al.setSkillCollisionError(collisionErr)
	if collisionErr != nil {
		logger.WarnCF("agent",
			"event=skill_scope_collision_startup_degraded runtime started in collision-degraded mode",
			map[string]any{
				"code":            collisionErr.Code,
				"collision_count": len(collisionErr.Collisions),
			},
		)
	}
	return collisionErr
}

func (al *AgentLoop) RefreshSkillCollisionState() error {
	cfg := al.GetConfig()
	registry := al.GetRegistry()
	inv, err := buildSkillScopeInventoryForRegistry(cfg, registry)
	if err != nil {
		return err
	}
	collisionErr := inv.CollisionError()
	al.setSkillCollisionError(collisionErr)
	return nil
}

func buildSkillScopeInventoryForRegistry(
	cfg *config.Config,
	registry *AgentRegistry,
) (*skills.SkillScopeInventory, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config cannot be nil")
	}
	if registry == nil {
		return nil, fmt.Errorf("registry cannot be nil")
	}

	workspaceByAgent := make(map[string]string)
	agentIDs := registry.ListAgentIDs()
	sort.Strings(agentIDs)
	for _, agentID := range agentIDs {
		inst, ok := registry.GetAgent(agentID)
		if !ok {
			continue
		}
		if strings.TrimSpace(inst.Workspace) == "" {
			continue
		}
		workspaceByAgent[agentID] = inst.Workspace
	}

	globalSkillsDir := config.ResolveGlobalSkillsDir(cfg.Tools.Skills.GlobalDir)
	inventory, err := skills.BuildSkillScopeInventory(globalSkillsDir, workspaceByAgent)
	if err != nil {
		return nil, err
	}
	return inventory, nil
}
