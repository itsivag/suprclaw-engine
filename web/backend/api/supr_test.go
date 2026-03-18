package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/itsivag/suprclaw/pkg/config"
)

func TestEnsureSuprChannel_FreshConfig(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	h := NewHandler(configPath)

	changed, err := h.ensureSuprChannel("")
	if err != nil {
		t.Fatalf("ensureSuprChannel() error = %v", err)
	}
	if !changed {
		t.Fatal("ensureSuprChannel() should report changed on a fresh config")
	}

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}

	if !cfg.Channels.Supr.Enabled {
		t.Error("expected Supr to be enabled after setup")
	}
	if cfg.Channels.Supr.Token == "" {
		t.Error("expected a non-empty token after setup")
	}
}

func TestEnsureSuprChannel_DoesNotEnableTokenQuery(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	h := NewHandler(configPath)

	if _, err := h.ensureSuprChannel(""); err != nil {
		t.Fatalf("ensureSuprChannel() error = %v", err)
	}

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}

	if cfg.Channels.Supr.AllowTokenQuery {
		t.Error("setup must not enable allow_token_query by default")
	}
}

func TestEnsureSuprChannel_DoesNotSetWildcardOrigins(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	h := NewHandler(configPath)

	if _, err := h.ensureSuprChannel("http://localhost:18800"); err != nil {
		t.Fatalf("ensureSuprChannel() error = %v", err)
	}

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}

	for _, origin := range cfg.Channels.Supr.AllowOrigins {
		if origin == "*" {
			t.Error("setup must not set wildcard origin '*'")
		}
	}
}

func TestEnsureSuprChannel_NoOriginWithoutCaller(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	h := NewHandler(configPath)

	if _, err := h.ensureSuprChannel(""); err != nil {
		t.Fatalf("ensureSuprChannel() error = %v", err)
	}

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}

	// Without a caller origin, allow_origins stays empty (CheckOrigin
	// allows all when the list is empty, so the channel still works).
	if len(cfg.Channels.Supr.AllowOrigins) != 0 {
		t.Errorf("allow_origins = %v, want empty when no caller origin", cfg.Channels.Supr.AllowOrigins)
	}
}

func TestEnsureSuprChannel_SetsCallerOrigin(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	h := NewHandler(configPath)

	lanOrigin := "http://192.168.1.9:18800"
	if _, err := h.ensureSuprChannel(lanOrigin); err != nil {
		t.Fatalf("ensureSuprChannel() error = %v", err)
	}

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}

	if len(cfg.Channels.Supr.AllowOrigins) != 1 || cfg.Channels.Supr.AllowOrigins[0] != lanOrigin {
		t.Errorf("allow_origins = %v, want [%s]", cfg.Channels.Supr.AllowOrigins, lanOrigin)
	}
}

func TestEnsureSuprChannel_PreservesUserSettings(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")

	// Pre-configure with custom user settings
	cfg := config.DefaultConfig()
	cfg.Channels.Supr.Enabled = true
	cfg.Channels.Supr.Token = "user-custom-token"
	cfg.Channels.Supr.AllowTokenQuery = true
	cfg.Channels.Supr.AllowOrigins = []string{"https://myapp.example.com"}
	if err := config.SaveConfig(configPath, cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	h := NewHandler(configPath)

	changed, err := h.ensureSuprChannel("")
	if err != nil {
		t.Fatalf("ensureSuprChannel() error = %v", err)
	}
	if changed {
		t.Error("ensureSuprChannel() should not change a fully configured config")
	}

	cfg, err = config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}

	if cfg.Channels.Supr.Token != "user-custom-token" {
		t.Errorf("token = %q, want %q", cfg.Channels.Supr.Token, "user-custom-token")
	}
	if !cfg.Channels.Supr.AllowTokenQuery {
		t.Error("user's allow_token_query=true must be preserved")
	}
	if len(cfg.Channels.Supr.AllowOrigins) != 1 || cfg.Channels.Supr.AllowOrigins[0] != "https://myapp.example.com" {
		t.Errorf("allow_origins = %v, want [https://myapp.example.com]", cfg.Channels.Supr.AllowOrigins)
	}
}

