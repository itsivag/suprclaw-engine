package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/itsivag/suprclaw/pkg/config"
)

const (
	defaultBrowserRelayCompatTimeout = 20 * time.Second
)

var browserRelayCompatToolSpecs = []struct {
	name        string
	action      string
	description string
}{
	{name: "mcp_browser_relay_browser_relay_tabs_list", action: "tabs.list", description: "List browser relay targets."},
	{name: "mcp_browser_relay_browser_relay_tabs_select", action: "tabs.select", description: "Select a browser relay target."},
	{name: "mcp_browser_relay_browser_relay_navigate", action: "navigate", description: "Navigate the target tab to a URL."},
	{name: "mcp_browser_relay_browser_relay_click", action: "click", description: "Click an element on the target tab."},
	{name: "mcp_browser_relay_browser_relay_type", action: "type", description: "Type text into an element on the target tab."},
	{name: "mcp_browser_relay_browser_relay_press", action: "press", description: "Press a keyboard key on the target tab."},
	{name: "mcp_browser_relay_browser_relay_wait", action: "wait", description: "Wait for a condition on the target tab."},
	{name: "mcp_browser_relay_browser_relay_snapshot", action: "snapshot", description: "Capture a compact snapshot from the target tab."},
	{name: "mcp_browser_relay_browser_relay_screenshot", action: "screenshot", description: "Capture a screenshot from the target tab."},
}

// BrowserRelayCompatOptions contains request wiring for browser relay V2 compatibility tools.
type BrowserRelayCompatOptions struct {
	ActionsURL string
	Headers    map[string]string
	Timeout    time.Duration
}

// NewBrowserRelayCompatTools creates local replacement tools for legacy MCP browser_relay names.
// These tools call Browser Relay V2 actions endpoint directly.
func NewBrowserRelayCompatTools(cfg *config.Config) []Tool {
	opts, ok := ResolveBrowserRelayCompatOptions(cfg)
	if !ok {
		return nil
	}
	client := &http.Client{Timeout: opts.Timeout}
	out := make([]Tool, 0, len(browserRelayCompatToolSpecs))
	for _, spec := range browserRelayCompatToolSpecs {
		out = append(out, &browserRelayCompatTool{
			name:        spec.name,
			action:      spec.action,
			description: spec.description,
			actionsURL:  opts.ActionsURL,
			headers:     cloneStringMap(opts.Headers),
			client:      client,
		})
	}
	return out
}

// ResolveBrowserRelayCompatOptions resolves connectivity details for Browser Relay V2 actions.
func ResolveBrowserRelayCompatOptions(cfg *config.Config) (BrowserRelayCompatOptions, bool) {
	if cfg == nil {
		return BrowserRelayCompatOptions{}, false
	}

	var endpoint string
	headers := map[string]string{}

	if server, ok := cfg.Tools.MCP.Servers["browser_relay"]; ok {
		if strings.TrimSpace(server.URL) != "" {
			endpoint = deriveBrowserRelayActionsURL(server.URL)
		}
		for k, v := range server.Headers {
			if strings.TrimSpace(k) == "" || strings.TrimSpace(v) == "" {
				continue
			}
			headers[k] = v
		}
	}

	if endpoint == "" && cfg.Tools.BrowserRelay.Enabled {
		host := strings.TrimSpace(cfg.Tools.BrowserRelay.Host)
		if host != "" && cfg.Tools.BrowserRelay.Port > 0 {
			endpoint = fmt.Sprintf("http://%s:%d/api/browser-relay/actions", host, cfg.Tools.BrowserRelay.Port)
		}
		if token := strings.TrimSpace(cfg.Tools.BrowserRelay.Token); token != "" {
			headers["Authorization"] = "Bearer " + token
		}
	}

	if strings.TrimSpace(endpoint) == "" {
		return BrowserRelayCompatOptions{}, false
	}
	if _, ok := headers["Content-Type"]; !ok {
		headers["Content-Type"] = "application/json"
	}

	return BrowserRelayCompatOptions{
		ActionsURL: endpoint,
		Headers:    headers,
		Timeout:    defaultBrowserRelayCompatTimeout,
	}, true
}

func deriveBrowserRelayActionsURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	u.RawQuery = ""
	u.Fragment = ""
	u.Path = "/api/browser-relay/actions"
	return u.String()
}

type browserRelayCompatTool struct {
	name        string
	action      string
	description string
	actionsURL  string
	headers     map[string]string
	client      *http.Client
}

func (t *browserRelayCompatTool) Name() string { return t.name }

func (t *browserRelayCompatTool) Description() string {
	return "[BrowserRelay V2 Compat] " + t.description
}

func (t *browserRelayCompatTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"targetId":       map[string]any{"type": "string"},
			"target_id":      map[string]any{"type": "string"},
			"url":            map[string]any{"type": "string"},
			"selector":       map[string]any{"type": "string"},
			"text":           map[string]any{"type": "string"},
			"key":            map[string]any{"type": "string"},
			"expression":     map[string]any{"type": "string"},
			"wait_mode":      map[string]any{"type": "string"},
			"ref_generation": map[string]any{"type": "string"},
			"mode":           map[string]any{"type": "string"},
			"scope_selector": map[string]any{"type": "string"},
			"depth":          map[string]any{"type": "integer"},
			"max_nodes":      map[string]any{"type": "integer"},
			"max_text_chars": map[string]any{"type": "integer"},
			"timeout_ms":     map[string]any{"type": "integer"},
			"interval_ms":    map[string]any{"type": "integer"},
		},
	}
}

func (t *browserRelayCompatTool) SideEffectType() string { return "external" }

