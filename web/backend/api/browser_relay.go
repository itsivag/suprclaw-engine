package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"github.com/itsivag/suprclaw/pkg/browserrelay"
	"github.com/itsivag/suprclaw/pkg/config"
	"github.com/itsivag/suprclaw/pkg/logger"
)

const (
	defaultBrowserRelayHost           = "127.0.0.1"
	defaultBrowserRelayPort           = 18792
	defaultBrowserRelayMaxClients     = 16
	defaultBrowserRelayIdleTimeoutSec = 60
	defaultBrowserRelayTimeout        = 15 * time.Second
	defaultRelayRequestCacheTTL       = 5 * time.Minute
	defaultRelayRequestCacheMax       = 512
)

var browserRelayUpgrader = websocket.Upgrader{
	CheckOrigin: func(_ *http.Request) bool {
		return true
	},
}

func (h *Handler) registerBrowserRelayRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/browser-relay/status", h.handleBrowserRelayStatus)
	mux.HandleFunc("POST /api/browser-relay/setup", h.handleBrowserRelaySetup)
	mux.HandleFunc("GET /api/browser-relay/token", h.handleBrowserRelayToken)
	mux.HandleFunc("POST /api/browser-relay/token", h.handleBrowserRelayRegenerateToken)
	mux.HandleFunc("POST /api/browser-relay/pairing", h.handleBrowserRelayPairing)
	mux.HandleFunc("POST /api/browser-relay/pairing/claim", h.handleBrowserRelayPairingClaim)
	mux.HandleFunc("GET /api/browser-relay/pairing/qr/{code}", h.handleBrowserRelayPairingQR)
	mux.HandleFunc("GET /api/browser-relay/session/state", h.handleBrowserRelaySessionState)
	mux.HandleFunc("POST /api/browser-relay/session/refresh", h.handleBrowserRelaySessionRefresh)
	mux.HandleFunc("POST /api/browser-relay/session/stop", h.handleBrowserRelaySessionStop)
	mux.HandleFunc("GET /api/browser-relay/targets", h.handleBrowserRelayTargets)
	mux.HandleFunc("POST /api/browser-relay/actions", h.handleBrowserRelayActionV2)

	mux.HandleFunc("GET /browser-relay/extension", h.handleBrowserRelayExtensionWS)
	mux.HandleFunc("GET /browser-relay/cdp/{targetId}", h.handleBrowserRelayCDPWS)
}

func (h *Handler) handleBrowserRelayStatus(w http.ResponseWriter, r *http.Request) {
	cfg, err := config.LoadConfig(h.configPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to load config: %v", err), http.StatusInternalServerError)
		return
	}

	relayCfg := normalizeBrowserRelayConfig(cfg.Tools.BrowserRelay)
	manager := h.browserRelayManager(cfg)
	status := manager.Status()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":                        status,
		"enabled":                       relayCfg.Enabled,
		"host":                          relayCfg.Host,
		"port":                          relayCfg.Port,
		"compat_openclaw":               relayCfg.CompatOpenClaw,
		"allow_token_query":             relayCfg.AllowTokenQuery,
		"engine_mode":                   relayCfg.EngineMode,
		"agent_browser_enabled":         relayCfg.AgentBrowserEnabled,
		"agent_browser_binary":          relayCfg.AgentBrowserBinary,
		"agent_browser_batch_window_ms": relayCfg.AgentBrowserBatchWindowMS,
		"agent_browser_batch_max_steps": relayCfg.AgentBrowserBatchMaxSteps,
		"agent_browser_stream_enabled":  relayCfg.AgentBrowserStreamEnabled,
		"agent_browser_stream_port":     relayCfg.AgentBrowserStreamPort,
		"agent_browser_runtime_command_timeout_ms": relayCfg.AgentBrowserRuntimeCommandTimeoutMS,
		"snapshot_default_mode":                    relayCfg.SnapshotDefaultMode,
		"snapshot_max_payload_bytes":               relayCfg.SnapshotMaxPayloadBytes,
		"snapshot_max_nodes":                       relayCfg.SnapshotMaxNodes,
		"snapshot_max_text_chars":                  relayCfg.SnapshotMaxTextChars,
		"snapshot_max_depth":                       relayCfg.SnapshotMaxDepth,
		"snapshot_interactive_only_default":        relayCfg.SnapshotInteractiveOnly,
		"snapshot_ref_ttl_sec":                     relayCfg.SnapshotRefTTLSec,
		"snapshot_max_generations":                 relayCfg.SnapshotMaxGenerations,
		"snapshot_allow_full_tree":                 relayCfg.SnapshotAllowFullTree,
		"extension_ws_url":                         h.browserRelayExtensionURL(r, relayCfg),
		"cdp_ws_url_template":                      h.browserRelayCDPTemplateURL(r, relayCfg),
		"configured_relay_port":                    relayCfg.Port,
	})
}

func (h *Handler) handleBrowserRelaySetup(w http.ResponseWriter, r *http.Request) {
	cfg, err := config.LoadConfig(h.configPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to load config: %v", err), http.StatusInternalServerError)
		return
	}

	if token := strings.TrimSpace(cfg.Tools.BrowserRelay.Token); token != "" {
		if !h.isBrowserRelayBootstrapAuthorized(r, cfg) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
	}
	relayCfg, changed, err := h.browserRelayEnsureConfigured(cfg)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"enabled":          relayCfg.Enabled,
		"changed":          changed,
		"token":            relayCfg.Token,
		"extension_ws_url": h.browserRelayExtensionURL(r, relayCfg),
		"cdp_ws_url":       h.browserRelayCDPTemplateURL(r, relayCfg),
		"host":             relayCfg.Host,
		"port":             relayCfg.Port,
	})
}

func (h *Handler) handleBrowserRelayToken(w http.ResponseWriter, r *http.Request) {
	cfg, err := config.LoadConfig(h.configPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to load config: %v", err), http.StatusInternalServerError)
		return
	}
	if strings.TrimSpace(cfg.Tools.BrowserRelay.Token) != "" && !h.isBrowserRelayBootstrapAuthorized(r, cfg) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	relayCfg := normalizeBrowserRelayConfig(cfg.Tools.BrowserRelay)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"enabled":          relayCfg.Enabled,
		"token":            relayCfg.Token,
		"extension_ws_url": h.browserRelayExtensionURL(r, relayCfg),
	})
}

