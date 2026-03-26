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
	_ = cfg

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"targets": manager.Targets(),
		"compat":  relayCfg.CompatOpenClaw,
	})
}

type browserRelayActionRequest struct {
	TargetID   string `json:"target_id"`
	URL        string `json:"url"`
	Selector   string `json:"selector"`
	Text       string `json:"text"`
	Key        string `json:"key"`
	Expression string `json:"expression"`
	TimeoutMS  int    `json:"timeout_ms"`
	IntervalMS int    `json:"interval_ms"`
}

func (h *Handler) handleBrowserRelayAction(w http.ResponseWriter, r *http.Request) {
	_, _, manager, ok := h.requireBrowserRelayEnabled(w, r, true, true)
	if !ok {
		return
	}

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

	result, statusCode, err := h.executeBrowserRelayAction(ctx, manager, action, req)
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
	manager *browserrelay.Manager,
	action string,
	req browserRelayActionRequest,
) (any, int, error) {
	switch action {
	case "tabs.list":
		return map[string]any{"targets": manager.Targets()}, http.StatusOK, nil
	case "tabs.select":
		if req.TargetID == "" {
			return nil, http.StatusBadRequest, fmt.Errorf("target_id is required")
		}
		raw, err := manager.SendCommand(ctx, req.TargetID, "Target.activateTarget", map[string]any{"targetId": req.TargetID}, false)
		if err != nil {
			return nil, mapBrowserRelayError(err), err
		}
		return decodeRawResult(raw), http.StatusOK, nil
	case "navigate":
		if req.TargetID == "" || req.URL == "" {
			return nil, http.StatusBadRequest, fmt.Errorf("target_id and url are required")
		}
		raw, err := manager.SendCommand(ctx, req.TargetID, "Page.navigate", map[string]any{"url": req.URL}, true)
		if err != nil {
			return nil, mapBrowserRelayError(err), err
		}
		return decodeRawResult(raw), http.StatusOK, nil
	case "click":
		if req.TargetID == "" || req.Selector == "" {
			return nil, http.StatusBadRequest, fmt.Errorf("target_id and selector are required")
		}
		return h.browserRelayClick(ctx, manager, req.TargetID, req.Selector)
	case "type":
		if req.TargetID == "" || req.Selector == "" {
			return nil, http.StatusBadRequest, fmt.Errorf("target_id and selector are required")
		}
		return h.browserRelayType(ctx, manager, req.TargetID, req.Selector, req.Text)
	case "press":
		if req.TargetID == "" || req.Key == "" {
			return nil, http.StatusBadRequest, fmt.Errorf("target_id and key are required")
		}
		return h.browserRelayPress(ctx, manager, req.TargetID, req.Key)
	case "screenshot":
		if req.TargetID == "" {
			return nil, http.StatusBadRequest, fmt.Errorf("target_id is required")
		}
		raw, err := manager.SendCommand(ctx, req.TargetID, "Page.captureScreenshot", map[string]any{"format": "png"}, true)
		if err != nil {
			return nil, mapBrowserRelayError(err), err
		}
		return decodeRawResult(raw), http.StatusOK, nil
	case "snapshot":
		if req.TargetID == "" {
			return nil, http.StatusBadRequest, fmt.Errorf("target_id is required")
		}
		return h.browserRelaySnapshot(ctx, manager, req.TargetID)
	case "wait":
		if req.TargetID == "" || strings.TrimSpace(req.Expression) == "" {
			return nil, http.StatusBadRequest, fmt.Errorf("target_id and expression are required")
		}
		return h.browserRelayWait(ctx, manager, req.TargetID, req.Expression, req.TimeoutMS, req.IntervalMS)
	default:
		return nil, http.StatusNotFound, fmt.Errorf("unsupported action: %s", action)
	}
}