func TestEnsureSuprChannel_Idempotent(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	h := NewHandler(configPath)

	origin := "http://localhost:18800"

	// First call sets things up
	if _, err := h.ensureSuprChannel(origin); err != nil {
		t.Fatalf("first ensureSuprChannel() error = %v", err)
	}

	cfg1, _ := config.LoadConfig(configPath)
	token1 := cfg1.Channels.Supr.Token

	// Second call should be a no-op
	changed, err := h.ensureSuprChannel(origin)
	if err != nil {
		t.Fatalf("second ensureSuprChannel() error = %v", err)
	}
	if changed {
		t.Error("second ensureSuprChannel() should not report changed")
	}

	cfg2, _ := config.LoadConfig(configPath)
	if cfg2.Channels.Supr.Token != token1 {
		t.Error("token should not change on subsequent calls")
	}
}

func TestHandleSuprSetup_IncludesRequestOrigin(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	h := NewHandler(configPath)

	req := httptest.NewRequest("POST", "/api/supr/setup", nil)
	req.Header.Set("Origin", "http://10.0.0.5:3000")
	rec := httptest.NewRecorder()

	h.handleSuprSetup(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}

	if len(cfg.Channels.Supr.AllowOrigins) != 1 || cfg.Channels.Supr.AllowOrigins[0] != "http://10.0.0.5:3000" {
		t.Errorf("allow_origins = %v, want [http://10.0.0.5:3000]", cfg.Channels.Supr.AllowOrigins)
	}
}

func TestHandleSuprSetup_Response(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	h := NewHandler(configPath)

	req := httptest.NewRequest("POST", "/api/supr/setup", nil)
	rec := httptest.NewRecorder()

	h.handleSuprSetup(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var resp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp["token"] == nil || resp["token"] == "" {
		t.Error("response should contain a non-empty token")
	}
	if resp["ws_url"] == nil || resp["ws_url"] == "" {
		t.Error("response should contain ws_url")
	}
	if resp["enabled"] != true {
		t.Error("response should have enabled=true")
	}
	if resp["changed"] != true {
		t.Error("response should have changed=true on first setup")
	}
}

func TestHandleWebSocketProxyReloadsGatewayTargetFromConfig(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	h := NewHandler(configPath)
	handler := h.handleWebSocketProxy()

	server1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/supr/ws" {
			t.Fatalf("server1 path = %q, want %q", r.URL.Path, "/supr/ws")
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "server1")
	}))
	defer server1.Close()

	server2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/supr/ws" {
			t.Fatalf("server2 path = %q, want %q", r.URL.Path, "/supr/ws")
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "server2")
	}))
	defer server2.Close()

	cfg := config.DefaultConfig()
	cfg.Gateway.Host = "127.0.0.1"
	cfg.Gateway.Port = mustGatewayTestPort(t, server1.URL)
	if err := config.SaveConfig(configPath, cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	req1 := httptest.NewRequest(http.MethodGet, "/supr/ws", nil)
	rec1 := httptest.NewRecorder()
	handler(rec1, req1)

	if rec1.Code != http.StatusOK {
		t.Fatalf("first status = %d, want %d", rec1.Code, http.StatusOK)
	}
	if body := rec1.Body.String(); body != "server1" {
		t.Fatalf("first body = %q, want %q", body, "server1")
	}

	cfg.Gateway.Port = mustGatewayTestPort(t, server2.URL)
	if err := config.SaveConfig(configPath, cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	req2 := httptest.NewRequest(http.MethodGet, "/supr/ws", nil)
	rec2 := httptest.NewRecorder()
	handler(rec2, req2)

	if rec2.Code != http.StatusOK {
		t.Fatalf("second status = %d, want %d", rec2.Code, http.StatusOK)
	}
	if body := rec2.Body.String(); body != "server2" {
		t.Fatalf("second body = %q, want %q", body, "server2")
	}
}

func mustGatewayTestPort(t *testing.T, rawURL string) int {
	t.Helper()

	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("url.Parse() error = %v", err)
	}

	port, err := strconv.Atoi(parsed.Port())
	if err != nil {
		t.Fatalf("Atoi(%q) error = %v", parsed.Port(), err)
	}

	return port
}