func (h *Handler) handleBrowserRelayRegenerateToken(w http.ResponseWriter, r *http.Request) {
	cfg, err := config.LoadConfig(h.configPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to load config: %v", err), http.StatusInternalServerError)
		return
	}
	if strings.TrimSpace(cfg.Tools.BrowserRelay.Token) != "" && !h.isBrowserRelayHTTPAuthorized(r, cfg) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	relayCfg := normalizeBrowserRelayConfig(cfg.Tools.BrowserRelay)
	relayCfg.Token = generateSecureToken()
	cfg.Tools.BrowserRelay = relayCfg
	if err = config.SaveConfig(h.configPath, cfg); err != nil {
		http.Error(w, fmt.Sprintf("Failed to save config: %v", err), http.StatusInternalServerError)
		return
	}

	manager := h.browserRelayManager(cfg)
	manager.ApplyConfig(browserRelayConfigFromConfig(cfg))

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"token":            relayCfg.Token,
		"extension_ws_url": h.browserRelayExtensionURL(r, relayCfg),
	})
}

func (h *Handler) handleBrowserRelayTargets(w http.ResponseWriter, r *http.Request) {
	cfg, relayCfg, manager, ok := h.requireBrowserRelayEnabled(w, r, true, true)
	if !ok {
		return
	}
	_ = manager

	router := h.browserRelayRouter(cfg)
	result, err := router.ExecuteAction(r.Context(), "tabs.list", browserrelay.ActionRequest{})
	if err != nil {
		http.Error(w, err.Error(), mapBrowserRelayError(err))
		return
	}
	payload, _ := result.(map[string]any)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"targets": payload["targets"],
		"compat":  relayCfg.CompatOpenClaw,
	})
}

type relayRetryClass string

const (
	retryClassNever            relayRetryClass = "never"
	retryClassSafeImmediate    relayRetryClass = "safe_immediate"
	retryClassSafeBackoff      relayRetryClass = "safe_backoff"
	retryClassAfterStateChange relayRetryClass = "after_state_change"
)

type relayExecutionPolicy struct {
	StopOnError *bool `json:"stop_on_error,omitempty"`
	TimeoutMS   int   `json:"timeout_ms,omitempty"`
}

type relayActionV2Step struct {
	Action string         `json:"action"`
	Args   map[string]any `json:"args,omitempty"`
}

type relayActionV2Request struct {
	RequestID       string               `json:"request_id"`
	Target          string               `json:"target"`
	Action          string               `json:"action,omitempty"`
	Args            map[string]any       `json:"args,omitempty"`
	Steps           []relayActionV2Step  `json:"steps,omitempty"`
	ExecutionPolicy relayExecutionPolicy `json:"execution_policy,omitempty"`
}

type relayActionTiming struct {
	LatencyMS  int64 `json:"latency_ms"`
	QueueDepth int   `json:"queue_depth"`
	Cached     bool  `json:"cached"`
}

type relayActionV2Response struct {
	RequestID    string            `json:"request_id"`
	OK           bool              `json:"ok"`
	Result       any               `json:"result,omitempty"`
	ErrorCode    string            `json:"error_code,omitempty"`
	ErrorMessage string            `json:"error_message,omitempty"`
	RetryClass   relayRetryClass   `json:"retry_class,omitempty"`
	TraceID      string            `json:"trace_id"`
	Timing       relayActionTiming `json:"timing"`
}

type relayRequestCacheMeta struct {
	CreatedAt   time.Time
	Fingerprint string
	HTTPStatus  int
}

type relayErrorInfo struct {
	HTTPStatus int
	Code       string
	RetryClass relayRetryClass
}

