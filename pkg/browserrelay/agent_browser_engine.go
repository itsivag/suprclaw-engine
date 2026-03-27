package browserrelay

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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
		cmd.Stdin = bytes.NewReader(stdin)
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

type agentBrowserCommand []string

type agentBrowserRequestPlan struct {
	commands         []agentBrowserCommand
	finalize         func(stepResults []any, duration time.Duration) (any, error)
	cleanup          func()
	commandTimeoutMS int
}

type agentBrowserQueuedRequest struct {
	id         string
	action     string
	targetID   string
	enqueuedAt time.Time
	plan       agentBrowserRequestPlan
	responseCh chan agentBrowserQueuedResponse
}

type agentBrowserQueuedResponse struct {
	result any
	err    error
}

type agentBrowserSessionRuntime struct {
	session       Session
	headless      bool
	queue         chan *agentBrowserQueuedRequest
	closeCh       chan struct{}
	doneCh        chan struct{}
	closeOnce     sync.Once
	cancelErr     error
	disconnected  bool
	disconnectErr error
	inflight      bool
}

// AgentBrowserEngine executes browser actions via the local agent-browser CLI.
type AgentBrowserEngine struct {
	cfg    Config
	binary string
	runner agentBrowserRunner
	now    func() time.Time

	mu       sync.RWMutex
	sessions map[string]*agentBrowserSessionRuntime
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
		sessions: make(map[string]*agentBrowserSessionRuntime),
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
	sessions := e.detachAllSessions()
	for sessionID, rt := range sessions {
		e.stopRuntime(rt, ErrAgentBrowserQueueCanceled)
		ctx, cancel := e.commandContext(context.Background(), 0)
		_, _ = e.runAgentBrowserCommand(ctx, sessionID, rt.headless, []string{"close"}, nil)
		cancel()
	}
}

func (e *AgentBrowserEngine) ListTargets(_ context.Context) ([]Target, error) {
	e.mu.Lock()
	stale := e.evictIdleSessionsLocked()
	out := make([]Target, 0, len(e.sessions))
	for _, rt := range e.sessions {
		out = append(out, Target{
			ID:            rt.session.TargetID,
			Type:          "page",
			Title:         "agent-browser",
			Attached:      true,
			LastSeen:      rt.session.LastSeen,
			Source:        TargetSourceAgentBrowser,
			SessionID:     rt.session.ID,
			StreamWSURL:   rt.session.StreamWSURL,
			StreamPort:    rt.session.StreamPort,
			StreamEnabled: rt.session.StreamEnabled,
		})
	}
	e.mu.Unlock()
	e.closeStaleSessions(stale)
	return out, nil
}

func (e *AgentBrowserEngine) ListSessions(_ context.Context) ([]Session, error) {
	e.mu.Lock()
	stale := e.evictIdleSessionsLocked()
	out := make([]Session, 0, len(e.sessions))
	for _, rt := range e.sessions {
		out = append(out, rt.session)
	}
	e.mu.Unlock()
	e.closeStaleSessions(stale)
	return out, nil
}

func (e *AgentBrowserEngine) CreateSession(ctx context.Context, req ActionRequest) (any, error) {
	if !e.isAgentBrowserEnabled() {
		return nil, ErrAgentBrowserUnavailable
	}

	e.mu.Lock()
	stale := e.evictIdleSessionsLocked()
	if e.cfg.AgentBrowserMaxSessions > 0 && len(e.sessions) >= e.cfg.AgentBrowserMaxSessions {
		e.mu.Unlock()
		e.closeStaleSessions(stale)
		return nil, ErrMaxClientsReached
	}
	sessionID := uuid.NewString()
	targetID := BuildAgentBrowserTargetID(sessionID, "main")
	headless := e.cfg.AgentBrowserDefaultHeadless
	url := strings.TrimSpace(req.URL)
	if url == "" {
		url = "about:blank"
	}
	e.mu.Unlock()
	e.closeStaleSessions(stale)

	openCtx, cancelOpen := e.commandContext(ctx, req.TimeoutMS)
	_, err := e.runAgentBrowserCommand(openCtx, sessionID, headless, []string{"open", url}, nil)
	cancelOpen()
	if err != nil {
		return nil, err
	}

	streamEnabled := false
	streamPort := 0
	streamWSURL := ""
	if e.streamEnabledByConfig() {
		if err = e.ensureStreamEnabled(ctx, sessionID, headless); err != nil {
			return nil, err
		}
		streamEnabled, streamPort, streamWSURL = e.readStreamStatus(ctx, sessionID, headless)
	}

	now := e.now().UTC()
	rt := &agentBrowserSessionRuntime{
		session: Session{
			ID:            sessionID,
			TargetID:      targetID,
			Source:        TargetSourceAgentBrowser,
			CreatedAt:     now,
			LastSeen:      now,
			StreamWSURL:   streamWSURL,
			StreamPort:    streamPort,
			StreamEnabled: streamEnabled,
		},
		headless: headless,
		queue:    make(chan *agentBrowserQueuedRequest, defaultAgentBrowserQueueDepth),
		closeCh:  make(chan struct{}),
		doneCh:   make(chan struct{}),
	}

	e.mu.Lock()
	if e.cfg.AgentBrowserMaxSessions > 0 && len(e.sessions) >= e.cfg.AgentBrowserMaxSessions {
		e.mu.Unlock()
		closeCtx, cancelClose := e.commandContext(context.Background(), 0)
		_, _ = e.runAgentBrowserCommand(closeCtx, sessionID, headless, []string{"close"}, nil)
		cancelClose()
		return nil, ErrMaxClientsReached
	}
	e.sessions[sessionID] = rt
	e.mu.Unlock()

	go e.runSessionQueue(sessionID, rt)

	logger.DebugCF("browser-relay", "Created agent-browser session", map[string]any{
		"session_id":     sessionID,
		"target_id":      targetID,
		"stream_enabled": streamEnabled,
		"stream_port":    streamPort,
	})

	return map[string]any{
		"ok":             true,
		"session_id":     sessionID,
		"target_id":      targetID,
		"source":         TargetSourceAgentBrowser,
		"stream_enabled": streamEnabled,
		"stream_port":    streamPort,
		"stream_ws_url":  streamWSURL,
	}, nil
}

