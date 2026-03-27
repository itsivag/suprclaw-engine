package browserrelay

import (
	"context"
	"fmt"
	"strings"
)

// ExecutableEngine is the execution contract for browser relay backends.
type ExecutableEngine interface {
	ListTargets(ctx context.Context) ([]Target, error)
	ListSessions(ctx context.Context) ([]Session, error)
	CreateSession(ctx context.Context, req ActionRequest) (any, error)
	CloseSession(ctx context.Context, sessionID string) error
	ExecuteAction(ctx context.Context, action string, req ActionRequest) (any, error)
}

// EngineRouter dispatches actions across extension and agent-browser engines.
type EngineRouter struct {
	cfg       Config
	extension ExecutableEngine
	agent     ExecutableEngine
}

func NewEngineRouter(cfg Config, extensionEngine ExecutableEngine, agentBrowserEngine ExecutableEngine) *EngineRouter {
	return &EngineRouter{
		cfg:       normalizeConfig(cfg),
		extension: extensionEngine,
		agent:     agentBrowserEngine,
	}
}

func (r *EngineRouter) ApplyConfig(cfg Config) {
	r.cfg = normalizeConfig(cfg)
}

func (r *EngineRouter) ExecuteAction(ctx context.Context, action string, req ActionRequest) (any, error) {
	switch action {
	case "tabs.list":
		targets, err := r.listTargets(ctx)
		if err != nil {
			return nil, err
		}
		return map[string]any{"targets": targets}, nil
	case "session.list":
		if !r.agentBrowserAvailable() {
			return nil, ErrAgentBrowserUnavailable
		}
		sessions, err := r.agent.ListSessions(ctx)
		if err != nil {
			return nil, err
		}
		return map[string]any{"sessions": sessions}, nil
	case "session.create":
		if !r.agentBrowserAvailable() {
			return nil, ErrAgentBrowserUnavailable
		}
		return r.agent.CreateSession(ctx, req)
	case "session.close":
		if !r.agentBrowserAvailable() {
			return nil, ErrAgentBrowserUnavailable
		}
		sessionID := strings.TrimSpace(req.SessionID)
		if sessionID == "" {
			sessionID, _, _ = ParseAgentBrowserTargetID(req.TargetID)
		}
		if strings.TrimSpace(sessionID) == "" {
			return nil, fmt.Errorf("%w: missing session_id", ErrInvalidTargetID)
		}
		if err := r.agent.CloseSession(ctx, sessionID); err != nil {
			return nil, err
		}
		return map[string]any{"ok": true}, nil
	}

	engine, err := r.resolveEngineForAction(action, req.TargetID)
	if err != nil {
		return nil, err
	}
	return engine.ExecuteAction(ctx, action, req)
}

func (r *EngineRouter) listTargets(ctx context.Context) ([]Target, error) {
	out := make([]Target, 0)
	if r.extension != nil && r.cfg.EngineMode != EngineModeAgentBrowser {
		targets, err := r.extension.ListTargets(ctx)
		if err != nil {
			return nil, err
		}
		out = append(out, targets...)
	}
	if r.agent != nil && r.agentBrowserAvailable() {
		targets, err := r.agent.ListTargets(ctx)
		if err != nil {
			return nil, err
		}
		out = append(out, targets...)
	}
	return out, nil
}

func (r *EngineRouter) resolveEngineForAction(action, targetID string) (ExecutableEngine, error) {
	targetID = strings.TrimSpace(targetID)
	if isAgentBrowserTargetID(targetID) {
		if !r.agentBrowserAvailable() || r.agent == nil {
			return nil, ErrAgentBrowserUnavailable
		}
		return r.agent, nil
	}
	if isExtensionTargetID(targetID) {
		if r.cfg.EngineMode == EngineModeAgentBrowser || r.extension == nil {
			return nil, ErrNoExtensionForTarget
		}
		return r.extension, nil
	}

	// Legacy IDs default to extension for backward compatibility, except when
	// the action obviously targets agent-browser sessions.
	if strings.HasPrefix(action, "session.") {
		if !r.agentBrowserAvailable() || r.agent == nil {
			return nil, ErrAgentBrowserUnavailable
		}
		return r.agent, nil
	}
	if r.cfg.EngineMode == EngineModeAgentBrowser {
		if !r.agentBrowserAvailable() || r.agent == nil {
			return nil, ErrAgentBrowserUnavailable
		}
		return r.agent, nil
	}
	if r.extension == nil {
		return nil, ErrNoExtensionForTarget
	}
	return r.extension, nil
}

func (r *EngineRouter) agentBrowserAvailable() bool {
	if !r.cfg.AgentBrowserEnabled {
		return false
	}
	return r.cfg.EngineMode == EngineModeHybrid || r.cfg.EngineMode == EngineModeAgentBrowser
}