func (h *Handler) handleBrowserRelayActionV2(w http.ResponseWriter, r *http.Request) {
	cfg, _, manager, ok := h.requireBrowserRelayEnabled(w, r, true, true)
	if !ok {
		return
	}
	router := h.browserRelayRouter(cfg)

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	var req relayActionV2Request
	if len(body) == 0 {
		h.writeRelayV2Error(w, relayActionV2Response{
			RequestID:    "",
			OK:           false,
			ErrorCode:    "invalid_request",
			ErrorMessage: "empty request body",
			RetryClass:   retryClassNever,
			TraceID:      uuid.NewString(),
			Timing:       relayActionTiming{},
		}, http.StatusBadRequest)
		return
	}
	if err = json.Unmarshal(body, &req); err != nil {
		h.writeRelayV2Error(w, relayActionV2Response{
			RequestID:    "",
			OK:           false,
			ErrorCode:    "invalid_json",
			ErrorMessage: fmt.Sprintf("Invalid JSON: %v", err),
			RetryClass:   retryClassNever,
			TraceID:      uuid.NewString(),
			Timing:       relayActionTiming{},
		}, http.StatusBadRequest)
		return
	}

	traceID := uuid.NewString()
	if strings.TrimSpace(req.RequestID) == "" {
		req.RequestID = uuid.NewString()
	}
	req.RequestID = strings.TrimSpace(req.RequestID)

	if cached, status, ok := h.relayCachedResponse(req); ok {
		cached.TraceID = traceID
		cached.Timing.Cached = true
		h.writeRelayV2(w, cached, status)
		return
	}

	action, payload, validationErr := h.validateAndConvertRelayActionV2(req)
	if validationErr != nil {
		info := classifyRelayError(validationErr)
		errorCode := "validation_error"
		retryClass := retryClassNever
		httpStatus := http.StatusBadRequest
		if errors.Is(validationErr, browserrelay.ErrSnapshotRefRequired) {
			errorCode = info.Code
			retryClass = info.RetryClass
		}
		resp := relayActionV2Response{
			RequestID:    req.RequestID,
			OK:           false,
			ErrorCode:    errorCode,
			ErrorMessage: validationErr.Error(),
			RetryClass:   retryClass,
			TraceID:      traceID,
		}
		h.cacheRelayResponse(req, resp, httpStatus)
		h.writeRelayV2Error(w, resp, httpStatus)
		return
	}

	timeout := defaultBrowserRelayTimeout
	if req.ExecutionPolicy.TimeoutMS > 0 {
		timeout = time.Duration(req.ExecutionPolicy.TimeoutMS) * time.Millisecond
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	start := time.Now()
	source := relayEngineFromAction(action, payload.TargetID)
	queueDepth := manager.QueueDepth(browserrelayExtensionRawTarget(payload.TargetID))
	if isBrowserRelayDebugTrace() {
		logger.DebugCF("browser-relay", "relay v2 request", map[string]any{
			"trace_id":    traceID,
			"request_id":  req.RequestID,
			"target":      req.Target,
			"action":      action,
			"engine":      source,
			"queue_depth": queueDepth,
		})
	}

	result, execErr := router.ExecuteAction(ctx, action, payload)
	latency := time.Since(start).Milliseconds()

	if execErr != nil {
		info := classifyRelayError(execErr)
		resp := relayActionV2Response{
			RequestID:    req.RequestID,
			OK:           false,
			ErrorCode:    info.Code,
			ErrorMessage: execErr.Error(),
			RetryClass:   info.RetryClass,
			TraceID:      traceID,
			Timing: relayActionTiming{
				LatencyMS:  latency,
				QueueDepth: queueDepth,
			},
		}
		h.cacheRelayResponse(req, resp, info.HTTPStatus)
		logger.WarnCF("browser-relay", "browser relay v2 action failed", map[string]any{
			"trace_id":    traceID,
			"request_id":  req.RequestID,
			"target":      req.Target,
			"action":      action,
			"engine":      source,
			"queue_depth": queueDepth,
			"latency_ms":  latency,
			"error_code":  resp.ErrorCode,
			"retry_class": resp.RetryClass,
			"error":       execErr.Error(),
		})
		h.writeRelayV2Error(w, resp, info.HTTPStatus)
		return
	}

	resp := relayActionV2Response{
		RequestID: req.RequestID,
		OK:        true,
		Result:    result,
		TraceID:   traceID,
		Timing: relayActionTiming{
			LatencyMS:  latency,
			QueueDepth: queueDepth,
		},
	}
	h.cacheRelayResponse(req, resp, http.StatusOK)
	logger.DebugCF("browser-relay", "browser relay v2 action completed", map[string]any{
		"trace_id":    traceID,
		"request_id":  req.RequestID,
		"target":      req.Target,
		"action":      action,
		"engine":      source,
		"queue_depth": queueDepth,
		"latency_ms":  latency,
		"result":      "ok",
	})
	h.writeRelayV2(w, resp, http.StatusOK)
}

func (h *Handler) validateAndConvertRelayActionV2(req relayActionV2Request) (string, browserrelay.ActionRequest, error) {
	target := strings.TrimSpace(req.Target)
	action := strings.TrimSpace(req.Action)
	if len(req.Steps) > 0 {
		if action == "" {
			action = "batch"
		}
		if action != "batch" {
			return "", browserrelay.ActionRequest{}, fmt.Errorf("steps can only be used with action=batch")
		}
	}
	if action == "" {
		return "", browserrelay.ActionRequest{}, fmt.Errorf("action is required")
	}
	payload := browserrelay.ActionRequest{
		TargetID:       target,
		StopOnError:    true,
		StopOnErrorSet: false,
	}
	if req.ExecutionPolicy.StopOnError != nil {
		payload.StopOnErrorSet = true
		payload.StopOnError = *req.ExecutionPolicy.StopOnError
	}

	if action == "batch" {
		if target == "" {
			return "", browserrelay.ActionRequest{}, fmt.Errorf("target is required for batch")
		}
		if len(req.Steps) == 0 {
			return "", browserrelay.ActionRequest{}, fmt.Errorf("steps are required for batch")
		}
		payload.Steps = make([]browserrelay.BatchStep, 0, len(req.Steps))
		for i, step := range req.Steps {
			a := strings.TrimSpace(step.Action)
			if a == "" {
				return "", browserrelay.ActionRequest{}, fmt.Errorf("steps[%d].action is required", i)
			}
			bs := browserrelay.BatchStep{
				Action:          a,
				TargetID:        target,
				URL:             argString(step.Args, "url"),
				Selector:        argString(step.Args, "selector"),
				Text:            argString(step.Args, "text"),
				Key:             argString(step.Args, "key"),
				Expression:      argString(step.Args, "expression"),
				WaitMode:        argString(step.Args, "wait_mode"),
				RefGeneration:   argString(step.Args, "ref_generation"),
				SnapshotMode:    argString(step.Args, "mode"),
				InteractiveOnly: argBoolPtr(step.Args, "interactive_only"),
				ScopeSelector:   argString(step.Args, "scope_selector"),
				Depth:           argInt(step.Args, "depth"),
				MaxNodes:        argInt(step.Args, "max_nodes"),
				MaxTextChars:    argInt(step.Args, "max_text_chars"),
				TimeoutMS:       argInt(step.Args, "timeout_ms"),
				IntervalMS:      argInt(step.Args, "interval_ms"),
			}
			if (a == "click" || a == "type") && !isRelayRefSelector(bs.Selector) {
				return "", browserrelay.ActionRequest{}, fmt.Errorf(
					"%w: steps[%d].selector must be @eN for action=%s",
					browserrelay.ErrSnapshotRefRequired,
					i,
					a,
				)
			}
			payload.Steps = append(payload.Steps, bs)
		}
		return action, payload, nil
	}

	payload.SessionID = argString(req.Args, "session_id")
	payload.URL = argString(req.Args, "url")
	payload.Selector = argString(req.Args, "selector")
	payload.Text = argString(req.Args, "text")
	payload.Key = argString(req.Args, "key")
	payload.Expression = argString(req.Args, "expression")
	payload.WaitMode = argString(req.Args, "wait_mode")
	payload.RefGeneration = argString(req.Args, "ref_generation")
	payload.SnapshotMode = argString(req.Args, "mode")
	payload.InteractiveOnly = argBoolPtr(req.Args, "interactive_only")
	payload.ScopeSelector = argString(req.Args, "scope_selector")
	payload.Depth = argInt(req.Args, "depth")
	payload.MaxNodes = argInt(req.Args, "max_nodes")
	payload.MaxTextChars = argInt(req.Args, "max_text_chars")
	payload.TimeoutMS = argInt(req.Args, "timeout_ms")
	payload.IntervalMS = argInt(req.Args, "interval_ms")
	if (action == "click" || action == "type") && !isRelayRefSelector(payload.Selector) {
		return "", browserrelay.ActionRequest{}, fmt.Errorf(
			"%w: selector must be @eN for action=%s",
			browserrelay.ErrSnapshotRefRequired,
			action,
		)
	}
	return action, payload, nil
}

func isRelayRefSelector(selector string) bool {
	selector = strings.TrimSpace(selector)
	return strings.HasPrefix(selector, "@e") && len(selector) > 2
}

func argString(args map[string]any, key string) string {
	if len(args) == 0 {
		return ""
	}
	v, ok := args[key]
	if !ok || v == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprintf("%v", v))
}