func (e *AgentBrowserEngine) CloseSession(ctx context.Context, sessionID string) error {
	sid := strings.TrimSpace(sessionID)
	if sid == "" {
		return fmt.Errorf("%w: missing session id", ErrInvalidTargetID)
	}

	e.mu.Lock()
	rt, ok := e.sessions[sid]
	if ok {
		delete(e.sessions, sid)
	}
	e.mu.Unlock()
	if !ok {
		return ErrSessionNotFound
	}

	e.stopRuntime(rt, ErrAgentBrowserQueueCanceled)
	closeCtx, cancel := e.commandContext(ctx, 0)
	_, err := e.runAgentBrowserCommand(closeCtx, sid, rt.headless, []string{"close"}, nil)
	cancel()
	if err != nil && !errors.Is(err, ErrAgentBrowserRuntimeDisconnected) {
		logger.WarnCF("browser-relay", "Failed to close agent-browser session cleanly", map[string]any{
			"session_id": sid,
			"error":      err.Error(),
		})
	}
	return nil
}

func (e *AgentBrowserEngine) ExecuteAction(ctx context.Context, action string, req ActionRequest) (any, error) {
	if !e.isAgentBrowserEnabled() {
		return nil, ErrAgentBrowserUnavailable
	}
	action = strings.TrimSpace(action)

	if action == "tabs.select" {
		return map[string]any{"ok": true, "source": TargetSourceAgentBrowser}, nil
	}

	e.mu.Lock()
	stale := e.evictIdleSessionsLocked()
	sessionID, _, ok := ParseAgentBrowserTargetID(req.TargetID)
	if !ok || strings.TrimSpace(sessionID) == "" {
		e.mu.Unlock()
		e.closeStaleSessions(stale)
		return nil, fmt.Errorf("%w: %s", ErrInvalidTargetID, req.TargetID)
	}
	rt, exists := e.sessions[sessionID]
	if !exists {
		e.mu.Unlock()
		e.closeStaleSessions(stale)
		return nil, ErrSessionNotFound
	}
	if rt.disconnected {
		err := rt.disconnectErr
		if err == nil {
			err = ErrAgentBrowserRuntimeDisconnected
		}
		e.mu.Unlock()
		e.closeStaleSessions(stale)
		return nil, err
	}
	rt.session.LastSeen = e.now().UTC()
	e.mu.Unlock()
	e.closeStaleSessions(stale)

	plan, err := e.buildRequestPlan(action, req)
	if err != nil {
		if plan.cleanup != nil {
			plan.cleanup()
		}
		return nil, err
	}

	queued := &agentBrowserQueuedRequest{
		id:         uuid.NewString(),
		action:     action,
		targetID:   req.TargetID,
		enqueuedAt: e.now(),
		plan:       plan,
		responseCh: make(chan agentBrowserQueuedResponse, 1),
	}

	select {
	case <-rt.closeCh:
		if queued.plan.cleanup != nil {
			queued.plan.cleanup()
		}
		err = rt.cancelErr
		if err == nil {
			err = ErrAgentBrowserQueueCanceled
		}
		return nil, err
	case rt.queue <- queued:
	default:
		if queued.plan.cleanup != nil {
			queued.plan.cleanup()
		}
		return nil, ErrRelayQueueFull
	}

	select {
	case resp := <-queued.responseCh:
		return resp.result, resp.err
	case <-ctx.Done():
		return nil, ErrRequestCanceled
	}
}

