package api

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/skip2/go-qrcode"

	"github.com/itsivag/suprclaw/pkg/config"
)

const (
	browserRelayLeaseStateFile   = "browser-relay-state.json"
	browserRelayLeaseActive      = "active"
	browserRelayLeaseStopped     = "stopped"
	browserRelayTokenTypeSession = "session"
	browserRelayTokenTypeRefresh = "refresh"
)

type browserRelayLeaseState struct {
	LeaseID       string                              `json:"lease_id"`
	LeaseState    string                              `json:"lease_state"`
	TokenVersion  int64                               `json:"token_version"`
	LastSeenAt    time.Time                           `json:"last_seen_at,omitempty"`
	HardStoppedAt time.Time                           `json:"hard_stopped_at,omitempty"`
	PairingCodes  map[string]browserRelayPairingCode  `json:"pairing_codes,omitempty"`
	RefreshTokens map[string]browserRelayRefreshToken `json:"refresh_tokens,omitempty"`
}

type browserRelayPairingCode struct {
	Code      string    `json:"code"`
	Subject   string    `json:"subject,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

type browserRelayRefreshToken struct {
	JTI       string    `json:"jti"`
	LeaseID   string    `json:"lease_id"`
	Subject   string    `json:"subject,omitempty"`
	Version   int64     `json:"version"`
	IssuedAt  time.Time `json:"issued_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

type browserRelayTokenClaims struct {
	Type    string `json:"typ"`
	LeaseID string `json:"lease_id"`
	Subject string `json:"sub,omitempty"`
	Version int64  `json:"ver"`
	Issued  int64  `json:"iat"`
	Expires int64  `json:"exp"`
	JTI     string `json:"jti,omitempty"`
}

func (h *Handler) browserRelayStatePath() string {
	return filepath.Join(filepath.Dir(h.configPath), browserRelayLeaseStateFile)
}

func loadBrowserRelayLeaseState(path string) (browserRelayLeaseState, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return browserRelayLeaseState{}, nil
		}
		return browserRelayLeaseState{}, err
	}

	var state browserRelayLeaseState
	if err := json.Unmarshal(data, &state); err != nil {
		return browserRelayLeaseState{}, err
	}
	return state, nil
}

func saveBrowserRelayLeaseState(path string, state browserRelayLeaseState) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func normalizeBrowserRelayLeaseState(state *browserRelayLeaseState, now time.Time) {
	if strings.TrimSpace(state.LeaseID) == "" {
		state.LeaseID = uuid.NewString()
	}
	if strings.TrimSpace(state.LeaseState) == "" {
		state.LeaseState = browserRelayLeaseActive
	}
	if state.TokenVersion <= 0 {
		state.TokenVersion = 1
	}
	if state.PairingCodes == nil {
		state.PairingCodes = make(map[string]browserRelayPairingCode)
	}
	if state.RefreshTokens == nil {
		state.RefreshTokens = make(map[string]browserRelayRefreshToken)
	}
	for code, pairing := range state.PairingCodes {
		if !pairing.ExpiresAt.IsZero() && now.After(pairing.ExpiresAt) {
			delete(state.PairingCodes, code)
		}
	}
	for jti, refresh := range state.RefreshTokens {
		if !refresh.ExpiresAt.IsZero() && now.After(refresh.ExpiresAt) {
			delete(state.RefreshTokens, jti)
		}
	}
}

func (h *Handler) withBrowserRelayLeaseState(
	mutator func(*browserRelayLeaseState) error,
) (browserRelayLeaseState, error) {
	h.browserRelayStateMu.Lock()
	defer h.browserRelayStateMu.Unlock()

	path := h.browserRelayStatePath()
	state, err := loadBrowserRelayLeaseState(path)
	if err != nil {
		return browserRelayLeaseState{}, err
	}

	normalizeBrowserRelayLeaseState(&state, time.Now().UTC())
	if mutator != nil {
		if err := mutator(&state); err != nil {
			return browserRelayLeaseState{}, err
		}
	}
	if err := saveBrowserRelayLeaseState(path, state); err != nil {
		return browserRelayLeaseState{}, err
	}
	return state, nil
}