func argInt(args map[string]any, key string) int {
	if len(args) == 0 {
		return 0
	}
	v, ok := args[key]
	if !ok || v == nil {
		return 0
	}
	switch t := v.(type) {
	case float64:
		return int(t)
	case float32:
		return int(t)
	case int:
		return t
	case int64:
		return int(t)
	case json.Number:
		i, err := t.Int64()
		if err == nil {
			return int(i)
		}
	}
	return 0
}

func argBoolPtr(args map[string]any, key string) *bool {
	if len(args) == 0 {
		return nil
	}
	v, ok := args[key]
	if !ok || v == nil {
		return nil
	}
	switch t := v.(type) {
	case bool:
		val := t
		return &val
	case string:
		trimmed := strings.TrimSpace(strings.ToLower(t))
		if trimmed == "true" || trimmed == "1" || trimmed == "yes" || trimmed == "on" {
			val := true
			return &val
		}
		if trimmed == "false" || trimmed == "0" || trimmed == "no" || trimmed == "off" {
			val := false
			return &val
		}
	}
	return nil
}

func classifyRelayError(err error) relayErrorInfo {
	if strings.Contains(err.Error(), "is required") || strings.Contains(err.Error(), "are required") {
		return relayErrorInfo{HTTPStatus: http.StatusBadRequest, Code: "validation_error", RetryClass: retryClassNever}
	}
	switch {
	case errors.Is(err, browserrelay.ErrUnsupportedAction):
		return relayErrorInfo{HTTPStatus: http.StatusNotFound, Code: "unsupported_action", RetryClass: retryClassNever}
	case errors.Is(err, browserrelay.ErrTargetNotAttached):
		return relayErrorInfo{HTTPStatus: http.StatusConflict, Code: "target_not_attached", RetryClass: retryClassAfterStateChange}
	case errors.Is(err, browserrelay.ErrTargetOwned):
		return relayErrorInfo{HTTPStatus: http.StatusConflict, Code: "target_owned", RetryClass: retryClassAfterStateChange}
	case errors.Is(err, browserrelay.ErrMaxClientsReached):
		return relayErrorInfo{HTTPStatus: http.StatusTooManyRequests, Code: "max_clients_reached", RetryClass: retryClassSafeBackoff}
	case errors.Is(err, browserrelay.ErrRelayQueueFull):
		return relayErrorInfo{HTTPStatus: http.StatusTooManyRequests, Code: "queue_full", RetryClass: retryClassSafeBackoff}
	case errors.Is(err, browserrelay.ErrRelayLoopGuardTriggered):
		return relayErrorInfo{HTTPStatus: http.StatusConflict, Code: "relay_loop_guard_triggered", RetryClass: retryClassNever}
	case errors.Is(err, browserrelay.ErrSnapshotRefNotFound):
		return relayErrorInfo{HTTPStatus: http.StatusConflict, Code: "snapshot_ref_not_found", RetryClass: retryClassNever}
	case errors.Is(err, browserrelay.ErrSnapshotRefRequired):
		return relayErrorInfo{HTTPStatus: http.StatusBadRequest, Code: "snapshot_ref_required", RetryClass: retryClassNever}
	case errors.Is(err, browserrelay.ErrSnapshotPayloadTooLarge):
		return relayErrorInfo{HTTPStatus: http.StatusRequestEntityTooLarge, Code: "snapshot_payload_too_large", RetryClass: retryClassNever}
	case errors.Is(err, browserrelay.ErrSnapshotScopeNotFound):
		return relayErrorInfo{HTTPStatus: http.StatusNotFound, Code: "snapshot_scope_not_found", RetryClass: retryClassNever}
	case errors.Is(err, browserrelay.ErrSnapshotModeUnsupported):
		return relayErrorInfo{HTTPStatus: http.StatusBadRequest, Code: "snapshot_mode_unsupported", RetryClass: retryClassNever}
	case errors.Is(err, browserrelay.ErrSnapshotProgressBlocked):
		return relayErrorInfo{HTTPStatus: http.StatusConflict, Code: "snapshot_progress_blocked", RetryClass: retryClassNever}
	case errors.Is(err, browserrelay.ErrActionabilityNotEnabled):
		return relayErrorInfo{HTTPStatus: http.StatusConflict, Code: "actionability_not_enabled", RetryClass: retryClassAfterStateChange}
	case errors.Is(err, browserrelay.ErrActionabilityNotEvents):
		return relayErrorInfo{HTTPStatus: http.StatusConflict, Code: "actionability_not_receiving_events", RetryClass: retryClassAfterStateChange}
	case errors.Is(err, browserrelay.ErrActionabilityTimeout):
		return relayErrorInfo{HTTPStatus: http.StatusRequestTimeout, Code: "actionability_timeout", RetryClass: retryClassAfterStateChange}
	case errors.Is(err, browserrelay.ErrRelayQueueCanceled):
		return relayErrorInfo{HTTPStatus: http.StatusConflict, Code: "queue_canceled", RetryClass: retryClassAfterStateChange}
	case errors.Is(err, browserrelay.ErrAgentBrowserUnavailable):
		return relayErrorInfo{HTTPStatus: http.StatusServiceUnavailable, Code: "agent_browser_unavailable", RetryClass: retryClassSafeBackoff}
	case errors.Is(err, browserrelay.ErrAgentBrowserRuntimeDisconnected):
		return relayErrorInfo{
			HTTPStatus: http.StatusConflict,
			Code:       "agent_browser_runtime_disconnected",
			RetryClass: retryClassAfterStateChange,
		}
	case errors.Is(err, browserrelay.ErrAgentBrowserQueueCanceled):
		return relayErrorInfo{
			HTTPStatus: http.StatusConflict,
			Code:       "agent_browser_queue_canceled",
			RetryClass: retryClassAfterStateChange,
		}
	case errors.Is(err, browserrelay.ErrAgentBrowserBatchFailed):
		return relayErrorInfo{
			HTTPStatus: http.StatusBadGateway,
			Code:       "agent_browser_batch_failed",
			RetryClass: retryClassAfterStateChange,
		}
	case errors.Is(err, browserrelay.ErrNoExtensionForTarget):
		return relayErrorInfo{HTTPStatus: http.StatusNotFound, Code: "extension_not_found", RetryClass: retryClassAfterStateChange}
	case errors.Is(err, browserrelay.ErrTargetNotFound):
		return relayErrorInfo{HTTPStatus: http.StatusNotFound, Code: "target_not_found", RetryClass: retryClassAfterStateChange}
	case errors.Is(err, browserrelay.ErrSessionNotFound):
		return relayErrorInfo{HTTPStatus: http.StatusNotFound, Code: "session_not_found", RetryClass: retryClassAfterStateChange}
	case errors.Is(err, browserrelay.ErrInvalidTargetID):
		return relayErrorInfo{HTTPStatus: http.StatusBadRequest, Code: "invalid_target_id", RetryClass: retryClassNever}
	case errors.Is(err, browserrelay.ErrRequestTimeout):
		return relayErrorInfo{HTTPStatus: http.StatusRequestTimeout, Code: "request_timeout", RetryClass: retryClassSafeBackoff}
	case errors.Is(err, browserrelay.ErrRequestCanceled):
		return relayErrorInfo{HTTPStatus: http.StatusRequestTimeout, Code: "request_canceled", RetryClass: retryClassSafeImmediate}
	default:
		return relayErrorInfo{HTTPStatus: http.StatusBadGateway, Code: "relay_internal_error", RetryClass: retryClassSafeBackoff}
	}
}

