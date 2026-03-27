package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/itsivag/suprclaw/pkg/config"
)

const (
	defaultBrowserRelayV2Timeout = 20 * time.Second
)

// BrowserRelayV2Options contains request wiring for Browser Relay V2 tools.
type BrowserRelayV2Options struct {
	ActionsURL string
	Headers    map[string]string
	Timeout    time.Duration
}

// NewBrowserRelayV2Tools registers strict Browser Relay V2 tools only.
func NewBrowserRelayV2Tools(cfg *config.Config) []Tool {
	opts, ok := ResolveBrowserRelayV2Options(cfg)
	if !ok {
		return nil
	}
	client := &http.Client{Timeout: opts.Timeout}
	return []Tool{
		&browserRelayV2TargetsListTool{
			actionsURL: opts.ActionsURL,
			headers:    cloneStringMap(opts.Headers),
			client:     client,
		},
		&browserRelayV2ActionTool{
			actionsURL: opts.ActionsURL,
			headers:    cloneStringMap(opts.Headers),
			client:     client,
		},
		&browserRelayV2BatchTool{
			actionsURL: opts.ActionsURL,
			headers:    cloneStringMap(opts.Headers),
			client:     client,
		},
	}
}

// ResolveBrowserRelayV2Options resolves connectivity details for Browser Relay V2 actions.
// This is a hard V2 migration path: Browser Relay V2 tools are registered only when
// tools.mcp.servers.browser_relay.url is configured and valid.
func ResolveBrowserRelayV2Options(cfg *config.Config) (BrowserRelayV2Options, bool) {
	if cfg == nil || !cfg.Tools.BrowserRelay.Enabled {
		return BrowserRelayV2Options{}, false
	}
	server, ok := cfg.Tools.MCP.Servers["browser_relay"]
	if !ok || strings.TrimSpace(server.URL) == "" {
		return BrowserRelayV2Options{}, false
	}
	actionsURL := deriveBrowserRelayV2ActionsURL(server.URL)
	if actionsURL == "" {
		return BrowserRelayV2Options{}, false
	}
	headers := map[string]string{
		"Content-Type": "application/json",
	}
	for k, v := range server.Headers {
		key := strings.TrimSpace(k)
		val := strings.TrimSpace(v)
		if key == "" || val == "" {
			continue
		}
		headers[key] = val
	}
	return BrowserRelayV2Options{
		ActionsURL: actionsURL,
		Headers:    headers,
		Timeout:    defaultBrowserRelayV2Timeout,
	}, true
}

func deriveBrowserRelayV2ActionsURL(raw string) string {
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
	switch strings.TrimRight(u.Path, "/") {
	case "/api/mcp/browser-relay":
		u.Path = "/api/browser-relay/actions"
	case "/api/browser-relay/actions":
		// already actions endpoint
	default:
		// unsupported path for hard migrate mode
		return ""
	}
	return u.String()
}

type browserRelayV2TargetsListTool struct {
	actionsURL string
	headers    map[string]string
	client     *http.Client
}

func (t *browserRelayV2TargetsListTool) Name() string {
	return "browser_relay_v2_targets_list"
}

func (t *browserRelayV2TargetsListTool) Description() string {
	return "List browser relay targets using V2 actions envelope."
}

func (t *browserRelayV2TargetsListTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"request_id": map[string]any{"type": "string"},
		},
	}
}

func (t *browserRelayV2TargetsListTool) SideEffectType() string { return "external" }

func (t *browserRelayV2TargetsListTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	payload := map[string]any{
		"request_id": requestIDFromArgs(args),
		"action":     "tabs.list",
		"args":       map[string]any{},
	}
	res, err := callBrowserRelayV2(ctx, t.client, t.actionsURL, t.headers, payload)
	if err != nil {
		return ErrorResult(err.Error()).WithError(err)
	}
	return NewToolResult(res)
}

type browserRelayV2ActionTool struct {
	actionsURL string
	headers    map[string]string
	client     *http.Client
}

func (t *browserRelayV2ActionTool) Name() string {
	return "browser_relay_v2_action"
}

func (t *browserRelayV2ActionTool) Description() string {
	return "Execute one Browser Relay V2 action with strict V2 request envelope."
}

func (t *browserRelayV2ActionTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"request_id": map[string]any{"type": "string"},
			"target":     map[string]any{"type": "string"},
			"action":     map[string]any{"type": "string"},
			"args":       map[string]any{"type": "object"},
			"execution_policy": map[string]any{
				"type": "object",
			},
		},
		"required": []string{"action"},
	}
}