func (h *Handler) browserRelayEnsureConfigured(cfg *config.Config) (config.BrowserRelayConfig, bool, error) {
	relayCfg := normalizeBrowserRelayConfig(cfg.Tools.BrowserRelay)
	changed := false
	if !relayCfg.Enabled {
		relayCfg.Enabled = true
		changed = true
	}
	if strings.TrimSpace(relayCfg.Token) == "" {
		relayCfg.Token = generateSecureToken()
		changed = true
	}
	if !isLoopbackHost(relayCfg.Host) {
		return config.BrowserRelayConfig{}, false, errors.New("tools.browser_relay.host must be loopback")
	}

	cfg.Tools.BrowserRelay = relayCfg
	if changed {
		if err := config.SaveConfig(h.configPath, cfg); err != nil {
			return config.BrowserRelayConfig{}, false, err
		}
	}

	manager := h.browserRelayManager(cfg)
	manager.ApplyConfig(browserRelayConfigFromConfig(cfg))
	return relayCfg, changed, nil
}

func (h *Handler) browserRelayRequestSubject(r *http.Request, relayCfg config.BrowserRelayConfig) string {
	header := strings.TrimSpace(relayCfg.BootstrapIdentityHeader)
	if header == "" {
		return ""
	}
	return strings.TrimSpace(r.Header.Get(header))
}

func (h *Handler) isBrowserRelayBootstrapAuthorized(r *http.Request, cfg *config.Config) bool {
	if h.isBrowserRelayHTTPAuthorized(r, cfg) {
		return true
	}
	relayCfg := normalizeBrowserRelayConfig(cfg.Tools.BrowserRelay)
	return h.browserRelayRequestSubject(r, relayCfg) != ""
}

func browserRelayTokenKey(relayToken string) []byte {
	return []byte("suprclaw-browser-relay-v1:" + relayToken)
}

func signBrowserRelayToken(claims browserRelayTokenClaims, key []byte) (string, error) {
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(payload)
	signature := mac.Sum(nil)
	return base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func parseBrowserRelayToken(token string, key []byte) (browserRelayTokenClaims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return browserRelayTokenClaims{}, errors.New("invalid token format")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return browserRelayTokenClaims{}, errors.New("invalid token payload")
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return browserRelayTokenClaims{}, errors.New("invalid token signature")
	}

	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(payload)
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return browserRelayTokenClaims{}, errors.New("token signature mismatch")
	}

	var claims browserRelayTokenClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return browserRelayTokenClaims{}, errors.New("invalid token claims")
	}
	return claims, nil
}

func generateBrowserRelayCode(bytesCount int) string {
	buf := make([]byte, bytesCount)
	if _, err := rand.Read(buf); err != nil {
		return uuid.NewString()
	}
	return strings.ToUpper(hex.EncodeToString(buf))
}

func (h *Handler) issueBrowserRelaySessionCredentials(
	cfg *config.Config,
	state *browserRelayLeaseState,
	subject string,
	now time.Time,
) (string, string, time.Time, time.Time, error) {
	relayCfg := normalizeBrowserRelayConfig(cfg.Tools.BrowserRelay)
	relayToken := strings.TrimSpace(relayCfg.Token)
	if relayToken == "" {
		return "", "", time.Time{}, time.Time{}, errors.New("relay token is not configured")
	}

	sessionExpiry := now.Add(time.Duration(relayCfg.SessionTokenTTLSec) * time.Second)
	refreshExpiry := now.Add(time.Duration(relayCfg.RefreshTokenTTLSec) * time.Second)
	jti := generateBrowserRelayCode(16)

	key := browserRelayTokenKey(relayToken)
	sessionToken, err := signBrowserRelayToken(browserRelayTokenClaims{
		Type:    browserRelayTokenTypeSession,
		LeaseID: state.LeaseID,
		Subject: subject,
		Version: state.TokenVersion,
		Issued:  now.Unix(),
		Expires: sessionExpiry.Unix(),
	}, key)
	if err != nil {
		return "", "", time.Time{}, time.Time{}, err
	}

	refreshToken, err := signBrowserRelayToken(browserRelayTokenClaims{
		Type:    browserRelayTokenTypeRefresh,
		LeaseID: state.LeaseID,
		Subject: subject,
		Version: state.TokenVersion,
		Issued:  now.Unix(),
		Expires: refreshExpiry.Unix(),
		JTI:     jti,
	}, key)
	if err != nil {
		return "", "", time.Time{}, time.Time{}, err
	}

	state.RefreshTokens[jti] = browserRelayRefreshToken{
		JTI:       jti,
		LeaseID:   state.LeaseID,
		Subject:   subject,
		Version:   state.TokenVersion,
		IssuedAt:  now,
		ExpiresAt: refreshExpiry,
	}
	state.LastSeenAt = now

	return sessionToken, refreshToken, sessionExpiry, refreshExpiry, nil
}