func mapBrowserRelayError(err error) int {
	return classifyRelayError(err).HTTPStatus
}

func relayEngineFromAction(action, target string) string {
	if strings.HasPrefix(strings.TrimSpace(target), "ab:") || strings.HasPrefix(strings.TrimSpace(action), "session.") {
		return "agent_browser"
	}
	return "extension"
}

func browserrelayExtensionRawTarget(target string) string {
	id := strings.TrimSpace(target)
	if strings.HasPrefix(id, "ext:") {
		return strings.TrimSpace(strings.TrimPrefix(id, "ext:"))
	}
	return id
}

func (h *Handler) relayCachedResponse(req relayActionV2Request) (relayActionV2Response, int, bool) {
	fingerprint := relayRequestFingerprint(req)
	h.browserRelayReqMu.Lock()
	defer h.browserRelayReqMu.Unlock()
	h.cleanupRelayRequestCacheLocked()
	meta, ok := h.browserRelayReqMeta[req.RequestID]
	if !ok {
		return relayActionV2Response{}, 0, false
	}
	if meta.Fingerprint != fingerprint {
		return relayActionV2Response{
			RequestID:    req.RequestID,
			OK:           false,
			ErrorCode:    "request_id_reuse_conflict",
			ErrorMessage: "request_id already used with a different payload",
			RetryClass:   retryClassNever,
		}, http.StatusConflict, true
	}
	resp, ok := h.browserRelayReqCache[req.RequestID]
	if !ok {
		return relayActionV2Response{}, 0, false
	}
	status := meta.HTTPStatus
	if status <= 0 {
		status = http.StatusOK
	}
	return resp, status, true
}

func (h *Handler) cacheRelayResponse(req relayActionV2Request, resp relayActionV2Response, status int) {
	h.browserRelayReqMu.Lock()
	defer h.browserRelayReqMu.Unlock()
	h.cleanupRelayRequestCacheLocked()
	if len(h.browserRelayReqCache) >= defaultRelayRequestCacheMax {
		h.evictOldestRelayRequestLocked()
	}
	h.browserRelayReqCache[req.RequestID] = resp
	h.browserRelayReqMeta[req.RequestID] = relayRequestCacheMeta{
		CreatedAt:   time.Now(),
		Fingerprint: relayRequestFingerprint(req),
		HTTPStatus:  status,
	}
}

func (h *Handler) cleanupRelayRequestCacheLocked() {
	cutoff := time.Now().Add(-defaultRelayRequestCacheTTL)
	for id, meta := range h.browserRelayReqMeta {
		if meta.CreatedAt.Before(cutoff) {
			delete(h.browserRelayReqMeta, id)
			delete(h.browserRelayReqCache, id)
		}
	}
}

func (h *Handler) evictOldestRelayRequestLocked() {
	var (
		oldestID string
		oldestAt time.Time
	)
	for id, meta := range h.browserRelayReqMeta {
		if oldestID == "" || meta.CreatedAt.Before(oldestAt) {
			oldestID = id
			oldestAt = meta.CreatedAt
		}
	}
	if oldestID != "" {
		delete(h.browserRelayReqMeta, oldestID)
		delete(h.browserRelayReqCache, oldestID)
	}
}

func relayRequestFingerprint(req relayActionV2Request) string {
	type requestShape struct {
		Target          string
		Action          string
		Args            map[string]any
		Steps           []relayActionV2Step
		ExecutionPolicy relayExecutionPolicy
	}
	data, _ := json.Marshal(requestShape{
		Target:          req.Target,
		Action:          req.Action,
		Args:            req.Args,
		Steps:           req.Steps,
		ExecutionPolicy: req.ExecutionPolicy,
	})
	return string(data)
}

func (h *Handler) writeRelayV2(w http.ResponseWriter, resp relayActionV2Response, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(resp)
}

func (h *Handler) writeRelayV2Error(w http.ResponseWriter, resp relayActionV2Response, status int) {
	if status <= 0 {
		status = http.StatusBadRequest
	}
	h.writeRelayV2(w, resp, status)
}

