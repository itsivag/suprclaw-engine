package browserrelay

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/itsivag/suprclaw/pkg/logger"
)

type agentBrowserRunner interface {
	Run(ctx context.Context, binary string, args []string, stdin []byte) ([]byte, []byte, error)
}

type osAgentBrowserRunner struct{}

func (r *osAgentBrowserRunner) Run(
	ctx context.Context,
	binary string,
	args []string,
	stdin []byte,
) ([]byte, []byte, error) {
	cmd := exec.CommandContext(ctx, binary, args...)
	if len(stdin) > 0 {
		cmd.Stdin = strings.NewReader(string(stdin))
	}
	stdout, err := cmd.Output()
	if err == nil {
		return stdout, nil, nil
	}
	var stderr []byte
	if ee, ok := err.(*exec.ExitError); ok {
		stderr = append([]byte(nil), ee.Stderr...)
	}
	return stdout, stderr, err
}

type agentBrowserSession struct {
	session  Session
	headless bool
}

// AgentBrowserEngine executes browser actions via the local agent-browser CLI.
type AgentBrowserEngine struct {
	cfg    Config
	binary string
	runner agentBrowserRunner
	now    func() time.Time

	mu       sync.Mutex
	sessions map[string]*agentBrowserSession
}

func NewAgentBrowserEngine(cfg Config, runner agentBrowserRunner) *AgentBrowserEngine {
	if runner == nil {
		runner = &osAgentBrowserRunner{}
	}
	binary := strings.TrimSpace(cfg.AgentBrowserBinary)
	if binary == "" {
		binary = "agent-browser"
	}
	return &AgentBrowserEngine{
		cfg:      normalizeConfig(cfg),
		binary:   binary,
		runner:   runner,
		now:      time.Now,
		sessions: make(map[string]*agentBrowserSession),
	}
}

func (e *AgentBrowserEngine) ApplyConfig(cfg Config) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.cfg = normalizeConfig(cfg)
	if strings.TrimSpace(cfg.AgentBrowserBinary) != "" {
		e.binary = strings.TrimSpace(cfg.AgentBrowserBinary)
	}
}

func (e *AgentBrowserEngine) Close() {
	e.mu.Lock()
	defer e.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for sessionID, s := range e.sessions {
		_, _ = e.runJSON(ctx, sessionID, s.headless, "close")
		delete(e.sessions, sessionID)
	}
}

func (e *AgentBrowserEngine) ListTargets(_ context.Context) ([]Target, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.evictIdleSessionsLocked()
	out := make([]Target, 0, len(e.sessions))
	for _, s := range e.sessions {
		out = append(out, Target{
			ID:       s.session.TargetID,
			Type:     "page",
			Title:    "agent-browser",
			Attached: true,
			LastSeen: s.session.LastSeen,
			Source:   TargetSourceAgentBrowser,
		})
	}
	return out, nil
}

func (e *AgentBrowserEngine) ListSessions(_ context.Context) ([]Session, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.evictIdleSessionsLocked()
	out := make([]Session, 0, len(e.sessions))
	for _, s := range e.sessions {
		out = append(out, s.session)
	}
	return out, nil
}

func (e *AgentBrowserEngine) CreateSession(ctx context.Context, req ActionRequest) (any, error) {
	if !e.cfg.AgentBrowserEnabled {
		return nil, ErrAgentBrowserUnavailable
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.evictIdleSessionsLocked()
	if e.cfg.AgentBrowserMaxSessions > 0 && len(e.sessions) >= e.cfg.AgentBrowserMaxSessions {
		return nil, ErrMaxClientsReached
	}
	sessionID := uuid.NewString()
	targetID := BuildAgentBrowserTargetID(sessionID, "main")
	headless := e.cfg.AgentBrowserDefaultHeadless
	url := strings.TrimSpace(req.URL)
	if url == "" {
		url = "about:blank"
	}
	if _, err := e.runJSON(ctx, sessionID, headless, "open", url); err != nil {
		return nil, err
	}

	now := e.now().UTC()
	e.sessions[sessionID] = &agentBrowserSession{
		session: Session{
			ID:        sessionID,
			TargetID:  targetID,
			Source:    TargetSourceAgentBrowser,
			CreatedAt: now,
			LastSeen:  now,
		},
		headless: headless,
	}
	logger.DebugCF("browser-relay", "Created agent-browser session", map[string]any{
		"session_id": sessionID,
		"target_id":  targetID,
	})
	return map[string]any{
		"ok":         true,
		"session_id": sessionID,
		"target_id":  targetID,
		"source":     TargetSourceAgentBrowser,
	}, nil
}

func (e *AgentBrowserEngine) CloseSession(ctx context.Context, sessionID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	sid := strings.TrimSpace(sessionID)
	if sid == "" {
		return fmt.Errorf("%w: missing session id", ErrInvalidTargetID)
	}
	s, ok := e.sessions[sid]
	if !ok {
		return ErrSessionNotFound
	}
	_, err := e.runJSON(ctx, sid, s.headless, "close")
	delete(e.sessions, sid)
	if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		logger.WarnCF("browser-relay", "Failed to close agent-browser session cleanly", map[string]any{
			"session_id": sid,
			"error":      err.Error(),
		})
	}
	return nil
}