func (h *Handler) browserRelaySessionClaimsFromRequest(
	r *http.Request,
	cfg *config.Config,
) (*browserRelayTokenClaims, error) {
	token := strings.TrimSpace(extractBearerToken(r.Header.Get("Authorization")))
	if token == "" {
		return nil, errors.New("missing bearer token")
	}

	relayCfg := normalizeBrowserRelayConfig(cfg.Tools.BrowserRelay)
	if strings.TrimSpace(relayCfg.Token) == "" {
		return nil, errors.New("relay token is not configured")
	}

	claims, err := parseBrowserRelayToken(token, browserRelayTokenKey(relayCfg.Token))
	if err != nil {
		return nil, err
	}
	if claims.Type != browserRelayTokenTypeSession {
		return nil, errors.New("token is not a session token")
	}
	if claims.Expires <= time.Now().Unix() {
		return nil, errors.New("session token expired")
	}

	state, err := h.withBrowserRelayLeaseState(nil)
	if err != nil {
		return nil, err
	}
	if state.LeaseState == browserRelayLeaseStopped {
		return nil, errors.New("relay lease is stopped")
	}
	if claims.LeaseID != state.LeaseID || claims.Version != state.TokenVersion {
		return nil, errors.New("session token revoked")
	}
	return &claims, nil
}

func (h *Handler) browserRelayRefreshToken(
	cfg *config.Config,
	refreshToken string,
) (string, string, time.Time, time.Time, *browserRelayTokenClaims, error) {
	relayCfg := normalizeBrowserRelayConfig(cfg.Tools.BrowserRelay)
	if strings.TrimSpace(relayCfg.Token) == "" {
		return "", "", time.Time{}, time.Time{}, nil, errors.New("relay token is not configured")
	}

	claims, err := parseBrowserRelayToken(strings.TrimSpace(refreshToken), browserRelayTokenKey(relayCfg.Token))
	if err != nil {
		return "", "", time.Time{}, time.Time{}, nil, err
	}
	if claims.Type != browserRelayTokenTypeRefresh {
		return "", "", time.Time{}, time.Time{}, nil, errors.New("token is not a refresh token")
	}
	if claims.JTI == "" {
		return "", "", time.Time{}, time.Time{}, nil, errors.New("refresh token jti is missing")
	}
	if claims.Expires <= time.Now().Unix() {
		return "", "", time.Time{}, time.Time{}, nil, errors.New("refresh token expired")
	}

	var (
		sessionToken string
		nextRefresh  string
		sessionExp   time.Time
		refreshExp   time.Time
	)
	now := time.Now().UTC()
	state, err := h.withBrowserRelayLeaseState(func(state *browserRelayLeaseState) error {
		if state.LeaseState == browserRelayLeaseStopped {
			return errors.New("relay lease is stopped")
		}
		if claims.LeaseID != state.LeaseID || claims.Version != state.TokenVersion {
			return errors.New("refresh token revoked")
		}
		record, ok := state.RefreshTokens[claims.JTI]
		if !ok {
			return errors.New("refresh token has been rotated")
		}
		if !record.ExpiresAt.IsZero() && now.After(record.ExpiresAt) {
			delete(state.RefreshTokens, claims.JTI)
			return errors.New("refresh token expired")
		}
		delete(state.RefreshTokens, claims.JTI)
		sessionToken, nextRefresh, sessionExp, refreshExp, err = h.issueBrowserRelaySessionCredentials(
			cfg, state, claims.Subject, now)
		return err
	})
	if err != nil {
		return "", "", time.Time{}, time.Time{}, nil, err
	}

	claims.LeaseID = state.LeaseID
	claims.Version = state.TokenVersion
	return sessionToken, nextRefresh, sessionExp, refreshExp, &claims, nil
}

func browserRelayHTTPScheme(r *http.Request) string {
	if requestWSScheme(r) == "wss" {
		return "https"
	}
	return "http"
}

func (h *Handler) browserRelayPairingClaimURL(r *http.Request, code string) string {
	return browserRelayHTTPScheme(r) + "://" + r.Host + "/api/browser-relay/pairing/claim?code=" + url.QueryEscape(code)
}