func isBrowserRelayDebugTrace() bool {
	value := strings.TrimSpace(strings.ToLower(os.Getenv("SUPRCLAW_BROWSER_RELAY_DEBUG_TRACE")))
	return value == "1" || value == "true" || value == "yes" || value == "on"
}

func (h *Handler) handleBrowserRelayExtensionWS(w http.ResponseWriter, r *http.Request) {
	cfg, relayCfg, manager, ok := h.requireBrowserRelayEnabled(w, r, false, false)
	if !ok {
		return
	}
	_ = cfg

	if authorized, subprotocol := h.browserRelayWSAuthorization(r, relayCfg); !authorized {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	} else {
		header := http.Header{}
		if subprotocol != "" {
			header.Set("Sec-WebSocket-Protocol", subprotocol)
		}
		conn, err := browserRelayUpgrader.Upgrade(w, r, header)
		if err != nil {
			return
		}
		sessionID := strings.TrimSpace(r.URL.Query().Get("session_id"))
		if sessionID == "" {
			sessionID = uuid.NewString()
		}

		wsConn := &relayWSConn{conn: conn}
		manager.AttachExtension(sessionID, wsConn)
		defer manager.DetachExtension(sessionID)

		for {
			_, payload, readErr := conn.ReadMessage()
			if readErr != nil {
				return
			}
			_ = manager.HandleExtensionMessage(sessionID, payload)
		}
	}
}

func (h *Handler) handleBrowserRelayCDPWS(w http.ResponseWriter, r *http.Request) {
	_, relayCfg, manager, ok := h.requireBrowserRelayEnabled(w, r, false, false)
	if !ok {
		return
	}
	if authorized, subprotocol := h.browserRelayWSAuthorization(r, relayCfg); !authorized {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	} else {
		targetID := strings.TrimSpace(r.PathValue("targetId"))
		if targetID == "" {
			http.Error(w, "missing targetId", http.StatusBadRequest)
			return
		}
		if strings.HasPrefix(targetID, "ext:") {
			targetID = strings.TrimSpace(strings.TrimPrefix(targetID, "ext:"))
		}

		header := http.Header{}
		if subprotocol != "" {
			header.Set("Sec-WebSocket-Protocol", subprotocol)
		}
		conn, err := browserRelayUpgrader.Upgrade(w, r, header)
		if err != nil {
			return
		}

		clientID := uuid.NewString()
		wsConn := &relayWSConn{conn: conn}
		if err = manager.RegisterCDPClient(clientID, targetID, wsConn); err != nil {
			_ = conn.WriteJSON(map[string]any{"error": map[string]any{"message": err.Error()}})
			_ = conn.Close()
			return
		}
		defer manager.UnregisterCDPClient(clientID)

		for {
			_, payload, readErr := conn.ReadMessage()
			if readErr != nil {
				return
			}

			var req struct {
				ID     json.RawMessage `json:"id"`
				Method string          `json:"method"`
				Params json.RawMessage `json:"params"`
			}
			if err = json.Unmarshal(payload, &req); err != nil {
				_ = wsConn.WriteJSON(map[string]any{"error": map[string]any{"message": "invalid request"}})
				continue
			}
			if req.Method == "" {
				continue
			}

			var params any
			if len(req.Params) > 0 {
				_ = json.Unmarshal(req.Params, &params)
			}

			ctx, cancel := context.WithTimeout(r.Context(), defaultBrowserRelayTimeout)
			result, commandErr := manager.SendCommand(ctx, targetID, req.Method, params, true)
			cancel()

			if len(req.ID) == 0 {
				continue
			}

			response := map[string]any{}
			var decodedID any
			if unmarshalErr := json.Unmarshal(req.ID, &decodedID); unmarshalErr != nil {
				decodedID = string(req.ID)
			}
			response["id"] = decodedID
			if commandErr != nil {
				response["error"] = map[string]any{"message": commandErr.Error()}
			} else {
				response["result"] = decodeRawResult(result)
			}
			_ = wsConn.WriteJSON(response)
		}
	}
}

func (h *Handler) requireBrowserRelayEnabled(
	w http.ResponseWriter,
	r *http.Request,
	requireAuth bool,
	allowSessionAuth bool,
) (*config.Config, config.BrowserRelayConfig, *browserrelay.Manager, bool) {
	cfg, err := config.LoadConfig(h.configPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to load config: %v", err), http.StatusInternalServerError)
		return nil, config.BrowserRelayConfig{}, nil, false
	}
	relayCfg := normalizeBrowserRelayConfig(cfg.Tools.BrowserRelay)
	if !relayCfg.Enabled {
		http.Error(w, "browser relay is disabled", http.StatusServiceUnavailable)
		return nil, config.BrowserRelayConfig{}, nil, false
	}
	if !isLoopbackHost(relayCfg.Host) {
		http.Error(w, "browser relay host must be loopback", http.StatusBadRequest)
		return nil, config.BrowserRelayConfig{}, nil, false
	}
	if requireAuth {
		if !h.isBrowserRelayHTTPAuthorized(r, cfg) {
			if !allowSessionAuth {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return nil, config.BrowserRelayConfig{}, nil, false
			}
			if _, err := h.browserRelaySessionClaimsFromRequest(r, cfg); err != nil {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return nil, config.BrowserRelayConfig{}, nil, false
			}
		}
	}
	state, stateErr := h.withBrowserRelayLeaseState(nil)
	if stateErr != nil {
		http.Error(w, fmt.Sprintf("failed to load relay lease state: %v", stateErr), http.StatusInternalServerError)
		return nil, config.BrowserRelayConfig{}, nil, false
	}
	if state.LeaseState == browserRelayLeaseStopped {
		http.Error(w, "relay lease is stopped", http.StatusConflict)
		return nil, config.BrowserRelayConfig{}, nil, false
	}
	return cfg, relayCfg, h.browserRelayManager(cfg), true
}

