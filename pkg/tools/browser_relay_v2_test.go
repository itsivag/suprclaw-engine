package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/itsivag/suprclaw/pkg/config"
)

func TestResolveBrowserRelayV2Options_StrictDefault(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Tools.BrowserRelay.Enabled = true
	cfg.Tools.BrowserRelay.Token = "relay-token"
	cfg.Tools.MCP.Servers = map[string]config.MCPServerConfig{
		"browser_relay": {
			Enabled: true,
			Type:    "http",
			URL:     "https://api.suprclaw.com/api/mcp/browser-relay",
		},
	}

	opts, ok := ResolveBrowserRelayV2Options(cfg)
	if !ok {
		t.Fatal("ResolveBrowserRelayV2Options() ok = false, want true")
	}
	if opts.ActionsURL != defaultBrowserRelayV2ActionsURL {
		t.Fatalf("ActionsURL = %q, want %q", opts.ActionsURL, defaultBrowserRelayV2ActionsURL)
	}
	if got := opts.Headers["Authorization"]; got != "Bearer relay-token" {
		t.Fatalf("Authorization header = %q, want Bearer relay-token", got)
	}
}

func TestBrowserRelayV2ActionTool_ExecuteCallsV2(t *testing.T) {
	var gotBody map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		if r.URL.Path != "/api/browser-relay/actions" {
			t.Fatalf("path = %q, want /api/browser-relay/actions", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"request_id": "req-1",
			"ok":         true,
			"result": map[string]any{
				"ok": true,
			},
		})
	}))
	defer ts.Close()

	tool := &browserRelayV2ActionTool{
		actionsURL: ts.URL + "/api/browser-relay/actions",
		headers:    map[string]string{"Authorization": "Bearer abc"},
		client:     ts.Client(),
	}

	result := tool.Execute(context.Background(), map[string]any{
		"request_id": "req-1",
		"target":     "ext:394834123",
		"action":     "navigate",
		"args": map[string]any{
			"url": "https://www.amazon.in/",
		},
	})
	if result == nil || result.IsError {
		t.Fatalf("Execute() error = %v, for_llm=%s", result.Err, result.ForLLM)
	}
	if got, _ := gotBody["action"].(string); got != "navigate" {
		t.Fatalf("action = %q, want navigate", got)
	}
	if got, _ := gotBody["target"].(string); got != "ext:394834123" {
		t.Fatalf("target = %q, want ext:394834123", got)
	}
	args, _ := gotBody["args"].(map[string]any)
	if got, _ := args["url"].(string); got != "https://www.amazon.in/" {
		t.Fatalf("args.url = %q", got)
	}
}

func TestBrowserRelayV2Tools_NamesAreV2Only(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Tools.BrowserRelay.Enabled = true
	tools := NewBrowserRelayV2Tools(cfg)
	if len(tools) != 3 {
		t.Fatalf("len(tools) = %d, want 3", len(tools))
	}
	names := map[string]bool{}
	for _, tool := range tools {
		names[tool.Name()] = true
	}
	for _, want := range []string{
		"browser_relay_v2_targets_list",
		"browser_relay_v2_action",
		"browser_relay_v2_batch",
	} {
		if !names[want] {
			t.Fatalf("missing tool %q", want)
		}
	}
}