func (t *browserRelayV2ActionTool) SideEffectType() string { return "external" }

func (t *browserRelayV2ActionTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	if strings.TrimSpace(asString(args["action"])) == "" {
		return ErrorResult("action is required")
	}
	payload := map[string]any{
		"request_id": requestIDFromArgs(args),
		"action":     asString(args["action"]),
	}
	if target := strings.TrimSpace(asString(args["target"])); target != "" {
		payload["target"] = target
	}
	if body, ok := asObject(args["args"]); ok {
		payload["args"] = body
	}
	if policy, ok := asObject(args["execution_policy"]); ok {
		payload["execution_policy"] = policy
	}
	res, err := callBrowserRelayV2(ctx, t.client, t.actionsURL, t.headers, payload)
	if err != nil {
		return ErrorResult(err.Error()).WithError(err)
	}
	return NewToolResult(res)
}

type browserRelayV2BatchTool struct {
	actionsURL string
	headers    map[string]string
	client     *http.Client
}

func (t *browserRelayV2BatchTool) Name() string {
	return "browser_relay_v2_batch"
}

func (t *browserRelayV2BatchTool) Description() string {
	return "Execute Browser Relay V2 batch action with strict V2 request envelope."
}

func (t *browserRelayV2BatchTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"request_id": map[string]any{"type": "string"},
			"target":     map[string]any{"type": "string"},
			"steps": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"action": map[string]any{"type": "string"},
						"args":   map[string]any{"type": "object"},
					},
					"required": []string{"action"},
				},
			},
			"execution_policy": map[string]any{
				"type": "object",
			},
		},
		"required": []string{"target", "steps"},
	}
}

func (t *browserRelayV2BatchTool) SideEffectType() string { return "external" }

func (t *browserRelayV2BatchTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	target := strings.TrimSpace(asString(args["target"]))
	if target == "" {
		return ErrorResult("target is required")
	}
	steps, ok := asArray(args["steps"])
	if !ok || len(steps) == 0 {
		return ErrorResult("steps is required")
	}
	payload := map[string]any{
		"request_id": requestIDFromArgs(args),
		"target":     target,
		"steps":      steps,
	}
	if policy, ok := asObject(args["execution_policy"]); ok {
		payload["execution_policy"] = policy
	}
	res, err := callBrowserRelayV2(ctx, t.client, t.actionsURL, t.headers, payload)
	if err != nil {
		return ErrorResult(err.Error()).WithError(err)
	}
	return NewToolResult(res)
}

func requestIDFromArgs(args map[string]any) string {
	if args == nil {
		return fmt.Sprintf("v2-%d", time.Now().UnixNano())
	}
	if reqID := strings.TrimSpace(asString(args["request_id"])); reqID != "" {
		return reqID
	}
	return fmt.Sprintf("v2-%d", time.Now().UnixNano())
}

func callBrowserRelayV2(
	ctx context.Context,
	client *http.Client,
	actionsURL string,
	headers map[string]string,
	payload map[string]any,
) (string, error) {
	if strings.TrimSpace(actionsURL) == "" {
		return "", fmt.Errorf("browser relay v2 is not configured")
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, actionsURL, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("browser relay v2 action request failed: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	if resp.StatusCode >= http.StatusBadRequest {
		return "", fmt.Errorf("browser relay v2 action HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}

	var envelope struct {
		OK           bool            `json:"ok"`
		Result       json.RawMessage `json:"result"`
		ErrorCode    string          `json:"error_code"`
		ErrorMessage string          `json:"error_message"`
		RetryClass   string          `json:"retry_class"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return "", fmt.Errorf("invalid browser relay v2 response: %w", err)
	}
	if !envelope.OK {
		return "", fmt.Errorf(
			"browser relay v2 action failed (%s/%s): %s",
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

func asString(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprintf("%v", v)
}

func asObject(v any) (map[string]any, bool) {
	if v == nil {
		return nil, false
	}
	m, ok := v.(map[string]any)
	return m, ok
}

func asArray(v any) ([]any, bool) {
	if v == nil {
		return nil, false
	}
	a, ok := v.([]any)
	return a, ok
}

func cloneStringMap(src map[string]string) map[string]string {
	if len(src) == 0 {
		return map[string]string{}
	}
	out := make(map[string]string, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}
