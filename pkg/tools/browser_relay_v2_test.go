package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/itsivag/suprclaw/pkg/config"
)

func TestResolveBrowserRelayV2Options_UsesConfiguredMCPServerURL(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Tools.BrowserRelay.Enabled = true
	cfg.Tools.MCP.Servers = map[string]config.MCPServerConfig{
		"browser_relay": {
			Enabled: true,
			Type:    "http",
			URL:     "https://api.suprclaw.com/api/mcp/browser-relay",
			Headers: map[string]string{
				"Authorization": "Bearer gateway-token",
			},
		},
	}

	opts, ok := ResolveBrowserRelayV2Options(cfg)
	if !ok {
		t.Fatal("ResolveBrowserRelayV2Options() ok = false, want true")
	}
	if opts.EndpointURL != "https://api.suprclaw.com/api/mcp/browser-relay" {
		t.Fatalf("EndpointURL = %q", opts.EndpointURL)
	}
	if got := opts.Headers["Authorization"]; got != "Bearer gateway-token" {
		t.Fatalf("Authorization header = %q, want Bearer gateway-token", got)
	}
}

func TestResolveBrowserRelayV2Options_FailsWhenMCPBrowserRelayURLMissing(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Tools.BrowserRelay.Enabled = true
	cfg.Tools.MCP.Servers = map[string]config.MCPServerConfig{}

	_, ok := ResolveBrowserRelayV2Options(cfg)
	if ok {
		t.Fatal("ResolveBrowserRelayV2Options() ok = true, want false")
	}
}

func TestNormalizeBrowserRelayV2EndpointURL(t *testing.T) {
	if got := normalizeBrowserRelayV2EndpointURL("https://api.suprclaw.com/api/mcp/browser-relay"); got != "https://api.suprclaw.com/api/mcp/browser-relay" {
		t.Fatalf("normalizeBrowserRelayV2EndpointURL() = %q", got)
	}
	if got := normalizeBrowserRelayV2EndpointURL("https://api.suprclaw.com/api/browser-relay/actions"); got != "https://api.suprclaw.com/api/browser-relay/actions" {
		t.Fatalf("normalizeBrowserRelayV2EndpointURL() passthrough = %q", got)
	}
	if got := normalizeBrowserRelayV2EndpointURL("https://api.suprclaw.com/api/mcp/other"); got != "" {
		t.Fatalf("normalizeBrowserRelayV2EndpointURL() unsupported path = %q, want empty", got)
	}
}

func TestBrowserRelayV2ActionTool_ExecuteCallsConfiguredV2Endpoint(t *testing.T) {
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
		endpointURL: ts.URL + "/api/browser-relay/actions",
		headers:     map[string]string{"Authorization": "Bearer abc"},
		client:      ts.Client(),
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

func TestBrowserRelayV2ActionTool_ExecuteCallsMCPWhenConfigured(t *testing.T) {
	var gotBody map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		if r.URL.Path != "/api/mcp/browser-relay" {
			t.Fatalf("path = %q, want /api/mcp/browser-relay", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0",
			"id":      "req-1",
			"result": map[string]any{
				"isError": false,
				"structuredContent": map[string]any{
					"ok": true,
				},
			},
		})
	}))
	defer ts.Close()

	tool := &browserRelayV2ActionTool{
		endpointURL: ts.URL + "/api/mcp/browser-relay",
		headers:     map[string]string{"Authorization": "Bearer abc"},
		client:      ts.Client(),
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

	if got, _ := gotBody["method"].(string); got != "tools/call" {
		t.Fatalf("method = %q, want tools/call", got)
	}
	params, _ := gotBody["params"].(map[string]any)
	if got, _ := params["name"].(string); got != "browser_relay_v2_action" {
		t.Fatalf("params.name = %q, want browser_relay_v2_action", got)
	}
	arguments, _ := params["arguments"].(map[string]any)
	if got, _ := arguments["action"].(string); got != "navigate" {
		t.Fatalf("arguments.action = %q, want navigate", got)
	}
}

func TestBrowserRelayV2Tools_NamesAreV2Only(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Tools.BrowserRelay.Enabled = true
	cfg.Tools.MCP.Servers = map[string]config.MCPServerConfig{
		"browser_relay": {
			Enabled: true,
			Type:    "http",
			URL:     "https://api.suprclaw.com/api/mcp/browser-relay",
		},
	}
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

func TestBrowserRelayV2ActionTool_RejectsUnsupportedAction(t *testing.T) {
	tool := &browserRelayV2ActionTool{}

	result := tool.Execute(context.Background(), map[string]any{
		"target": "ext:1",
		"action": "goto",
		"args": map[string]any{
			"url": "https://example.com",
		},
	})
	if result == nil || !result.IsError {
		t.Fatalf("expected error result, got %#v", result)
	}
	if result.ForLLM == "" {
		t.Fatalf("expected non-empty validation error")
	}
}

func TestBrowserRelayV2ActionTool_RejectsNonRefClickSelector(t *testing.T) {
	tool := &browserRelayV2ActionTool{}

	result := tool.Execute(context.Background(), map[string]any{
		"target": "ext:1",
		"action": "click",
		"args": map[string]any{
			"selector": "#submit",
		},
	})
	if result == nil || !result.IsError {
		t.Fatalf("expected error result, got %#v", result)
	}
	if result.ForLLM == "" {
		t.Fatalf("expected non-empty validation error")
	}
}

func TestBrowserRelayV2ActionTool_ParametersExposeActionEnum(t *testing.T) {
	tool := &browserRelayV2ActionTool{}
	params := tool.Parameters()
	props, ok := params["properties"].(map[string]any)
	if !ok {
		t.Fatalf("properties not found in schema")
	}
	action, ok := props["action"].(map[string]any)
	if !ok {
		t.Fatalf("action schema not found")
	}
	enumVals, ok := action["enum"].([]any)
	if !ok || len(enumVals) == 0 {
		t.Fatalf("action enum is missing or empty")
	}
}
