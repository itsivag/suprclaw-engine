package checkpoint

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/itsivag/suprclaw/pkg/providers"
)

// ToolHook is a per-agent-turn object that integrates checkpoint behavior
// into the agent loop. It is NOT safe for concurrent use across methods —
// LogToolCall may be called from multiple goroutines, but CheckpointBefore
// and IncrementAndMaybeCheckpoint must be called only from the main goroutine.
type ToolHook struct {
	svc        *Service
	agentID    string
	sessionKey string
	workspace  string
	cfg        *Config

	mu         sync.Mutex // protects callCount
	callCount  int
	lastCommit atomic.Value // string: last commit ID taken by this hook
}

// newToolHook creates a hook for a single agent-turn execution.
func newToolHook(svc *Service, agentID, sessionKey, workspace string, cfg *Config) *ToolHook {
	return &ToolHook{
		svc:        svc,
		agentID:    agentID,
		sessionKey: sessionKey,
		workspace:  workspace,
		cfg:        cfg,
	}
}

// LogToolCall appends an audit entry for a completed tool call.
// Goroutine-safe; may be called concurrently from tool execution goroutines.
func (h *ToolHook) LogToolCall(
	toolName string,
	args map[string]any,
	resultPreview string,
	isError bool,
	sideEffect string,
) {
	entry := ActionEntry{
		AgentID:       h.agentID,
		SessionKey:    h.sessionKey,
		ToolName:      toolName,
		ArgsDigest:    DigestArgs(args),
		ResultPreview: truncate(resultPreview, 200),
		IsError:       isError,
		SideEffect:    sideEffect,
	}

	// For external side-effects, store full args for forensic value.
	if sideEffect == SideEffectExternal {
		entry.ArgsFull = args
	}

	if lastID, ok := h.lastCommit.Load().(string); ok {
		entry.CommitID = lastID
	}

	_ = h.svc.actionLog.Append(entry)
}

// CheckpointBefore takes a checkpoint if any tool in toolNames is in the
// checkpoint_before list. Must be called from the main goroutine before
// the tool goroutines start.
func (h *ToolHook) CheckpointBefore(
	ctx context.Context,
	toolNames []string,
	msgs []providers.Message,
) error {
	if h.cfg == nil || !h.cfg.Enabled {
		return nil
	}
	for _, name := range toolNames {
		if h.cfg.HasTool(name) {
			label := "before " + name
			if len(toolNames) > 1 {
				label = "before round"
			}
			return h.takeCheckpoint(ctx, msgs, label, "pre_tool:"+name)
		}
	}
	return nil
}

// IncrementAndMaybeCheckpoint increments the tool call counter and takes a
// periodic checkpoint when EveryNToolCalls is reached.
// Must be called from the main goroutine after wg.Wait().
func (h *ToolHook) IncrementAndMaybeCheckpoint(
	ctx context.Context,
	n int,
	msgs []providers.Message,
) error {
	if h.cfg == nil || !h.cfg.Enabled || h.cfg.EveryNToolCalls <= 0 {
		return nil
	}
	h.mu.Lock()
	h.callCount += n
	reached := h.callCount >= h.cfg.EveryNToolCalls
	if reached {
		h.callCount = 0
	}
	h.mu.Unlock()

	if !reached {
		return nil
	}
	return h.takeCheckpoint(ctx, msgs, "periodic", "periodic")
}

func (h *ToolHook) takeCheckpoint(
	ctx context.Context,
	msgs []providers.Message,
	label, trigger string,
) error {
	parentID := ""
	if lastID, ok := h.lastCommit.Load().(string); ok {
		parentID = lastID
	}

	m, err := h.svc.CreateCommit(ctx, h.agentID, h.sessionKey, h.workspace, msgs, label, trigger, parentID)
	if err != nil {
		return err
	}
	h.lastCommit.Store(m.ID)
	return nil
}