func (e *AgentBrowserEngine) ExecuteAction(ctx context.Context, action string, req ActionRequest) (any, error) {
	if !e.cfg.AgentBrowserEnabled {
		return nil, ErrAgentBrowserUnavailable
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.evictIdleSessionsLocked()
	sessionID, _, ok := ParseAgentBrowserTargetID(req.TargetID)
	if !ok || strings.TrimSpace(sessionID) == "" {
		return nil, fmt.Errorf("%w: %s", ErrInvalidTargetID, req.TargetID)
	}
	s, exists := e.sessions[sessionID]
	if !exists {
		return nil, ErrSessionNotFound
	}
	s.session.LastSeen = e.now().UTC()

	switch action {
	case "tabs.select":
		return map[string]any{"ok": true, "source": TargetSourceAgentBrowser}, nil
	case "navigate":
		if strings.TrimSpace(req.URL) == "" {
			return nil, fmt.Errorf("target_id and url are required")
		}
		return e.runJSON(ctx, sessionID, s.headless, "open", req.URL)
	case "click":
		if strings.TrimSpace(req.Selector) == "" {
			return nil, fmt.Errorf("target_id and selector are required")
		}
		return e.runJSON(ctx, sessionID, s.headless, "click", req.Selector)
	case "type":
		if strings.TrimSpace(req.Selector) == "" {
			return nil, fmt.Errorf("target_id and selector are required")
		}
		return e.runJSON(ctx, sessionID, s.headless, "fill", req.Selector, req.Text)
	case "press":
		if strings.TrimSpace(req.Key) == "" {
			return nil, fmt.Errorf("target_id and key are required")
		}
		return e.runJSON(ctx, sessionID, s.headless, "press", req.Key)
	case "snapshot":
		return e.runJSON(ctx, sessionID, s.headless, "snapshot")
	case "wait":
		if strings.TrimSpace(req.Expression) == "" {
			return nil, fmt.Errorf("target_id and expression are required")
		}
		return e.runJSON(ctx, sessionID, s.headless, "wait", "--fn", req.Expression)
	case "screenshot":
		return e.captureScreenshot(ctx, sessionID, s.headless)
	default:
		return nil, fmt.Errorf("%w: %s", ErrUnsupportedAction, action)
	}
}

func (e *AgentBrowserEngine) captureScreenshot(ctx context.Context, sessionID string, headless bool) (any, error) {
	tmpDir, err := os.MkdirTemp("", "suprclaw-ab-shot-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmpDir)
	path := filepath.Join(tmpDir, "shot.png")

	resp, err := e.runJSON(ctx, sessionID, headless, "screenshot", path)
	if err != nil {
		return nil, err
	}

	if payload, ok := resp.(map[string]any); ok {
		if explicitPath, _ := payload["path"].(string); strings.TrimSpace(explicitPath) != "" {
			path = explicitPath
		}
	}
	data, readErr := os.ReadFile(path)
	if readErr != nil {
		return nil, readErr
	}
	return map[string]any{
		"data":   base64.StdEncoding.EncodeToString(data),
		"source": TargetSourceAgentBrowser,
	}, nil
}

func (e *AgentBrowserEngine) runJSON(
	ctx context.Context,
	sessionID string,
	headless bool,
	actionArgs ...string,
) (any, error) {
	args := make([]string, 0, len(actionArgs)+4)
	args = append(args, "--json", "--session", sessionID)
	if !headless {
		args = append(args, "--headed")
	}
	args = append(args, actionArgs...)
	start := time.Now()
	stdout, stderr, err := e.runner.Run(ctx, e.binary, args, nil)
	latency := time.Since(start)
	if err != nil {
		logger.WarnCF("browser-relay", "agent-browser command failed", map[string]any{
			"session_id": sessionID,
			"args":       strings.Join(args, " "),
			"latency_ms": latency.Milliseconds(),
			"stderr":     string(stderr),
		})
		if strings.Contains(strings.ToLower(string(stderr)), "not found") ||
			strings.Contains(strings.ToLower(err.Error()), "executable file not found") {
			return nil, ErrAgentBrowserUnavailable
		}
		return nil, fmt.Errorf("agent-browser command failed: %w", err)
	}

	logger.DebugCF("browser-relay", "agent-browser command completed", map[string]any{
		"session_id": sessionID,
		"args":       strings.Join(args, " "),
		"latency_ms": latency.Milliseconds(),
	})

	out := strings.TrimSpace(string(stdout))
	if out == "" {
		return map[string]any{"ok": true}, nil
	}
	var decoded any
	if unmarshalErr := json.Unmarshal([]byte(out), &decoded); unmarshalErr != nil {
		return map[string]any{"output": out}, nil
	}
	return decoded, nil
}

func (e *AgentBrowserEngine) evictIdleSessionsLocked() {
	timeout := e.cfg.AgentBrowserIdleTimeoutSec
	if timeout <= 0 {
		return
	}
	deadline := e.now().Add(-time.Duration(timeout) * time.Second)
	for id, s := range e.sessions {
		if s.session.LastSeen.Before(deadline) {
			delete(e.sessions, id)
		}
	}
}