func (t *browserRelayCompatTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	if strings.TrimSpace(t.actionsURL) == "" {
		return ErrorResult("browser relay compat is not configured")
	}

	if t.action == "tabs.select" {
		result, err := t.handleTabsSelect(ctx, args)
		if err != nil {
			return ErrorResult(err.Error()).WithError(err)
		}
		payload, _ := json.Marshal(result)
		return NewToolResult(string(payload))
	}

	payload := map[string]any{
		"request_id": fmt.Sprintf("compat-%d", time.Now().UnixNano()),
		"action":     t.action,
	}

	targetID := extractTargetID(args)
	if targetID != "" && t.action != "tabs.list" {
		payload["target"] = normalizeCompatTarget(targetID)
	}

	actionArgs := buildCompatActionArgs(t.action, args)
	if len(actionArgs) > 0 {
		payload["args"] = actionArgs
	}

	res, err := t.callRelayAction(ctx, payload)
	if err != nil {
		return ErrorResult(err.Error()).WithError(err)
	}
	return NewToolResult(res)
}

func (t *browserRelayCompatTool) handleTabsSelect(ctx context.Context, args map[string]any) (map[string]any, error) {
	targetID := strings.TrimSpace(extractTargetID(args))
	if targetID == "" {
		return nil, fmt.Errorf("tabs_select requires targetId")
	}

	payload := map[string]any{
		"request_id": fmt.Sprintf("compat-%d", time.Now().UnixNano()),
		"action":     "tabs.list",
	}
	_, _ = t.callRelayAction(ctx, payload)

	return map[string]any{
		"ok":       true,
		"targetId": targetID,
		"selected": true,
	}, nil
}

func (t *browserRelayCompatTool) callRelayAction(ctx context.Context, payload map[string]any) (string, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.actionsURL, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	for k, v := range t.headers {
		req.Header.Set(k, v)
	}
	if req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := t.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("browser relay action request failed: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	if resp.StatusCode >= http.StatusBadRequest {
		return "", fmt.Errorf("browser relay action HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}

	var envelope struct {
		OK           bool            `json:"ok"`
		Result       json.RawMessage `json:"result"`
		ErrorCode    string          `json:"error_code"`
		ErrorMessage string          `json:"error_message"`
		RetryClass   string          `json:"retry_class"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return "", fmt.Errorf("invalid browser relay response: %w", err)
	}
	if !envelope.OK {
		return "", fmt.Errorf(
			"browser relay action failed (%s/%s): %s",
			strings.TrimSpace(envelope.ErrorCode),
			strings.TrimSpace(envelope.RetryClass),
			strings.TrimSpace(envelope.ErrorMessage),
		)
	}
	if len(envelope.Result) == 0 || string(envelope.Result) == "null" {
		return string(data), nil
	}
	return string(envelope.Result), nil
}

func extractTargetID(args map[string]any) string {
	if args == nil {
		return ""
	}
	for _, key := range []string{"targetId", "target_id", "target", "tabId", "tab_id"} {
		if value, ok := args[key]; ok {
			s := strings.TrimSpace(fmt.Sprintf("%v", value))
			if s != "" {
				return s
			}
		}
	}
	return ""
}

func normalizeCompatTarget(targetID string) string {
	targetID = strings.TrimSpace(targetID)
	if targetID == "" {
		return ""
	}
	if strings.HasPrefix(targetID, "ext:") || strings.HasPrefix(targetID, "ab:") {
		return targetID
	}
	return "ext:" + targetID
}

func buildCompatActionArgs(action string, args map[string]any) map[string]any {
	out := map[string]any{}
	if args == nil {
		if action == "snapshot" {
			out["mode"] = "compact"
		}
		return out
	}

	copyStringArg(out, "url", args, "url")
	copyStringArg(out, "selector", args, "selector")
	copyStringArg(out, "text", args, "text")
	copyStringArg(out, "key", args, "key")
	copyStringArg(out, "expression", args, "expression")
	copyStringArg(out, "wait_mode", args, "wait_mode", "waitMode")
	copyStringArg(out, "ref_generation", args, "ref_generation", "refGeneration")
	copyStringArg(out, "mode", args, "mode")
	copyStringArg(out, "scope_selector", args, "scope_selector", "scopeSelector")

	copyIntArg(out, "timeout_ms", args, "timeout_ms", "timeoutMs")
	copyIntArg(out, "interval_ms", args, "interval_ms", "intervalMs")
	copyIntArg(out, "depth", args, "depth")
	copyIntArg(out, "max_nodes", args, "max_nodes", "maxNodes")
	copyIntArg(out, "max_text_chars", args, "max_text_chars", "maxTextChars")

	if action == "snapshot" {
		if _, exists := out["mode"]; !exists {
			out["mode"] = "compact"
		}
	}
	if action == "wait" {
		if _, exists := out["wait_mode"]; !exists {
			out["wait_mode"] = "expression"
		}
	}
	return out
}

func copyStringArg(out map[string]any, outKey string, args map[string]any, keys ...string) {
	for _, key := range keys {
		value, ok := args[key]
		if !ok {
			continue
		}
		s := strings.TrimSpace(fmt.Sprintf("%v", value))
		if s == "" {
			continue
		}
		out[outKey] = s
		return
	}
}

func copyIntArg(out map[string]any, outKey string, args map[string]any, keys ...string) {
	for _, key := range keys {
		value, ok := args[key]
		if !ok {
			continue
		}
		s := strings.TrimSpace(fmt.Sprintf("%v", value))
		if s == "" {
			continue
		}
		n, err := strconv.Atoi(s)
		if err != nil {
			continue
		}
		out[outKey] = n
		return
	}
}

func cloneStringMap(src map[string]string) map[string]string {
	if len(src) == 0 {
		return map[string]string{}
	}
	dst := make(map[string]string, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}
