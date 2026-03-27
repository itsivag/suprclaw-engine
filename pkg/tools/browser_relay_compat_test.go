package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/itsivag/suprclaw/pkg/config"
)

func TestDeriveBrowserRelayActionsURL(t *testing.T) {
	got := deriveBrowserRelayActionsURL("https://api.suprclaw.com/api/mcp/browser-relay")
	want := "https://api.suprclaw.com/api/browser-relay/actions"
	if got != want {
		t.Fatalf("deriveBrowserRelayActionsURL() = %q, want %q", got, want)
	}
}

func TestResolveBrowserRelayCompatOptions_FromMCPServer(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Tools.BrowserRelay.Enabled = true
	cfg.Tools.MCP.Servers = map[string]config.MCPServerConfig{
		"browser_relay": {
			Enabled: true,
			Type:    "http",
			URL:     "https://api.suprclaw.com/api/mcp/browser-relay",
			Headers: map[string]string{"Authorization": "Bearer abc"},
		},
	}

	opts, ok := ResolveBrowserRelayCompatOptions(cfg)
	if !ok {
		t.Fatal("ResolveBrowserRelayCompatOptions() ok = false, want true")
	}
	if opts.ActionsURL != "https://api.suprclaw.com/api/browser-relay/actions" {
		t.Fatalf("ActionsURL = %q", opts.ActionsURL)
	}
	if got := opts.Headers["Authorization"]; got != "Bearer abc" {
		t.Fatalf("Authorization header = %q, want Bearer abc", got)
	}
}

func TestBrowserRelayCompatTool_ExecuteNavigateCallsV2(t *testing.T) {
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
			"request_id": "r1",
			"ok":         true,
			"result": map[string]any{
				"ok": true,
			},
		})
	}))
	defer ts.Close()

	tool := &browserRelayCompatTool{
		name:       "mcp_browser_relay_browser_relay_navigate",
		action:     "navigate",
		actionsURL: ts.URL + "/api/browser-relay/actions",
		headers:    map[string]string{"Authorization": "Bearer abc"},
		client:     ts.Client(),
	}

	result := tool.Execute(context.Background(), map[string]any{
		"targetId": "394834123",
		"url":      "https://www.amazon.in/",
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