func (h *Handler) browserRelayClick(
	ctx context.Context,
	manager *browserrelay.Manager,
	targetID string,
	selector string,
) (any, int, error) {
	x, y, err := h.browserRelayResolveElementCenter(ctx, manager, targetID, selector)
	if err != nil {
		return nil, mapBrowserRelayError(err), err
	}

	if _, err = manager.SendCommand(ctx, targetID, "Input.dispatchMouseEvent", map[string]any{
		"type":       "mousePressed",
		"x":          x,
		"y":          y,
		"button":     "left",
		"clickCount": 1,
	}, true); err != nil {
		return nil, mapBrowserRelayError(err), err
	}
	if _, err = manager.SendCommand(ctx, targetID, "Input.dispatchMouseEvent", map[string]any{
		"type":       "mouseReleased",
		"x":          x,
		"y":          y,
		"button":     "left",
		"clickCount": 1,
	}, true); err != nil {
		return nil, mapBrowserRelayError(err), err
	}
	return map[string]any{"ok": true, "x": x, "y": y}, http.StatusOK, nil
}

func (h *Handler) browserRelayType(
	ctx context.Context,
	manager *browserrelay.Manager,
	targetID string,
	selector string,
	text string,
) (any, int, error) {
	_, _, err := h.browserRelayClick(ctx, manager, targetID, selector)
	if err != nil {
		return nil, mapBrowserRelayError(err), err
	}
	if _, err = manager.SendCommand(ctx, targetID, "Input.insertText", map[string]any{"text": text}, true); err != nil {
		return nil, mapBrowserRelayError(err), err
	}
	return map[string]any{"ok": true}, http.StatusOK, nil
}

func (h *Handler) browserRelayPress(
	ctx context.Context,
	manager *browserrelay.Manager,
	targetID string,
	key string,
) (any, int, error) {
	if _, err := manager.SendCommand(ctx, targetID, "Input.dispatchKeyEvent", map[string]any{
		"type": "keyDown",
		"key":  key,
	}, true); err != nil {
		return nil, mapBrowserRelayError(err), err
	}
	if _, err := manager.SendCommand(ctx, targetID, "Input.dispatchKeyEvent", map[string]any{
		"type": "keyUp",
		"key":  key,
	}, true); err != nil {
		return nil, mapBrowserRelayError(err), err
	}
	return map[string]any{"ok": true}, http.StatusOK, nil
}

func (h *Handler) browserRelaySnapshot(
	ctx context.Context,
	manager *browserrelay.Manager,
	targetID string,
) (any, int, error) {
	raw, err := manager.SendCommand(ctx, targetID, "Accessibility.getFullAXTree", nil, true)
	if err == nil {
		return map[string]any{"kind": "ax_tree", "value": decodeRawResult(raw)}, http.StatusOK, nil
	}
	fallback, fallbackErr := manager.SendCommand(ctx, targetID, "DOMSnapshot.captureSnapshot", map[string]any{
		"computedStyles": []string{},
	}, true)
	if fallbackErr != nil {
		return nil, mapBrowserRelayError(err), err
	}
	return map[string]any{"kind": "dom_snapshot", "value": decodeRawResult(fallback)}, http.StatusOK, nil
}

func (h *Handler) browserRelayWait(
	ctx context.Context,
	manager *browserrelay.Manager,
	targetID string,
	expression string,
	timeoutMS int,
	intervalMS int,
) (any, int, error) {
	if timeoutMS <= 0 {
		timeoutMS = 10000
	}
	if intervalMS <= 0 {
		intervalMS = 250
	}

	deadline := time.Now().Add(time.Duration(timeoutMS) * time.Millisecond)
	wrapped := "(() => { try { return !!(" + expression + "); } catch (_) { return false; } })()"
	for {
		if time.Now().After(deadline) {
			return nil, http.StatusRequestTimeout, fmt.Errorf("wait condition timed out")
		}
		raw, err := manager.SendCommand(ctx, targetID, "Runtime.evaluate", map[string]any{
			"expression":    wrapped,
			"returnByValue": true,
			"awaitPromise":  true,
		}, true)
		if err != nil {
			return nil, mapBrowserRelayError(err), err
		}
		if truthy, parseErr := runtimeEvaluateTruthy(raw); parseErr == nil && truthy {
			return map[string]any{"ok": true}, http.StatusOK, nil
		}
		select {
		case <-ctx.Done():
			return nil, http.StatusRequestTimeout, browserrelay.ErrRequestCanceled
		case <-time.After(time.Duration(intervalMS) * time.Millisecond):
		}
	}
}

