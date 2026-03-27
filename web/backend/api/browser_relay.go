package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
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
	mux.HandleFunc("POST /api/browser-relay/actions/{action}", h.handleBrowserRelayAction)

	mux.HandleFunc("GET /browser-relay/extension", h.handleBrowserRelayExtensionWS)
	mux.HandleFunc("GET /browser-relay/cdp/{targetId}", h.handleBrowserRelayCDPWS)

	mux.HandleFunc("GET /json/version", h.handleBrowserRelayJSONVersion)
	mux.HandleFunc("GET /json/list", h.handleBrowserRelayJSONList)
	mux.HandleFunc("GET /devtools/page/{targetId}", h.handleBrowserRelayCompatWS)
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
		"status":                status,
		"enabled":               relayCfg.Enabled,
		"host":                  relayCfg.Host,
		"port":                  relayCfg.Port,
		"compat_openclaw":       relayCfg.CompatOpenClaw,
		"allow_token_query":     relayCfg.AllowTokenQuery,
		"engine_mode":           relayCfg.EngineMode,
		"agent_browser_enabled": relayCfg.AgentBrowserEnabled,
		"agent_browser_binary":  relayCfg.AgentBrowserBinary,
		"extension_ws_url":      h.browserRelayExtensionURL(r, relayCfg),
		"cdp_ws_url_template":   h.browserRelayCDPTemplateURL(r, relayCfg),
		"configured_relay_port": relayCfg.Port,
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

type browserRelayActionRequest struct {
	TargetID   string `json:"target_id"`
	SessionID  string `json:"session_id"`
	URL        string `json:"url"`
	Selector   string `json:"selector"`
	Text       string `json:"text"`
	Key        string `json:"key"`
	Expression string `json:"expression"`
	TimeoutMS  int    `json:"timeout_ms"`
	IntervalMS int    `json:"interval_ms"`
}

func (h *Handler) handleBrowserRelayAction(w http.ResponseWriter, r *http.Request) {
	cfg, _, _, ok := h.requireBrowserRelayEnabled(w, r, true, true)
	if !ok {
		return
	}
	router := h.browserRelayRouter(cfg)

	action := strings.TrimSpace(r.PathValue("action"))
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	var req browserRelayActionRequest
	if len(body) > 0 {
		if err = json.Unmarshal(body, &req); err != nil {
			http.Error(w, fmt.Sprintf("Invalid JSON: %v", err), http.StatusBadRequest)
			return
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), defaultBrowserRelayTimeout)
	defer cancel()

	result, statusCode, err := h.executeBrowserRelayAction(ctx, router, action, req)
	if err != nil {
		http.Error(w, err.Error(), statusCode)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"action": action,
		"result": result,
	})
}

func (h *Handler) executeBrowserRelayAction(
	ctx context.Context,
	router *browserrelay.EngineRouter,
	action string,
	req browserRelayActionRequest,
) (any, int, error) {
	start := time.Now()
	source := "extension"
	if strings.HasPrefix(strings.TrimSpace(req.TargetID), "ab:") || strings.HasPrefix(action, "session.") {
		source = "agent_browser"
	}
	result, err := router.ExecuteAction(ctx, action, browserrelay.ActionRequest{
		TargetID:   req.TargetID,
		SessionID:  req.SessionID,
		URL:        req.URL,
		Selector:   req.Selector,
		Text:       req.Text,
		Key:        req.Key,
		Expression: req.Expression,
		TimeoutMS:  req.TimeoutMS,
		IntervalMS: req.IntervalMS,
	})
	if err != nil {
		status := mapBrowserRelayError(err)
		if errors.Is(err, browserrelay.ErrUnsupportedAction) {
			status = http.StatusNotFound
		}
		if strings.Contains(err.Error(), "is required") {
			status = http.StatusBadRequest
		}
		logger.WarnCF("browser-relay", "browser relay action failed", map[string]any{
			"action":      action,
			"target_id":   req.TargetID,
			"source":      source,
			"latency_ms":  time.Since(start).Milliseconds(),
			"status_code": status,
			"error":       err.Error(),
		})
		return nil, status, err
	}
	logger.DebugCF("browser-relay", "browser relay action completed", map[string]any{
		"action":     action,
		"target_id":  req.TargetID,
		"source":     source,
		"latency_ms": time.Since(start).Milliseconds(),
	})
	return result, http.StatusOK, nil
}