func (h *Handler) handleBrowserRelayPairing(w http.ResponseWriter, r *http.Request) {
	cfg, err := config.LoadConfig(h.configPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to load config: %v", err), http.StatusInternalServerError)
		return
	}
	if !h.isBrowserRelayBootstrapAuthorized(r, cfg) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	relayCfg, _, err := h.browserRelayEnsureConfigured(cfg)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var payload struct {
		TTLSeconds int `json:"ttl_seconds"`
	}
	if r.Body != nil && r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil && !errors.Is(err, io.EOF) {
			http.Error(w, "invalid JSON payload", http.StatusBadRequest)
			return
		}
	}

	ttlSec := payload.TTLSeconds
	if ttlSec <= 0 {
		ttlSec = relayCfg.PairingCodeTTLSec
	}
	if ttlSec > 900 {
		ttlSec = 900
	}

	subject := h.browserRelayRequestSubject(r, relayCfg)
	now := time.Now().UTC()
	expiresAt := now.Add(time.Duration(ttlSec) * time.Second)
	code := generateBrowserRelayCode(4)

	if _, err := h.withBrowserRelayLeaseState(func(state *browserRelayLeaseState) error {
		state.PairingCodes[code] = browserRelayPairingCode{
			Code:      code,
			Subject:   subject,
			CreatedAt: now,
			ExpiresAt: expiresAt,
		}
		if state.LeaseState == "" {
			state.LeaseState = browserRelayLeaseActive
		}
		return nil
	}); err != nil {
		http.Error(w, fmt.Sprintf("failed to persist pairing code: %v", err), http.StatusInternalServerError)
		return
	}

	claimURL := h.browserRelayPairingClaimURL(r, code)
	qrURL := browserRelayHTTPScheme(r) + "://" + r.Host + "/api/browser-relay/pairing/qr/" + url.PathEscape(code)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"code":       code,
		"claim_url":  claimURL,
		"qr_svg_url": qrURL,
		"expires_at": expiresAt.Format(time.RFC3339),
	})
}

func (h *Handler) handleBrowserRelayPairingQR(w http.ResponseWriter, r *http.Request) {
	code := strings.TrimSpace(r.PathValue("code"))
	if code == "" {
		http.Error(w, "missing pairing code", http.StatusBadRequest)
		return
	}

	state, err := h.withBrowserRelayLeaseState(nil)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to load pairing state: %v", err), http.StatusInternalServerError)
		return
	}
	pairing, ok := state.PairingCodes[code]
	if !ok || time.Now().UTC().After(pairing.ExpiresAt) {
		http.Error(w, "pairing code expired", http.StatusGone)
		return
	}

	claimURL := h.browserRelayPairingClaimURL(r, code)
	png, err := qrcode.Encode(claimURL, qrcode.Medium, 256)
	if err != nil {
		http.Error(w, "failed to encode QR", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(png)
}

func (h *Handler) handleBrowserRelayPairingClaim(w http.ResponseWriter, r *http.Request) {
	cfg, err := config.LoadConfig(h.configPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to load config: %v", err), http.StatusInternalServerError)
		return
	}

	relayCfg := normalizeBrowserRelayConfig(cfg.Tools.BrowserRelay)
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	if code == "" {
		var payload struct {
			Code string `json:"code"`
		}
		if decodeErr := json.NewDecoder(r.Body).Decode(&payload); decodeErr == nil {
			code = strings.TrimSpace(payload.Code)
		}
	}
	if code == "" {
		http.Error(w, "pairing code is required", http.StatusBadRequest)
		return
	}

	subject := h.browserRelayRequestSubject(r, relayCfg)
	now := time.Now().UTC()
	var (
		sessionToken string
		refreshToken string
		sessionExp   time.Time
		refreshExp   time.Time
		leaseID      string
	)

	_, err = h.withBrowserRelayLeaseState(func(state *browserRelayLeaseState) error {
		pairing, ok := state.PairingCodes[code]
		if !ok {
			return errors.New("pairing code not found")
		}
		if now.After(pairing.ExpiresAt) {
			delete(state.PairingCodes, code)
			return errors.New("pairing code expired")
		}
		if pairing.Subject != "" && subject != pairing.Subject {
			return errors.New("pairing code subject mismatch")
		}
		delete(state.PairingCodes, code)
		if state.LeaseState == browserRelayLeaseStopped {
			state.LeaseState = browserRelayLeaseActive
			state.HardStoppedAt = time.Time{}
		}
		sessionToken, refreshToken, sessionExp, refreshExp, err = h.issueBrowserRelaySessionCredentials(
			cfg, state, subject, now)
		if err != nil {
			return err
		}
		leaseID = state.LeaseID
		return nil
	})
	if err != nil {
		switch {
		case strings.Contains(err.Error(), "expired"):
			http.Error(w, err.Error(), http.StatusGone)
		case strings.Contains(err.Error(), "subject mismatch"):
			http.Error(w, "forbidden", http.StatusForbidden)
		default:
			http.Error(w, err.Error(), http.StatusBadRequest)
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"lease_id":            leaseID,
		"session_token":       sessionToken,
		"refresh_token":       refreshToken,
		"session_expires_at":  sessionExp.Format(time.RFC3339),
		"refresh_expires_at":  refreshExp.Format(time.RFC3339),
		"extension_ws_url":    h.browserRelayExtensionURL(r, relayCfg),
		"cdp_ws_url_template": h.browserRelayCDPTemplateURL(r, relayCfg),
	})
}

func (h *Handler) handleBrowserRelaySessionState(w http.ResponseWriter, r *http.Request) {
	cfg, err := config.LoadConfig(h.configPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to load config: %v", err), http.StatusInternalServerError)
		return
	}
	relayCfg := normalizeBrowserRelayConfig(cfg.Tools.BrowserRelay)
	if !relayCfg.Enabled {
		http.Error(w, "browser relay is disabled", http.StatusServiceUnavailable)
		return
	}
	if !h.isBrowserRelayHTTPAuthorized(r, cfg) {
		if _, tokenErr := h.browserRelaySessionClaimsFromRequest(r, cfg); tokenErr != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
	}

	state, err := h.withBrowserRelayLeaseState(nil)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to load lease state: %v", err), http.StatusInternalServerError)
		return
	}
	status := h.browserRelayManager(cfg).Status()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"lease_state":     state.LeaseState,
		"lease_id":        state.LeaseID,
		"connected":       status.ConnectedExtensions > 0,
		"last_seen_at":    state.LastSeenAt.Format(time.RFC3339),
		"hard_stopped_at": state.HardStoppedAt.Format(time.RFC3339),
	})
}

