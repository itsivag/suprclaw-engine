package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"

	"github.com/itsivag/suprclaw/pkg/config"
)

func TestHandleBrowserRelaySetupPersistsConfig(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	cfg.Tools.BrowserRelay.Enabled = false
	cfg.Tools.BrowserRelay.Token = ""
	cfg.Tools.BrowserRelay.Host = "127.0.0.1"
	if err = config.SaveConfig(configPath, cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodPost, "/api/browser-relay/setup", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	updated, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if !updated.Tools.BrowserRelay.Enabled {
		t.Fatal("tools.browser_relay.enabled should be true after setup")
	}
	if strings.TrimSpace(updated.Tools.BrowserRelay.Token) == "" {
		t.Fatal("tools.browser_relay.token should be generated during setup")
	}
}

func TestBrowserRelayTargetsRequiresAuth(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	cfg.Tools.BrowserRelay.Enabled = true
	cfg.Tools.BrowserRelay.Token = "relay-token"
	cfg.Tools.BrowserRelay.Host = "127.0.0.1"
	if err = config.SaveConfig(configPath, cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/api/browser-relay/targets", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}

	req2 := httptest.NewRequest(http.MethodGet, "/api/browser-relay/targets", nil)
	req2.Header.Set("Authorization", "Bearer relay-token")
	rec2 := httptest.NewRecorder()
	mux.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec2.Code, http.StatusOK, rec2.Body.String())
	}
}

func TestBrowserRelayCompatRoutesShape(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	cfg.Tools.BrowserRelay.Enabled = true
	cfg.Tools.BrowserRelay.Token = "relay-token"
	cfg.Tools.BrowserRelay.Host = "127.0.0.1"
	cfg.Tools.BrowserRelay.CompatOpenClaw = true
	if err = config.SaveConfig(configPath, cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	versionReq := httptest.NewRequest(http.MethodGet, "/json/version", nil)
	versionReq.Header.Set("Authorization", "Bearer relay-token")
	versionRec := httptest.NewRecorder()
	mux.ServeHTTP(versionRec, versionReq)
	if versionRec.Code != http.StatusOK {
		t.Fatalf("/json/version status = %d, want %d", versionRec.Code, http.StatusOK)
	}

	var versionPayload map[string]any
	if err = json.Unmarshal(versionRec.Body.Bytes(), &versionPayload); err != nil {
		t.Fatalf("version payload decode error: %v", err)
	}
	if versionPayload["Browser"] == "" {
		t.Fatalf("expected Browser in /json/version response: %v", versionPayload)
	}

	listReq := httptest.NewRequest(http.MethodGet, "/json/list", nil)
	listReq.Header.Set("Authorization", "Bearer relay-token")
	listRec := httptest.NewRecorder()
	mux.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("/json/list status = %d, want %d", listRec.Code, http.StatusOK)
	}

	var listPayload []map[string]any
	if err = json.Unmarshal(listRec.Body.Bytes(), &listPayload); err != nil {
		t.Fatalf("list payload decode error: %v", err)
	}
}

func TestBrowserRelayWSEnforcesSubprotocolToken(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	cfg.Tools.BrowserRelay.Enabled = true
	cfg.Tools.BrowserRelay.Token = "relay-token"
	cfg.Tools.BrowserRelay.Host = "127.0.0.1"
	if err = config.SaveConfig(configPath, cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	server := httptest.NewServer(mux)
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/browser-relay/extension"

	_, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err == nil {
		t.Fatal("expected unauthorized websocket dial to fail")
	}
	if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		if resp == nil {
			t.Fatalf("expected HTTP response for failed ws dial")
		}
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
	}

	dialer := *websocket.DefaultDialer
	dialer.Subprotocols = []string{"token.relay-token"}
	conn, resp2, err := dialer.Dial(wsURL, nil)
	if err != nil {
		if resp2 != nil {
			t.Fatalf("authorized ws dial failed: %v (status=%d)", err, resp2.StatusCode)
		}
		t.Fatalf("authorized ws dial failed: %v", err)
	}
	_ = conn.Close()
}