func (e *AgentBrowserEngine) runSessionQueue(sessionID string, rt *agentBrowserSessionRuntime) {
	defer close(rt.doneCh)
	var carry *agentBrowserQueuedRequest

	for {
		first := carry
		carry = nil
		if first == nil {
			select {
			case <-rt.closeCh:
				e.drainSessionQueue(rt, rt.cancelErr)
				return
			case first = <-rt.queue:
			}
		}
		if first == nil {
			continue
		}

		pending := []*agentBrowserQueuedRequest{first}
		commandCount := len(first.plan.commands)
		batchWindow, maxSteps := e.batchSettings()
		flushReason := "window_elapsed"
		timer := time.NewTimer(batchWindow)

	collect:
		for commandCount < maxSteps {
			select {
			case <-rt.closeCh:
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				e.failQueuedRequests(pending, rt.cancelErr)
				e.drainSessionQueue(rt, rt.cancelErr)
				return
			case next := <-rt.queue:
				if next == nil {
					continue
				}
				nextCount := len(next.plan.commands)
				if commandCount+nextCount > maxSteps {
					flushReason = "step_cap"
					carry = next
					break collect
				}
				pending = append(pending, next)
				commandCount += nextCount
			case <-timer.C:
				flushReason = "window_elapsed"
				break collect
			}
		}

		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}

		e.flushSessionRequests(sessionID, rt, pending, flushReason)
	}
}

func (e *AgentBrowserEngine) flushSessionRequests(
	sessionID string,
	rt *agentBrowserSessionRuntime,
	pending []*agentBrowserQueuedRequest,
	flushReason string,
) {
	if len(pending) == 0 {
		return
	}
	start := time.Now()
	e.setRuntimeInflight(sessionID, true)
	defer e.setRuntimeInflight(sessionID, false)

	commands := make([]agentBrowserCommand, 0)
	owner := make([]int, 0)
	maxTimeoutMS := 0
	for i, req := range pending {
		commands = append(commands, req.plan.commands...)
		for range req.plan.commands {
			owner = append(owner, i)
		}
		if req.plan.commandTimeoutMS > maxTimeoutMS {
			maxTimeoutMS = req.plan.commandTimeoutMS
		}
	}

	logger.DebugCF("browser-relay", "agent-browser batch flush", map[string]any{
		"session_id":    sessionID,
		"queue_depth":   len(rt.queue),
		"batch_size":    len(commands),
		"request_count": len(pending),
		"flush_reason":  flushReason,
	})

	maxSteps := e.batchMaxSteps()
	results := make([]any, 0, len(commands))
	offset := 0
	for offset < len(commands) {
		end := offset + maxSteps
		if end > len(commands) {
			end = len(commands)
		}
		chunk := commands[offset:end]
		chunkStart := time.Now()
		chunkResults, err := e.runAgentBrowserBatch(sessionID, rt.headless, chunk, maxTimeoutMS)
		chunkLatency := time.Since(chunkStart).Milliseconds()
		if err != nil {
			if errors.Is(err, ErrAgentBrowserRuntimeDisconnected) {
				e.markRuntimeDisconnected(sessionID, err)
			}
			logger.WarnCF("browser-relay", "agent-browser batch flush failed", map[string]any{
				"session_id":  sessionID,
				"queue_depth": len(rt.queue),
				"batch_size":  len(chunk),
				"latency_ms":  chunkLatency,
				"error_code":  e.errorCodeForLog(err),
				"error":       err.Error(),
			})
			e.failQueuedRequests(pending, err)
			return
		}
		results = append(results, chunkResults...)
		offset = end
	}

	perReqResults := make([][]any, len(pending))
	for i, raw := range results {
		idx := owner[i]
		perReqResults[idx] = append(perReqResults[idx], raw)
	}

	totalDuration := time.Since(start)
	for i, req := range pending {
		result, err := req.plan.finalize(perReqResults[i], totalDuration)
		if req.plan.cleanup != nil {
			req.plan.cleanup()
		}
		req.responseCh <- agentBrowserQueuedResponse{result: result, err: err}
	}

	logger.DebugCF("browser-relay", "agent-browser batch flush completed", map[string]any{
		"session_id":    sessionID,
		"queue_depth":   len(rt.queue),
		"batch_size":    len(commands),
		"request_count": len(pending),
		"latency_ms":    totalDuration.Milliseconds(),
		"flush_reason":  flushReason,
	})
}

func (e *AgentBrowserEngine) runAgentBrowserBatch(
	sessionID string,
	headless bool,
	commands []agentBrowserCommand,
	timeoutMS int,
) ([]any, error) {
	payload := make([][]string, 0, len(commands))
	for _, cmd := range commands {
		payload = append(payload, append([]string(nil), cmd...))
	}
	stdin, _ := json.Marshal(payload)
	ctx, cancel := e.commandContext(context.Background(), timeoutMS)
	defer cancel()

	decoded, err := e.runAgentBrowserCommand(ctx, sessionID, headless, []string{"batch"}, stdin)
	if err != nil {
		if errors.Is(err, ErrAgentBrowserRuntimeDisconnected) {
			return nil, err
		}
		return nil, fmt.Errorf("%w: %v", ErrAgentBrowserBatchFailed, err)
	}

	results := decodeAgentBrowserBatchResults(decoded, len(commands))
	return results, nil
}