func (h *Handler) handleBrowserRelaySessionRefresh(w http.ResponseWriter, r *http.Request) {
	cfg, err := config.LoadConfig(h.configPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to load config: %v", err), http.StatusInternalServerError)
		return
	}

	var payload struct {
		RefreshToken string `json:"refresh_token"`
	}
	_ = json.NewDecoder(r.Body).Decode(&payload)
	refreshToken := strings.TrimSpace(payload.RefreshToken)
	if refreshToken == "" {
		refreshToken = strings.TrimSpace(extractBearerToken(r.Header.Get("Authorization")))
	}
	if refreshToken == "" {
		http.Error(w, "refresh token is required", http.StatusBadRequest)
		return
	}

	sessionToken, nextRefresh, sessionExp, refreshExp, claims, err := h.browserRelayRefreshToken(cfg, refreshToken)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}

	relayCfg := normalizeBrowserRelayConfig(cfg.Tools.BrowserRelay)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"lease_id":            claims.LeaseID,
		"session_token":       sessionToken,
		"refresh_token":       nextRefresh,
		"session_expires_at":  sessionExp.Format(time.RFC3339),
		"refresh_expires_at":  refreshExp.Format(time.RFC3339),
		"extension_ws_url":    h.browserRelayExtensionURL(r, relayCfg),
		"cdp_ws_url_template": h.browserRelayCDPTemplateURL(r, relayCfg),
	})
}

func (h *Handler) handleBrowserRelaySessionStop(w http.ResponseWriter, r *http.Request) {
	cfg, err := config.LoadConfig(h.configPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to load config: %v", err), http.StatusInternalServerError)
		return
	}

	authorized := h.isBrowserRelayHTTPAuthorized(r, cfg)
	if !authorized {
		if _, sessionErr := h.browserRelaySessionClaimsFromRequest(r, cfg); sessionErr == nil {
			authorized = true
		}
	}
	if !authorized {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	now := time.Now().UTC()
	state, err := h.withBrowserRelayLeaseState(func(state *browserRelayLeaseState) error {
		state.LeaseState = browserRelayLeaseStopped
		state.HardStoppedAt = now
		state.TokenVersion++
		state.PairingCodes = make(map[string]browserRelayPairingCode)
		state.RefreshTokens = make(map[string]browserRelayRefreshToken)
		return nil
	})
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to persist lease stop: %v", err), http.StatusInternalServerError)
		return
	}

	h.browserRelayMu.Lock()
	if h.browserRelay != nil {
		h.browserRelay.Close()
		h.browserRelay = nil
	}
	h.browserRelayMu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok":          true,
		"lease_id":    state.LeaseID,
		"lease_state": state.LeaseState,
		"stopped_at":  state.HardStoppedAt.Format(time.RFC3339),
	})
}