func normalizeBrowserRelayConfig(cfg config.BrowserRelayConfig) config.BrowserRelayConfig {
	rawSnapshotDefaultsUnset := strings.TrimSpace(cfg.SnapshotDefaultMode) == "" &&
		cfg.SnapshotMaxPayloadBytes == 0 &&
		cfg.SnapshotMaxNodes == 0 &&
		cfg.SnapshotMaxTextChars == 0 &&
		cfg.SnapshotMaxDepth == 0 &&
		cfg.SnapshotRefTTLSec == 0 &&
		cfg.SnapshotMaxGenerations == 0
	rawAgentBrowserRuntimeDefaultsUnset := cfg.AgentBrowserBatchWindowMS == 0 &&
		cfg.AgentBrowserBatchMaxSteps == 0 &&
		cfg.AgentBrowserRuntimeCommandTimeoutMS == 0 &&
		cfg.AgentBrowserStreamPort == 0 &&
		!cfg.AgentBrowserStreamEnabled

	if strings.TrimSpace(cfg.Host) == "" {
		cfg.Host = defaultBrowserRelayHost
	}
	if cfg.Port == 0 {
		cfg.Port = defaultBrowserRelayPort
	}
	if cfg.MaxClients <= 0 {
		cfg.MaxClients = defaultBrowserRelayMaxClients
	}
	if cfg.IdleTimeoutSec <= 0 {
		cfg.IdleTimeoutSec = defaultBrowserRelayIdleTimeoutSec
	}
	switch strings.ToLower(strings.TrimSpace(cfg.EngineMode)) {
	case "", browserrelay.EngineModeHybrid:
		cfg.EngineMode = browserrelay.EngineModeHybrid
	case browserrelay.EngineModeExtension:
		cfg.EngineMode = browserrelay.EngineModeExtension
	case browserrelay.EngineModeAgentBrowser:
		cfg.EngineMode = browserrelay.EngineModeAgentBrowser
	default:
		cfg.EngineMode = browserrelay.EngineModeHybrid
	}
	if strings.TrimSpace(cfg.AgentBrowserBinary) == "" {
		cfg.AgentBrowserBinary = "agent-browser"
	}
	if cfg.AgentBrowserMaxSessions <= 0 {
		cfg.AgentBrowserMaxSessions = 8
	}
	if cfg.AgentBrowserIdleTimeoutSec <= 0 {
		cfg.AgentBrowserIdleTimeoutSec = 300
	}
	if cfg.AgentBrowserBatchWindowMS <= 0 {
		cfg.AgentBrowserBatchWindowMS = 25
	}
	if cfg.AgentBrowserBatchMaxSteps <= 0 {
		cfg.AgentBrowserBatchMaxSteps = 24
	}
	if cfg.AgentBrowserRuntimeCommandTimeoutMS <= 0 {
		cfg.AgentBrowserRuntimeCommandTimeoutMS = 30000
	}
	if cfg.AgentBrowserStreamPort < 0 {
		cfg.AgentBrowserStreamPort = 0
	}
	if rawAgentBrowserRuntimeDefaultsUnset && !cfg.AgentBrowserStreamEnabled {
		cfg.AgentBrowserStreamEnabled = true
	}
	switch strings.ToLower(strings.TrimSpace(cfg.SnapshotDefaultMode)) {
	case "", "compact":
		cfg.SnapshotDefaultMode = "compact"
	case "full":
		cfg.SnapshotDefaultMode = "full"
	default:
		cfg.SnapshotDefaultMode = "compact"
	}
	if cfg.SnapshotMaxPayloadBytes <= 0 {
		cfg.SnapshotMaxPayloadBytes = 98304
	}
	if cfg.SnapshotMaxNodes <= 0 {
		cfg.SnapshotMaxNodes = 120
	}
	if cfg.SnapshotMaxTextChars <= 0 {
		cfg.SnapshotMaxTextChars = 120
	}
	if cfg.SnapshotMaxDepth <= 0 {
		cfg.SnapshotMaxDepth = 6
	}
	if cfg.SnapshotRefTTLSec <= 0 {
		cfg.SnapshotRefTTLSec = 600
	}
	if cfg.SnapshotMaxGenerations <= 0 {
		cfg.SnapshotMaxGenerations = 4
	}
	if rawSnapshotDefaultsUnset && !cfg.SnapshotInteractiveOnly {
		cfg.SnapshotInteractiveOnly = true
	}
	if rawSnapshotDefaultsUnset && !cfg.SnapshotAllowFullTree {
		cfg.SnapshotAllowFullTree = true
	}
	if cfg.PairingCodeTTLSec <= 0 {
		cfg.PairingCodeTTLSec = 180
	}
	if cfg.SessionTokenTTLSec <= 0 {
		cfg.SessionTokenTTLSec = 900
	}
	if cfg.RefreshTokenTTLSec <= 0 {
		cfg.RefreshTokenTTLSec = 7 * 24 * 60 * 60
	}
	return cfg
}

func decodeRawResult(raw json.RawMessage) any {
	if len(raw) == 0 {
		return map[string]any{}
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return map[string]any{"raw": string(raw)}
	}
	return decoded
}

