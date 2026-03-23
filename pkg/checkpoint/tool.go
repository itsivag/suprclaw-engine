package checkpoint

import (
	"context"
	"fmt"
	"time"

	"github.com/itsivag/suprclaw/pkg/providers"
	"github.com/itsivag/suprclaw/pkg/tools"
)

// CheckpointTool implements tools.Tool so an LLM can name and save checkpoints
// mid-task. Register it via agent.Tools.Register(svc.NewCheckpointTool(...)).
type CheckpointTool struct {
	svc        *Service
	agentID    string
	sessionKey string
	workspace  string
	getHistory func() []providers.Message
}

// NewCheckpointTool creates the agent self-checkpoint tool.
// getHistory returns the current session messages (called at execution time).
func NewCheckpointTool(
	svc *Service,
	agentID, sessionKey, workspace string,
	getHistory func() []providers.Message,
) *CheckpointTool {
	return &CheckpointTool{
		svc:        svc,
		agentID:    agentID,
		sessionKey: sessionKey,
		workspace:  workspace,
		getHistory: getHistory,
	}
}

// Name returns the tool name.
func (t *CheckpointTool) Name() string { return "checkpoint" }

// Description returns the tool description.
func (t *CheckpointTool) Description() string {
	return "Save a named checkpoint of the current session and workspace state. " +
		"Use before making risky changes so you can roll back if needed."
}

// Parameters returns the tool's JSON Schema parameters.
func (t *CheckpointTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"label": map[string]any{
				"type":        "string",
				"description": "Human-readable label for the checkpoint (e.g. 'before refactor').",
			},
		},
		"required": []string{},
	}
}

// Execute creates a commit with the provided label.
func (t *CheckpointTool) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	label, _ := args["label"].(string)
	if label == "" {
		label = fmt.Sprintf("manual checkpoint at %s", time.Now().Format(time.RFC3339))
	}

	msgs := t.getHistory()
	commit, err := t.svc.CreateCommit(ctx, t.agentID, t.sessionKey, t.workspace, msgs, label, "agent_tool", "")
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("Checkpoint failed: %v", err)).WithError(err)
	}
	return &tools.ToolResult{
		ForLLM: fmt.Sprintf("Checkpoint saved: %s (%s)", commit.ID[:12], label),
	}
}

// SideEffectType returns "none" — checkpointing is a local-only operation.
func (t *CheckpointTool) SideEffectType() string { return SideEffectNone }