func TestBrowserRelaySetupAllowsTrustedBootstrapHeader(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	cfg.Tools.BrowserRelay.Enabled = true
	cfg.Tools.BrowserRelay.Token = "relay-token"
	cfg.Tools.BrowserRelay.Host = "127.0.0.1"
	cfg.Tools.BrowserRelay.BootstrapIdentityHeader = "X-Relay-Identity"
	if err = config.SaveConfig(configPath, cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodPost, "/api/browser-relay/setup", strings.NewReader(`{}`))
	req.Header.Set("X-Relay-Identity", "user-1")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
}

func TestBrowserRelayPairClaimAndHardStopWithSessionToken(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	cfg.Tools.BrowserRelay.Enabled = true
	cfg.Tools.BrowserRelay.Token = "relay-token"
	cfg.Tools.BrowserRelay.Host = "127.0.0.1"
	if err = config.SaveConfig(configPath, cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	createReq := httptest.NewRequest(http.MethodPost, "/api/browser-relay/pairing", strings.NewReader(`{}`))
	createReq.Header.Set("Authorization", "Bearer relay-token")
	createRec := httptest.NewRecorder()
	mux.ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusOK {
		t.Fatalf("pairing status = %d, want %d, body=%s", createRec.Code, http.StatusOK, createRec.Body.String())
	}

	var createResp map[string]any
	if err = json.Unmarshal(createRec.Body.Bytes(), &createResp); err != nil {
		t.Fatalf("pairing unmarshal error: %v", err)
	}
	code, _ := createResp["code"].(string)
	if code == "" {
		t.Fatalf("pairing code empty: %v", createResp)
	}

	claimReq := httptest.NewRequest(http.MethodPost, "/api/browser-relay/pairing/claim?code="+code, nil)
	claimRec := httptest.NewRecorder()
	mux.ServeHTTP(claimRec, claimReq)
	if claimRec.Code != http.StatusOK {
		t.Fatalf("claim status = %d, want %d, body=%s", claimRec.Code, http.StatusOK, claimRec.Body.String())
	}

	var claimResp map[string]any
	if err = json.Unmarshal(claimRec.Body.Bytes(), &claimResp); err != nil {
		t.Fatalf("claim unmarshal error: %v", err)
	}
	sessionToken, _ := claimResp["session_token"].(string)
	if sessionToken == "" {
		t.Fatalf("session_token empty: %v", claimResp)
	}

	targetsReq := httptest.NewRequest(http.MethodGet, "/api/browser-relay/targets", nil)
	targetsReq.Header.Set("Authorization", "Bearer "+sessionToken)
	targetsRec := httptest.NewRecorder()
	mux.ServeHTTP(targetsRec, targetsReq)
	if targetsRec.Code != http.StatusOK {
		t.Fatalf("targets status = %d, want %d, body=%s", targetsRec.Code, http.StatusOK, targetsRec.Body.String())
	}

	stopReq := httptest.NewRequest(http.MethodPost, "/api/browser-relay/session/stop", nil)
	stopReq.Header.Set("Authorization", "Bearer "+sessionToken)
	stopRec := httptest.NewRecorder()
	mux.ServeHTTP(stopRec, stopReq)
	if stopRec.Code != http.StatusOK {
		t.Fatalf("stop status = %d, want %d, body=%s", stopRec.Code, http.StatusOK, stopRec.Body.String())
	}

	targetsReq2 := httptest.NewRequest(http.MethodGet, "/api/browser-relay/targets", nil)
	targetsReq2.Header.Set("Authorization", "Bearer "+sessionToken)
	targetsRec2 := httptest.NewRecorder()
	mux.ServeHTTP(targetsRec2, targetsReq2)
	if targetsRec2.Code != http.StatusUnauthorized {
		t.Fatalf("targets-after-stop status = %d, want %d, body=%s", targetsRec2.Code, http.StatusUnauthorized, targetsRec2.Body.String())
	}
}