func browserRelayConfigFromConfig(cfg *config.Config) browserrelay.Config {
	relayCfg := normalizeBrowserRelayConfig(cfg.Tools.BrowserRelay)
	return browserrelay.Config{
		Enabled:                             relayCfg.Enabled,
		Host:                                relayCfg.Host,
		Port:                                relayCfg.Port,
		Token:                               relayCfg.Token,
		CompatOpenClaw:                      relayCfg.CompatOpenClaw,
		MaxClients:                          relayCfg.MaxClients,
		IdleTimeoutSec:                      relayCfg.IdleTimeoutSec,
		AllowTokenQuery:                     relayCfg.AllowTokenQuery,
		EngineMode:                          relayCfg.EngineMode,
		AgentBrowserEnabled:                 relayCfg.AgentBrowserEnabled,
		AgentBrowserBinary:                  relayCfg.AgentBrowserBinary,
		AgentBrowserDefaultHeadless:         relayCfg.AgentBrowserDefaultHeadless,
		AgentBrowserMaxSessions:             relayCfg.AgentBrowserMaxSessions,
		AgentBrowserIdleTimeoutSec:          relayCfg.AgentBrowserIdleTimeoutSec,
		AgentBrowserBatchWindowMS:           relayCfg.AgentBrowserBatchWindowMS,
		AgentBrowserBatchMaxSteps:           relayCfg.AgentBrowserBatchMaxSteps,
		AgentBrowserStreamEnabled:           relayCfg.AgentBrowserStreamEnabled,
		AgentBrowserStreamPort:              relayCfg.AgentBrowserStreamPort,
		AgentBrowserRuntimeCommandTimeoutMS: relayCfg.AgentBrowserRuntimeCommandTimeoutMS,
		SnapshotDefaultMode:                 relayCfg.SnapshotDefaultMode,
		SnapshotMaxPayloadBytes:             relayCfg.SnapshotMaxPayloadBytes,
		SnapshotMaxNodes:                    relayCfg.SnapshotMaxNodes,
		SnapshotMaxTextChars:                relayCfg.SnapshotMaxTextChars,
		SnapshotMaxDepth:                    relayCfg.SnapshotMaxDepth,
		SnapshotInteractiveOnly:             relayCfg.SnapshotInteractiveOnly,
		SnapshotRefTTLSec:                   relayCfg.SnapshotRefTTLSec,
		SnapshotMaxGenerations:              relayCfg.SnapshotMaxGenerations,
		SnapshotAllowFullTree:               relayCfg.SnapshotAllowFullTree,
	}
}

func (h *Handler) browserRelayManager(cfg *config.Config) *browserrelay.Manager {
	h.browserRelayMu.Lock()
	defer h.browserRelayMu.Unlock()

	relayConfig := browserRelayConfigFromConfig(cfg)
	if h.browserRelay == nil {
		h.browserRelay = browserrelay.NewManager(relayConfig)
	} else {
		h.browserRelay.ApplyConfig(relayConfig)
	}
	return h.browserRelay
}

func (h *Handler) browserRelayRouter(cfg *config.Config) *browserrelay.EngineRouter {
	h.browserRelayMu.Lock()
	defer h.browserRelayMu.Unlock()

	relayConfig := browserRelayConfigFromConfig(cfg)
	if h.browserRelay == nil {
		h.browserRelay = browserrelay.NewManager(relayConfig)
	} else {
		h.browserRelay.ApplyConfig(relayConfig)
	}
	if h.browserRelayAgent == nil {
		h.browserRelayAgent = browserrelay.NewAgentBrowserEngine(relayConfig, nil)
	} else {
		h.browserRelayAgent.ApplyConfig(relayConfig)
	}
	if h.browserRelayActionRouter == nil {
		h.browserRelayActionRouter = browserrelay.NewEngineRouter(
			relayConfig,
			browserrelay.NewExtensionEngine(h.browserRelay),
			h.browserRelayAgent,
		)
	} else {
		h.browserRelayActionRouter.ApplyConfig(relayConfig)
	}
	return h.browserRelayActionRouter
}

func (h *Handler) isBrowserRelayHTTPAuthorized(r *http.Request, cfg *config.Config) bool {
	relayCfg := normalizeBrowserRelayConfig(cfg.Tools.BrowserRelay)
	token := strings.TrimSpace(relayCfg.Token)
	if token == "" {
		return false
	}
	if strings.TrimSpace(extractBearerToken(r.Header.Get("Authorization"))) == token {
		return true
	}
	if relayCfg.AllowTokenQuery && strings.TrimSpace(r.URL.Query().Get("token")) == token {
		return true
	}
	return false
}

func (h *Handler) browserRelayWSAuthorization(r *http.Request, relayCfg config.BrowserRelayConfig) (bool, string) {
	token := strings.TrimSpace(relayCfg.Token)
	if token == "" {
		return false, ""
	}
	if strings.TrimSpace(extractBearerToken(r.Header.Get("Authorization"))) == token {
		return true, ""
	}
	for _, proto := range websocket.Subprotocols(r) {
		if after, ok := strings.CutPrefix(proto, "token."); ok && strings.TrimSpace(after) == token {
			return true, proto
		}
	}
	if relayCfg.AllowTokenQuery && strings.TrimSpace(r.URL.Query().Get("token")) == token {
		return true, ""
	}
	return false, ""
}

func extractBearerToken(authHeader string) string {
	authHeader = strings.TrimSpace(authHeader)
	if after, ok := strings.CutPrefix(authHeader, "Bearer "); ok {
		return strings.TrimSpace(after)
	}
	return ""
}

func (h *Handler) browserRelayHostPort(r *http.Request, relayCfg config.BrowserRelayConfig) (string, int) {
	host := strings.TrimSpace(relayCfg.Host)
	if host == "" || host == "0.0.0.0" {
		host = requestHostName(r)
	}
	port := h.serverPort
	if port == 0 {
		if rPort := parsePortFromHost(r.Host); rPort > 0 {
			port = rPort
		} else {
			port = 18800
		}
	}
	return host, port
}

func parsePortFromHost(hostport string) int {
	if hostport == "" {
		return 0
	}
	_, portStr, err := net.SplitHostPort(hostport)
	if err != nil {
		return 0
	}
	port, convErr := strconv.Atoi(portStr)
	if convErr != nil {
		return 0
	}
	return port
}

func (h *Handler) browserRelayExtensionURL(r *http.Request, relayCfg config.BrowserRelayConfig) string {
	host, port := h.browserRelayHostPort(r, relayCfg)
	return requestWSScheme(r) + "://" + net.JoinHostPort(host, strconv.Itoa(port)) + "/browser-relay/extension"
}

func (h *Handler) browserRelayCDPTemplateURL(r *http.Request, relayCfg config.BrowserRelayConfig) string {
	host, port := h.browserRelayHostPort(r, relayCfg)
	return requestWSScheme(r) + "://" + net.JoinHostPort(host, strconv.Itoa(port)) + "/browser-relay/cdp/{targetId}"
}

type relayWSConn struct {
	conn    *websocket.Conn
	writeMu sync.Mutex
}

func (w *relayWSConn) WriteJSON(v any) error {
	w.writeMu.Lock()
	defer w.writeMu.Unlock()
	return w.conn.WriteJSON(v)
}

func (w *relayWSConn) Close() error {
	w.writeMu.Lock()
	defer w.writeMu.Unlock()
	return w.conn.Close()
}