func mapBrowserRelayError(err error) int {
	switch {
	case errors.Is(err, browserrelay.ErrTargetNotAttached), errors.Is(err, browserrelay.ErrTargetOwned):
		return http.StatusConflict
	case errors.Is(err, browserrelay.ErrMaxClientsReached):
		return http.StatusTooManyRequests
	case errors.Is(err, browserrelay.ErrAgentBrowserUnavailable):
		return http.StatusServiceUnavailable
	case errors.Is(err, browserrelay.ErrNoExtensionForTarget), errors.Is(err, browserrelay.ErrTargetNotFound), errors.Is(err, browserrelay.ErrSessionNotFound):
		return http.StatusNotFound
	case errors.Is(err, browserrelay.ErrInvalidTargetID):
		return http.StatusBadRequest
	case errors.Is(err, browserrelay.ErrRequestTimeout), errors.Is(err, browserrelay.ErrRequestCanceled):
		return http.StatusRequestTimeout
	default:
		return http.StatusBadGateway
	}
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
	h.handleBrowserRelayCDPWSWithCompat(w, r, false)
}

func (h *Handler) handleBrowserRelayCompatWS(w http.ResponseWriter, r *http.Request) {
	h.handleBrowserRelayCDPWSWithCompat(w, r, true)
}

func (h *Handler) handleBrowserRelayCDPWSWithCompat(w http.ResponseWriter, r *http.Request, compatOnly bool) {
	_, relayCfg, manager, ok := h.requireBrowserRelayEnabled(w, r, false, false)
	if !ok {
		return
	}
	if compatOnly && !relayCfg.CompatOpenClaw {
		http.NotFound(w, r)
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

func (h *Handler) handleBrowserRelayJSONVersion(w http.ResponseWriter, r *http.Request) {
	_, relayCfg, _, ok := h.requireBrowserRelayEnabled(w, r, true, true)
	if !ok {
		return
	}
	if !relayCfg.CompatOpenClaw {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"Browser":              "SuprClawBrowserRelay/1.0",
		"Protocol-Version":     "1.3",
		"User-Agent":           "suprclaw-browser-relay",
		"webSocketDebuggerUrl": h.browserRelayCDPTemplateURL(r, relayCfg),
	})
}

func (h *Handler) handleBrowserRelayJSONList(w http.ResponseWriter, r *http.Request) {
	cfg, relayCfg, _, ok := h.requireBrowserRelayEnabled(w, r, true, true)
	if !ok {
		return
	}
	if !relayCfg.CompatOpenClaw {
		http.NotFound(w, r)
		return
	}

	base := h.browserRelayDevtoolsBaseURL(r)
	router := h.browserRelayRouter(cfg)
	raw, err := router.ExecuteAction(r.Context(), "tabs.list", browserrelay.ActionRequest{})
	if err != nil {
		http.Error(w, err.Error(), mapBrowserRelayError(err))
		return
	}
	payload, _ := raw.(map[string]any)
	targets, _ := payload["targets"].([]browserrelay.Target)
	items := make([]map[string]any, 0, len(targets))
	for _, target := range targets {
		if target.Source == browserrelay.TargetSourceAgentBrowser {
			continue
		}
		targetID := target.ID
		if strings.HasPrefix(targetID, "ext:") {
			targetID = strings.TrimSpace(strings.TrimPrefix(targetID, "ext:"))
		}
		if target.Type == "" {
			target.Type = "page"
		}
		wsURL := base + "/devtools/page/" + url.PathEscape(targetID)
		items = append(items, map[string]any{
			"description":          "SuprClaw browser relay target",
			"id":                   targetID,
			"title":                target.Title,
			"type":                 target.Type,
			"url":                  target.URL,
			"webSocketDebuggerUrl": wsURL,
			"devtoolsFrontendUrl":  "/devtools/inspector.html?ws=" + url.QueryEscape(strings.TrimPrefix(wsURL, "ws://")),
		})
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(items)
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

func browserRelayConfigFromConfig(cfg *config.Config) browserrelay.Config {
	relayCfg := normalizeBrowserRelayConfig(cfg.Tools.BrowserRelay)
	return browserrelay.Config{
		Enabled:                     relayCfg.Enabled,
		Host:                        relayCfg.Host,
		Port:                        relayCfg.Port,
		Token:                       relayCfg.Token,
		CompatOpenClaw:              relayCfg.CompatOpenClaw,
		MaxClients:                  relayCfg.MaxClients,
		IdleTimeoutSec:              relayCfg.IdleTimeoutSec,
		AllowTokenQuery:             relayCfg.AllowTokenQuery,
		EngineMode:                  relayCfg.EngineMode,
		AgentBrowserEnabled:         relayCfg.AgentBrowserEnabled,
		AgentBrowserBinary:          relayCfg.AgentBrowserBinary,
		AgentBrowserDefaultHeadless: relayCfg.AgentBrowserDefaultHeadless,
		AgentBrowserMaxSessions:     relayCfg.AgentBrowserMaxSessions,
		AgentBrowserIdleTimeoutSec:  relayCfg.AgentBrowserIdleTimeoutSec,
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

func (h *Handler) browserRelayDevtoolsBaseURL(r *http.Request) string {
	return requestWSScheme(r) + "://" + r.Host
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