func (e *AgentBrowserEngine) buildRequestPlan(action string, req ActionRequest) (agentBrowserRequestPlan, error) {
	switch action {
	case "navigate":
		if strings.TrimSpace(req.URL) == "" {
			return agentBrowserRequestPlan{}, fmt.Errorf("target_id and url are required")
		}
		return e.singleCommandPlan(action, req, agentBrowserCommand{"open", req.URL}), nil
	case "click":
		if strings.TrimSpace(req.Selector) == "" {
			return agentBrowserRequestPlan{}, fmt.Errorf("target_id and selector are required")
		}
		return e.singleCommandPlan(action, req, agentBrowserCommand{"click", req.Selector}), nil
	case "type":
		if strings.TrimSpace(req.Selector) == "" {
			return agentBrowserRequestPlan{}, fmt.Errorf("target_id and selector are required")
		}
		return e.singleCommandPlan(action, req, agentBrowserCommand{"fill", req.Selector, req.Text}), nil
	case "press":
		if strings.TrimSpace(req.Key) == "" {
			return agentBrowserRequestPlan{}, fmt.Errorf("target_id and key are required")
		}
		return e.singleCommandPlan(action, req, agentBrowserCommand{"press", req.Key}), nil
	case "wait":
		cmd, err := buildAgentBrowserWaitCommand(req)
		if err != nil {
			return agentBrowserRequestPlan{}, err
		}
		return e.singleCommandPlan(action, req, cmd), nil
	case "snapshot":
		cmd, err := e.buildAgentBrowserSnapshotCommand(req)
		if err != nil {
			return agentBrowserRequestPlan{}, err
		}
		return e.singleCommandPlan(action, req, cmd), nil
	case "screenshot":
		return e.screenshotPlan(req)
	case "batch":
		return e.batchPlan(req)
	default:
		return agentBrowserRequestPlan{}, fmt.Errorf("%w: %s", ErrUnsupportedAction, action)
	}
}

func (e *AgentBrowserEngine) singleCommandPlan(
	action string,
	req ActionRequest,
	command agentBrowserCommand,
) agentBrowserRequestPlan {
	return agentBrowserRequestPlan{
		commands: []agentBrowserCommand{command},
		finalize: func(stepResults []any, _ time.Duration) (any, error) {
			if len(stepResults) == 0 {
				return nil, fmt.Errorf("%w: empty batch result for %s", ErrAgentBrowserBatchFailed, action)
			}
			if failure, message := isAgentBrowserFailure(stepResults[0]); failure {
				return nil, fmt.Errorf("%w: %s", ErrAgentBrowserBatchFailed, message)
			}
			result := normalizeAgentBrowserStepResult(stepResults[0])
			if m, ok := result.(map[string]any); ok {
				if _, exists := m["source"]; !exists {
					m["source"] = TargetSourceAgentBrowser
				}
				return m, nil
			}
			return map[string]any{"ok": true, "result": result, "source": TargetSourceAgentBrowser}, nil
		},
		commandTimeoutMS: req.TimeoutMS,
	}
}

