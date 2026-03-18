package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httputil"
	"time"

	"github.com/itsivag/suprclaw/pkg/config"
)

// registerSuprRoutes binds Supr Channel management endpoints to the ServeMux.
func (h *Handler) registerSuprRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/supr/token", h.handleGetSuprToken)
	mux.HandleFunc("POST /api/supr/token", h.handleRegenSuprToken)
	mux.HandleFunc("POST /api/supr/setup", h.handleSuprSetup)

	// WebSocket proxy: forward /supr/ws to gateway
	// This allows the frontend to connect via the same port as the web UI,
	// avoiding the need to expose extra ports for WebSocket communication.
	mux.HandleFunc("GET /supr/ws", h.handleWebSocketProxy())
}

// createWsProxy creates a reverse proxy to the current gateway WebSocket endpoint.
// The gateway bind host and port are resolved from the latest configuration.
func (h *Handler) createWsProxy() *httputil.ReverseProxy {
	wsProxy := httputil.NewSingleHostReverseProxy(h.gatewayProxyURL())
	wsProxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		http.Error(w, "Gateway unavailable: "+err.Error(), http.StatusBadGateway)
	}
	return wsProxy
}

// handleWebSocketProxy wraps a reverse proxy to handle WebSocket connections.
// The reverse proxy forwards the incoming upgrade handshake as-is.
func (h *Handler) handleWebSocketProxy() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		proxy := h.createWsProxy()
		proxy.ServeHTTP(w, r)
	}
}

// handleGetSuprToken returns the current WS token and URL for the frontend.
//
//	GET /api/supr/token
func (h *Handler) handleGetSuprToken(w http.ResponseWriter, r *http.Request) {
	cfg, err := config.LoadConfig(h.configPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to load config: %v", err), http.StatusInternalServerError)
		return
	}

	wsURL := h.buildWsURL(r, cfg)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"token":   cfg.Channels.Supr.Token,
		"ws_url":  wsURL,
		"enabled": cfg.Channels.Supr.Enabled,
	})
}

// handleRegenSuprToken generates a new Supr WebSocket token and saves it.
//
//	POST /api/supr/token
func (h *Handler) handleRegenSuprToken(w http.ResponseWriter, r *http.Request) {
	cfg, err := config.LoadConfig(h.configPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to load config: %v", err), http.StatusInternalServerError)
		return
	}

	token := generateSecureToken()
	cfg.Channels.Supr.Token = token

	if err := config.SaveConfig(h.configPath, cfg); err != nil {
		http.Error(w, fmt.Sprintf("Failed to save config: %v", err), http.StatusInternalServerError)
		return
	}

	wsURL := h.buildWsURL(r, cfg)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"token":  token,
		"ws_url": wsURL,
	})
}

// ensureSuprChannel enables the Supr channel with sane defaults if it isn't
// already configured. Returns true when the config was modified.
//
// callerOrigin is the Origin header from the setup request. If non-empty and
// no origins are configured yet, it's written as the allowed origin so the
// WebSocket handshake works for whatever host the caller is on (LAN, custom
// port, etc.). Pass "" when there's no request context.
func (h *Handler) ensureSuprChannel(callerOrigin string) (bool, error) {
	cfg, err := config.LoadConfig(h.configPath)
	if err != nil {
		return false, fmt.Errorf("failed to load config: %w", err)
	}

	changed := false

	if !cfg.Channels.Supr.Enabled {
		cfg.Channels.Supr.Enabled = true
		changed = true
	}

	if cfg.Channels.Supr.Token == "" {
		cfg.Channels.Supr.Token = generateSecureToken()
		changed = true
	}

	// Seed origins from the request instead of hardcoding ports.
	if len(cfg.Channels.Supr.AllowOrigins) == 0 && callerOrigin != "" {
		cfg.Channels.Supr.AllowOrigins = []string{callerOrigin}
		changed = true
	}

	if changed {
		if err := config.SaveConfig(h.configPath, cfg); err != nil {
			return false, fmt.Errorf("failed to save config: %w", err)
		}
	}

	return changed, nil
}

// handleSuprSetup automatically configures everything needed for the Supr Channel to work.
//
//	POST /api/supr/setup
func (h *Handler) handleSuprSetup(w http.ResponseWriter, r *http.Request) {
	changed, err := h.ensureSuprChannel(r.Header.Get("Origin"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	cfg, err := config.LoadConfig(h.configPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to load config: %v", err), http.StatusInternalServerError)
		return
	}

	wsURL := h.buildWsURL(r, cfg)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"token":   cfg.Channels.Supr.Token,
		"ws_url":  wsURL,
		"enabled": true,
		"changed": changed,
	})
}

// generateSecureToken creates a random 32-character hex string.
func generateSecureToken() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// Fallback to something pseudo-random if crypto/rand fails
		return fmt.Sprintf("supr_%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}