func (h *Handler) browserRelayResolveElementCenter(
	ctx context.Context,
	manager *browserrelay.Manager,
	targetID string,
	selector string,
) (float64, float64, error) {
	expr := fmt.Sprintf(`(() => {
		const el = document.querySelector(%s);
		if (!el) return {ok:false,error:"not_found"};
		const r = el.getBoundingClientRect();
		return {ok:true,x:r.left + (r.width / 2), y:r.top + (r.height / 2)};
	})()`, strconv.Quote(selector))

	raw, err := manager.SendCommand(ctx, targetID, "Runtime.evaluate", map[string]any{
		"expression":    expr,
		"returnByValue": true,
		"awaitPromise":  true,
	}, true)
	if err != nil {
		return 0, 0, err
	}

	value, parseErr := runtimeEvaluateValue(raw)
	if parseErr != nil {
		return 0, 0, parseErr
	}
	obj, ok := value.(map[string]any)
	if !ok {
		return 0, 0, fmt.Errorf("unexpected evaluation result type")
	}
	okValue, _ := obj["ok"].(bool)
	if !okValue {
		if msg, _ := obj["error"].(string); msg != "" {
			return 0, 0, errors.New(msg)
		}
		return 0, 0, fmt.Errorf("selector resolution failed")
	}

	x, okX := toFloat(obj["x"])
	y, okY := toFloat(obj["y"])
	if !okX || !okY {
		return 0, 0, fmt.Errorf("invalid click coordinates")
	}
	return x, y, nil
}

func runtimeEvaluateValue(raw json.RawMessage) (any, error) {
	var payload struct {
		Result struct {
			Value any `json:"value"`
		} `json:"result"`
		ExceptionDetails any `json:"exceptionDetails"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}
	if payload.ExceptionDetails != nil {
		return nil, fmt.Errorf("runtime evaluate exception")
	}
	return payload.Result.Value, nil
}

func runtimeEvaluateTruthy(raw json.RawMessage) (bool, error) {
	value, err := runtimeEvaluateValue(raw)
	if err != nil {
		return false, err
	}
	truthy, ok := value.(bool)
	if !ok {
		return false, fmt.Errorf("runtime evaluate did not return bool")
	}
	return truthy, nil
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

func toFloat(value any) (float64, bool) {
	switch v := value.(type) {
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case json.Number:
		f, err := v.Float64()
		if err == nil {
			return f, true
		}
	}
	return 0, false
}

func mapBrowserRelayError(err error) int {
	switch {
	case errors.Is(err, browserrelay.ErrTargetNotAttached), errors.Is(err, browserrelay.ErrTargetOwned):
		return http.StatusConflict
	case errors.Is(err, browserrelay.ErrMaxClientsReached):
		return http.StatusTooManyRequests
	case errors.Is(err, browserrelay.ErrNoExtensionForTarget), errors.Is(err, browserrelay.ErrTargetNotFound):
		return http.StatusNotFound
	case errors.Is(err, browserrelay.ErrRequestTimeout), errors.Is(err, browserrelay.ErrRequestCanceled):
		return http.StatusRequestTimeout
	default:
		return http.StatusBadGateway
	}
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
	_, relayCfg, manager, ok := h.requireBrowserRelayEnabled(w, r, true, true)
	if !ok {
		return
	}
	if !relayCfg.CompatOpenClaw {
		http.NotFound(w, r)
		return
	}

	base := h.browserRelayDevtoolsBaseURL(r)
	targets := manager.Targets()
	items := make([]map[string]any, 0, len(targets))
	for _, target := range targets {
		if target.Type == "" {
			target.Type = "page"
		}
		wsURL := base + "/devtools/page/" + url.PathEscape(target.ID)
		items = append(items, map[string]any{
			"description":          "SuprClaw browser relay target",
			"id":                   target.ID,
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
		Enabled:         relayCfg.Enabled,
		Host:            relayCfg.Host,
		Port:            relayCfg.Port,
		Token:           relayCfg.Token,
		CompatOpenClaw:  relayCfg.CompatOpenClaw,
		MaxClients:      relayCfg.MaxClients,
		IdleTimeoutSec:  relayCfg.IdleTimeoutSec,
		AllowTokenQuery: relayCfg.AllowTokenQuery,
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