func (e *AgentBrowserEngine) screenshotPlan(req ActionRequest) (agentBrowserRequestPlan, error) {
	tmpDir, err := os.MkdirTemp("", "suprclaw-ab-shot-*")
	if err != nil {
		return agentBrowserRequestPlan{}, err
	}
	path := filepath.Join(tmpDir, "shot.png")
	cleanup := func() {
		_ = os.RemoveAll(tmpDir)
	}

	return agentBrowserRequestPlan{
		commands: []agentBrowserCommand{{"screenshot", path}},
		cleanup:  cleanup,
		finalize: func(stepResults []any, _ time.Duration) (any, error) {
			if len(stepResults) == 0 {
				return nil, fmt.Errorf("%w: empty screenshot result", ErrAgentBrowserBatchFailed)
			}
			if failure, message := isAgentBrowserFailure(stepResults[0]); failure {
				return nil, fmt.Errorf("%w: %s", ErrAgentBrowserBatchFailed, message)
			}
			if payload, ok := normalizeAgentBrowserStepResult(stepResults[0]).(map[string]any); ok {
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
		},
		commandTimeoutMS: req.TimeoutMS,
	}, nil
}

type compiledBatchStep struct {
	index       int
	action      string
	commandIdx  int
	preError    error
	isValidStep bool
}

func (e *AgentBrowserEngine) batchPlan(req ActionRequest) (agentBrowserRequestPlan, error) {
	if len(req.Steps) == 0 {
		return agentBrowserRequestPlan{}, fmt.Errorf("target_id and steps are required")
	}
	stopOnError := true
	if req.StopOnErrorSet {
		stopOnError = req.StopOnError
	}

	compiled := make([]compiledBatchStep, 0, len(req.Steps))
	commands := make([]agentBrowserCommand, 0, len(req.Steps))
	cleanupFns := make([]func(), 0)
	maxTimeoutMS := req.TimeoutMS

	for i, step := range req.Steps {
		stepAction := strings.TrimSpace(step.Action)
		stepReq := ActionRequest{
			TargetID:        req.TargetID,
			URL:             step.URL,
			Selector:        step.Selector,
			Text:            step.Text,
			Key:             step.Key,
			Expression:      step.Expression,
			WaitMode:        step.WaitMode,
			RefGeneration:   step.RefGeneration,
			SnapshotMode:    step.SnapshotMode,
			InteractiveOnly: step.InteractiveOnly,
			ScopeSelector:   step.ScopeSelector,
			Depth:           step.Depth,
			MaxNodes:        step.MaxNodes,
			MaxTextChars:    step.MaxTextChars,
			TimeoutMS:       step.TimeoutMS,
			IntervalMS:      step.IntervalMS,
		}
		if stepReq.TimeoutMS > maxTimeoutMS {
			maxTimeoutMS = stepReq.TimeoutMS
		}

		if stepAction == "" || stepAction == "batch" {
			stepErr := fmt.Errorf("%w: invalid batch step action %q", ErrUnsupportedAction, stepAction)
			compiled = append(compiled, compiledBatchStep{index: i, action: stepAction, preError: stepErr})
			if stopOnError {
				break
			}
			continue
		}
		if strings.TrimSpace(step.TargetID) != "" && strings.TrimSpace(step.TargetID) != strings.TrimSpace(req.TargetID) {
			stepErr := fmt.Errorf("step[%d] target_id must match batch target_id", i)
			compiled = append(compiled, compiledBatchStep{index: i, action: stepAction, preError: stepErr})
			if stopOnError {
				break
			}
			continue
		}

		plan, err := e.buildRequestPlan(stepAction, stepReq)
		if err != nil {
			compiled = append(compiled, compiledBatchStep{index: i, action: stepAction, preError: err})
			if plan.cleanup != nil {
				plan.cleanup()
			}
			if stopOnError {
				break
			}
			continue
		}
		commands = append(commands, plan.commands...)
		compiled = append(compiled, compiledBatchStep{
			index:       i,
			action:      stepAction,
			commandIdx:  len(commands) - 1,
			isValidStep: true,
		})
		if plan.cleanup != nil {
			cleanupFns = append(cleanupFns, plan.cleanup)
		}
	}

	cleanup := func() {
		for _, fn := range cleanupFns {
			fn()
		}
	}

	return agentBrowserRequestPlan{
		commands: commands,
		cleanup:  cleanup,
		finalize: func(stepResults []any, duration time.Duration) (any, error) {
			results := make([]map[string]any, 0, len(compiled))
			successCount := 0
			failedCount := 0
			for _, step := range compiled {
				stepStart := time.Now()
				if step.preError != nil {
					results = append(results, map[string]any{
						"index":       step.index,
						"action":      step.action,
						"ok":          false,
						"error":       step.preError.Error(),
						"duration_ms": time.Since(stepStart).Milliseconds(),
					})
					failedCount++
					if stopOnError {
						break
					}
					continue
				}

				if step.commandIdx < 0 || step.commandIdx >= len(stepResults) {
					results = append(results, map[string]any{
						"index":       step.index,
						"action":      step.action,
						"ok":          false,
						"error":       fmt.Sprintf("%s step result missing", step.action),
						"duration_ms": time.Since(stepStart).Milliseconds(),
					})
					failedCount++
					if stopOnError {
						break
					}
					continue
				}

				raw := stepResults[step.commandIdx]
				if failure, message := isAgentBrowserFailure(raw); failure {
					results = append(results, map[string]any{
						"index":       step.index,
						"action":      step.action,
						"ok":          false,
						"error":       message,
						"duration_ms": time.Since(stepStart).Milliseconds(),
					})
					failedCount++
					if stopOnError {
						break
					}
					continue
				}

				successCount++
				results = append(results, map[string]any{
					"index":       step.index,
					"action":      step.action,
					"ok":          true,
					"result":      normalizeAgentBrowserStepResult(raw),
					"duration_ms": time.Since(stepStart).Milliseconds(),
				})
			}

			return map[string]any{
				"ok":        failedCount == 0,
				"target_id": req.TargetID,
				"results":   results,
				"stats": map[string]any{
					"step_count":        len(req.Steps),
					"success_count":     successCount,
					"failed_count":      failedCount,
					"total_duration_ms": duration.Milliseconds(),
				},
				"source": TargetSourceAgentBrowser,
			}, nil
		},
		commandTimeoutMS: maxTimeoutMS,
	}, nil
}

func (e *AgentBrowserEngine) buildAgentBrowserSnapshotCommand(req ActionRequest) (agentBrowserCommand, error) {
	mode := strings.ToLower(strings.TrimSpace(req.SnapshotMode))
	if mode == "" {
		mode = strings.ToLower(strings.TrimSpace(e.snapshotDefaultMode()))
	}
	if mode != "" && mode != "compact" && mode != "full" {
		return nil, fmt.Errorf("%w: %s", ErrSnapshotModeUnsupported, mode)
	}

	interactive := e.snapshotInteractiveDefault()
	if req.InteractiveOnly != nil {
		interactive = *req.InteractiveOnly
	}

	cmd := agentBrowserCommand{"snapshot"}
	if interactive {
		cmd = append(cmd, "-i")
	}
	if mode == "compact" {
		cmd = append(cmd, "-c")
	}
	if req.Depth > 0 {
		cmd = append(cmd, "-d", strconv.Itoa(req.Depth))
	}
	if strings.TrimSpace(req.ScopeSelector) != "" {
		cmd = append(cmd, "-s", strings.TrimSpace(req.ScopeSelector))
	}
	return cmd, nil
}

func buildAgentBrowserWaitCommand(req ActionRequest) (agentBrowserCommand, error) {
	mode := strings.ToLower(strings.TrimSpace(req.WaitMode))
	switch mode {
	case "", "expression":
		if strings.TrimSpace(req.Expression) == "" {
			return nil, fmt.Errorf("target_id and expression are required")
		}
		return agentBrowserCommand{"wait", "--fn", strings.TrimSpace(req.Expression)}, nil
	case "selector":
		if strings.TrimSpace(req.Selector) == "" {
			return nil, fmt.Errorf("target_id and selector are required")
		}
		return agentBrowserCommand{"wait", strings.TrimSpace(req.Selector)}, nil
	case "navigation":
		return agentBrowserCommand{"wait", "--load", "load"}, nil
	case "network_idle":
		return agentBrowserCommand{"wait", "--load", "networkidle"}, nil
	default:
		return nil, fmt.Errorf("unsupported wait_mode %q", mode)
	}
}

func (e *AgentBrowserEngine) runAgentBrowserCommand(
	ctx context.Context,
	sessionID string,
	headless bool,
	actionArgs []string,
	stdin []byte,
) (any, error) {
	args := make([]string, 0, len(actionArgs)+4)
	args = append(args, "--json", "--session", sessionID)
	if !headless {
		args = append(args, "--headed")
	}
	args = append(args, actionArgs...)

	start := time.Now()
	stdout, stderr, err := e.runner.Run(ctx, e.binary, args, stdin)
	latency := time.Since(start)
	if err != nil {
		stderrLower := strings.ToLower(string(stderr) + " " + err.Error())
		logger.WarnCF("browser-relay", "agent-browser command failed", map[string]any{
			"session_id": sessionID,
			"args":       strings.Join(args, " "),
			"latency_ms": latency.Milliseconds(),
			"stderr":     string(stderr),
		})
		if strings.Contains(stderrLower, "not found") || strings.Contains(stderrLower, "executable file") {
			return nil, ErrAgentBrowserUnavailable
		}
		if isAgentBrowserDisconnectError(stderrLower) {
			return nil, ErrAgentBrowserRuntimeDisconnected
		}
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, ErrRequestTimeout
		}
		if errors.Is(ctx.Err(), context.Canceled) {
			return nil, ErrRequestCanceled
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

func decodeAgentBrowserBatchResults(decoded any, expected int) []any {
	var results []any
	switch t := decoded.(type) {
	case []any:
		results = append(results, t...)
	case map[string]any:
		if arr, ok := t["results"].([]any); ok {
			results = append(results, arr...)
		} else if arr, ok := t["data"].([]any); ok {
			results = append(results, arr...)
		} else if expected == 1 {
			results = append(results, t)
		}
	default:
		if expected == 1 {
			results = append(results, t)
		}
	}

	if len(results) < expected {
		for len(results) < expected {
			results = append(results, map[string]any{"ok": true})
		}
	}
	if len(results) > expected {
		results = results[:expected]
	}
	return results
}

func normalizeAgentBrowserStepResult(raw any) any {
	payload, ok := raw.(map[string]any)
	if !ok {
		return raw
	}
	if data, exists := payload["data"]; exists {
		return data
	}
	return payload
}

func isAgentBrowserFailure(raw any) (bool, string) {
	payload, ok := raw.(map[string]any)
	if !ok {
		return false, ""
	}
	if okVal, exists := payload["ok"]; exists {
		if b, ok := okVal.(bool); ok && !b {
			return true, pickAgentBrowserErrorMessage(payload)
		}
	}
	if successVal, exists := payload["success"]; exists {
		if b, ok := successVal.(bool); ok && !b {
			return true, pickAgentBrowserErrorMessage(payload)
		}
	}
	if errVal, exists := payload["error"]; exists {
		errMsg := strings.TrimSpace(fmt.Sprintf("%v", errVal))
		if errMsg != "" && errMsg != "<nil>" {
			return true, errMsg
		}
	}
	return false, ""
}

func pickAgentBrowserErrorMessage(payload map[string]any) string {
	if errVal, exists := payload["error"]; exists {
		errMsg := strings.TrimSpace(fmt.Sprintf("%v", errVal))
		if errMsg != "" {
			return errMsg
		}
	}
	if msg, exists := payload["message"]; exists {
		msgText := strings.TrimSpace(fmt.Sprintf("%v", msg))
		if msgText != "" {
			return msgText
		}
	}
	return "agent-browser reported command failure"
}

func isAgentBrowserDisconnectError(s string) bool {
	if s == "" {
		return false
	}
	patterns := []string{
		"session not found",
		"no active session",
		"stream closed",
		"connection reset",
		"connection refused",
		"target closed",
		"browser has disconnected",
		"daemon not running",
	}
	for _, p := range patterns {
		if strings.Contains(s, p) {
			return true
		}
	}
	return false
}

func (e *AgentBrowserEngine) ensureStreamEnabled(ctx context.Context, sessionID string, headless bool) error {
	args := []string{"stream", "enable"}
	if e.streamPort() > 0 {
		args = append(args, "--port", strconv.Itoa(e.streamPort()))
	}
	streamCtx, cancel := e.commandContext(ctx, 0)
	_, err := e.runAgentBrowserCommand(streamCtx, sessionID, headless, args, nil)
	cancel()
	if err != nil {
		if errors.Is(err, ErrAgentBrowserRuntimeDisconnected) {
			return err
		}
		logger.WarnCF("browser-relay", "agent-browser stream enable failed", map[string]any{
			"session_id": sessionID,
			"error":      err.Error(),
		})
	}
	return nil
}

func (e *AgentBrowserEngine) readStreamStatus(
	ctx context.Context,
	sessionID string,
	headless bool,
) (bool, int, string) {
	streamCtx, cancel := e.commandContext(ctx, 0)
	defer cancel()
	status, err := e.runAgentBrowserCommand(streamCtx, sessionID, headless, []string{"stream", "status"}, nil)
	if err != nil {
		logger.WarnCF("browser-relay", "agent-browser stream status failed", map[string]any{
			"session_id": sessionID,
			"error":      err.Error(),
		})
		return false, 0, ""
	}
	return parseStreamStatus(status)
}

func parseStreamStatus(raw any) (bool, int, string) {
	payload, ok := raw.(map[string]any)
	if !ok {
		return false, 0, ""
	}
	enabled := boolFromMap(payload, "enabled") || boolFromMap(payload, "stream_enabled")
	port := intFromMap(payload, "port")
	if port == 0 {
		port = intFromMap(payload, "stream_port")
	}
	wsURL := stringFromMap(payload, "ws_url")
	if wsURL == "" {
		wsURL = stringFromMap(payload, "stream_ws_url")
	}
	if wsURL == "" && port > 0 {
		wsURL = fmt.Sprintf("ws://127.0.0.1:%d", port)
	}
	return enabled, port, wsURL
}

func boolFromMap(m map[string]any, key string) bool {
	v, ok := m[key]
	if !ok {
		return false
	}
	b, ok := v.(bool)
	return ok && b
}

func intFromMap(m map[string]any, key string) int {
	v, ok := m[key]
	if !ok {
		return 0
	}
	switch t := v.(type) {
	case int:
		return t
	case int64:
		return int(t)
	case float64:
		return int(t)
	case string:
		n, _ := strconv.Atoi(strings.TrimSpace(t))
		return n
	default:
		return 0
	}
}

func stringFromMap(m map[string]any, key string) string {
	v, ok := m[key]
	if !ok {
		return ""
	}
	return strings.TrimSpace(fmt.Sprintf("%v", v))
}

func (e *AgentBrowserEngine) closeStaleSessions(stale map[string]*agentBrowserSessionRuntime) {
	for sessionID, rt := range stale {
		e.stopRuntime(rt, ErrAgentBrowserQueueCanceled)
		ctx, cancel := e.commandContext(context.Background(), 0)
		_, _ = e.runAgentBrowserCommand(ctx, sessionID, rt.headless, []string{"close"}, nil)
		cancel()
	}
}

func (e *AgentBrowserEngine) stopRuntime(rt *agentBrowserSessionRuntime, err error) {
	if rt == nil {
		return
	}
	rt.closeOnce.Do(func() {
		if err == nil {
			err = ErrAgentBrowserQueueCanceled
		}
		rt.cancelErr = err
		close(rt.closeCh)
	})
	select {
	case <-rt.doneCh:
	case <-time.After(2 * time.Second):
	}
}

func (e *AgentBrowserEngine) failQueuedRequests(requests []*agentBrowserQueuedRequest, err error) {
	if err == nil {
		err = ErrAgentBrowserQueueCanceled
	}
	for _, req := range requests {
		if req == nil {
			continue
		}
		if req.plan.cleanup != nil {
			req.plan.cleanup()
		}
		req.responseCh <- agentBrowserQueuedResponse{err: err}
	}
}

func (e *AgentBrowserEngine) drainSessionQueue(rt *agentBrowserSessionRuntime, err error) {
	if err == nil {
		err = ErrAgentBrowserQueueCanceled
	}
	for {
		select {
		case req := <-rt.queue:
			if req == nil {
				continue
			}
			if req.plan.cleanup != nil {
				req.plan.cleanup()
			}
			req.responseCh <- agentBrowserQueuedResponse{err: err}
		default:
			return
		}
	}
}

func (e *AgentBrowserEngine) markRuntimeDisconnected(sessionID string, reason error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	rt, ok := e.sessions[sessionID]
	if !ok {
		return
	}
	rt.disconnected = true
	if reason == nil {
		reason = ErrAgentBrowserRuntimeDisconnected
	}
	rt.disconnectErr = reason
	rt.cancelErr = ErrAgentBrowserRuntimeDisconnected
	rt.closeOnce.Do(func() {
		close(rt.closeCh)
	})
}

func (e *AgentBrowserEngine) setRuntimeInflight(sessionID string, inflight bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if rt, ok := e.sessions[sessionID]; ok {
		rt.inflight = inflight
	}
}

func (e *AgentBrowserEngine) evictIdleSessionsLocked() map[string]*agentBrowserSessionRuntime {
	timeout := e.cfg.AgentBrowserIdleTimeoutSec
	if timeout <= 0 {
		return nil
	}
	deadline := e.now().Add(-time.Duration(timeout) * time.Second)
	stale := make(map[string]*agentBrowserSessionRuntime)
	for id, rt := range e.sessions {
		if rt.session.LastSeen.Before(deadline) {
			stale[id] = rt
			delete(e.sessions, id)
		}
	}
	return stale
}

func (e *AgentBrowserEngine) detachAllSessions() map[string]*agentBrowserSessionRuntime {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make(map[string]*agentBrowserSessionRuntime, len(e.sessions))
	for id, rt := range e.sessions {
		out[id] = rt
	}
	e.sessions = make(map[string]*agentBrowserSessionRuntime)
	return out
}

func (e *AgentBrowserEngine) batchSettings() (time.Duration, int) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	window := e.cfg.AgentBrowserBatchWindowMS
	if window <= 0 {
		window = defaultAgentBrowserBatchWindowMS
	}
	steps := e.cfg.AgentBrowserBatchMaxSteps
	if steps <= 0 {
		steps = defaultAgentBrowserBatchMaxSteps
	}
	return time.Duration(window) * time.Millisecond, steps
}

func (e *AgentBrowserEngine) batchMaxSteps() int {
	_, steps := e.batchSettings()
	return steps
}

func (e *AgentBrowserEngine) commandContext(parent context.Context, timeoutMS int) (context.Context, context.CancelFunc) {
	e.mu.RLock()
	defaultTimeout := e.cfg.AgentBrowserRuntimeCommandTimeoutMS
	e.mu.RUnlock()

	if timeoutMS <= 0 {
		timeoutMS = defaultTimeout
	}
	if timeoutMS <= 0 {
		timeoutMS = defaultAgentBrowserRuntimeTimeoutMS
	}
	return context.WithTimeout(parent, time.Duration(timeoutMS)*time.Millisecond)
}

func (e *AgentBrowserEngine) isAgentBrowserEnabled() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.cfg.AgentBrowserEnabled
}

func (e *AgentBrowserEngine) streamEnabledByConfig() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.cfg.AgentBrowserStreamEnabled
}

func (e *AgentBrowserEngine) streamPort() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.cfg.AgentBrowserStreamPort
}

func (e *AgentBrowserEngine) snapshotDefaultMode() string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.cfg.SnapshotDefaultMode
}

func (e *AgentBrowserEngine) snapshotInteractiveDefault() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.cfg.SnapshotInteractiveOnly
}

func (e *AgentBrowserEngine) errorCodeForLog(err error) string {
	switch {
	case errors.Is(err, ErrAgentBrowserRuntimeDisconnected):
		return "agent_browser_runtime_disconnected"
	case errors.Is(err, ErrAgentBrowserQueueCanceled):
		return "agent_browser_queue_canceled"
	case errors.Is(err, ErrAgentBrowserBatchFailed):
		return "agent_browser_batch_failed"
	case errors.Is(err, ErrAgentBrowserUnavailable):
		return "agent_browser_unavailable"
	default:
		return "relay_internal_error"
	}
}
