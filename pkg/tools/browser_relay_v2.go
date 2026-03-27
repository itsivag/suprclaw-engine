package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/itsivag/suprclaw/pkg/config"
)

const (
	defaultBrowserRelayV2Timeout = 20 * time.Second
)

var browserRelayV2SupportedActions = map[string]struct{}{
	"tabs.select": {},
	"navigate":    {},
	"click":       {},
	"type":        {},
	"press":       {},
	"screenshot":  {},
	"snapshot":    {},
	"wait":        {},
}

var browserRelayV2ActionEnum = []any{
	"tabs.select",
	"navigate",
	"click",
	"type",
	"press",
	"screenshot",
	"snapshot",
	"wait",
}

// BrowserRelayV2Options contains request wiring for Browser Relay V2 tools.
type BrowserRelayV2Options struct {
	EndpointURL string
	Headers     map[string]string
	Timeout     time.Duration
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
			endpointURL: opts.EndpointURL,
			headers:     cloneStringMap(opts.Headers),
			client:      client,
		},
		&browserRelayV2ActionTool{
			endpointURL: opts.EndpointURL,
			headers:     cloneStringMap(opts.Headers),
			client:      client,
		},
		&browserRelayV2BatchTool{
			endpointURL: opts.EndpointURL,
			headers:     cloneStringMap(opts.Headers),
			client:      client,
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
	endpointURL := normalizeBrowserRelayV2EndpointURL(server.URL)
	if endpointURL == "" {
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
		EndpointURL: endpointURL,
		Headers:     headers,
		Timeout:     defaultBrowserRelayV2Timeout,
	}, true
}

func normalizeBrowserRelayV2EndpointURL(raw string) string {
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
	path := strings.TrimRight(u.Path, "/")
	switch path {
	case "/api/mcp/browser-relay":
		u.Path = path
	case "/api/browser-relay/actions":
		u.Path = path
	default:
		// unsupported path for hard migrate mode
		return ""
	}
	return u.String()
}

type browserRelayV2TargetsListTool struct {
	endpointURL string
	headers     map[string]string
	client      *http.Client
}

func (t *browserRelayV2TargetsListTool) Name() string {
	return "browser_relay_v2_targets_list"
}

func (t *browserRelayV2TargetsListTool) Description() string {
	return "List Browser Relay V2 targets. Call this first, then pass the selected target id into browser_relay_v2_action/batch."
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
	res, err := callBrowserRelayV2(ctx, t.client, t.endpointURL, t.headers, "browser_relay_v2_targets_list", payload)
	if err != nil {
		return ErrorResult(err.Error()).WithError(err)
	}
	return NewToolResult(res)
}

type browserRelayV2ActionTool struct {
	endpointURL string
	headers     map[string]string
	client      *http.Client
}

func (t *browserRelayV2ActionTool) Name() string {
	return "browser_relay_v2_action"
}

func (t *browserRelayV2ActionTool) Description() string {
	return "Execute one Browser Relay V2 action. Allowed actions: tabs.select, navigate, click, type, press, screenshot, snapshot, wait. For click/type, args.selector MUST be a snapshot ref like @e12."
}

func (t *browserRelayV2ActionTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"request_id": map[string]any{"type": "string"},
			"target": map[string]any{
				"type":        "string",
				"description": "Target id from browser_relay_v2_targets_list (required for every action).",
			},
			"action": map[string]any{
				"type": "string",
				"enum": browserRelayV2ActionEnum,
			},
			"args": map[string]any{
				"type":        "object",
				"description": "Action arguments. click/type require args.selector as @eN; type also requires args.text; navigate requires args.url; press requires args.key.",
			},
			"execution_policy": map[string]any{
				"type": "object",
			},
		},
		"required": []string{"target", "action"},
	}
}

func (t *browserRelayV2ActionTool) SideEffectType() string { return "external" }

func (t *browserRelayV2ActionTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	action := strings.TrimSpace(asString(args["action"]))
	if action == "" {
		return ErrorResult("action is required")
	}
	target := strings.TrimSpace(asString(args["target"]))
	if target == "" {
		return ErrorResult("target is required")
	}
	if _, ok := browserRelayV2SupportedActions[action]; !ok {
		return ErrorResult(fmt.Sprintf("unsupported action %q; allowed actions: %s", action, strings.Join(sortedBrowserRelayV2Actions(), ", ")))
	}
	payload := map[string]any{
		"request_id": requestIDFromArgs(args),
		"action":     action,
		"target":     target,
	}
	body := map[string]any{}
	if b, ok := asObject(args["args"]); ok {
		body = b
	}
	if err := validateBrowserRelayV2ActionPayload(action, body); err != nil {
		return ErrorResult(err.Error())
	}
	if len(body) > 0 {
		payload["args"] = body
	}
	if policy, ok := asObject(args["execution_policy"]); ok {
		payload["execution_policy"] = policy
	}
	res, err := callBrowserRelayV2(ctx, t.client, t.endpointURL, t.headers, "browser_relay_v2_action", payload)
	if err != nil {
		return ErrorResult(err.Error()).WithError(err)
	}
	return NewToolResult(res)
}

type browserRelayV2BatchTool struct {
	endpointURL string
	headers     map[string]string
	client      *http.Client
}

func (t *browserRelayV2BatchTool) Name() string {
	return "browser_relay_v2_batch"
}

func (t *browserRelayV2BatchTool) Description() string {
	return "Execute a Browser Relay V2 batch on one target. Each step action must be one of: tabs.select, navigate, click, type, press, screenshot, snapshot, wait. click/type require ref selectors (@eN)."
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
						"action": map[string]any{
							"type": "string",
							"enum": browserRelayV2ActionEnum,
						},
						"args": map[string]any{"type": "object"},
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
	normalizedSteps := make([]any, 0, len(steps))
	for i, stepRaw := range steps {
		step, ok := asObject(stepRaw)
		if !ok {
			return ErrorResult(fmt.Sprintf("steps[%d] must be an object", i))
		}
		action := strings.TrimSpace(asString(step["action"]))
		if action == "" {
			return ErrorResult(fmt.Sprintf("steps[%d].action is required", i))
		}
		if _, ok := browserRelayV2SupportedActions[action]; !ok {
			return ErrorResult(fmt.Sprintf("steps[%d].action %q is unsupported; allowed actions: %s", i, action, strings.Join(sortedBrowserRelayV2Actions(), ", ")))
		}
		body := map[string]any{}
		if b, ok := asObject(step["args"]); ok {
			body = b
		}
		if err := validateBrowserRelayV2ActionPayload(action, body); err != nil {
			return ErrorResult(fmt.Sprintf("steps[%d]: %s", i, err.Error()))
		}
		normalized := map[string]any{"action": action}
		if len(body) > 0 {
			normalized["args"] = body
		}
		normalizedSteps = append(normalizedSteps, normalized)
	}
	payload := map[string]any{
		"request_id": requestIDFromArgs(args),
		"target":     target,
		"steps":      normalizedSteps,
	}
	if policy, ok := asObject(args["execution_policy"]); ok {
		payload["execution_policy"] = policy
	}
	res, err := callBrowserRelayV2(ctx, t.client, t.endpointURL, t.headers, "browser_relay_v2_batch", payload)
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
	endpointURL string,
	headers map[string]string,
	toolName string,
	payload map[string]any,
) (string, error) {
	if strings.TrimSpace(endpointURL) == "" {
		return "", fmt.Errorf("browser relay v2 is not configured")
	}
	parsedURL, err := url.Parse(endpointURL)
	if err != nil {
		return "", fmt.Errorf("invalid browser relay v2 endpoint: %w", err)
	}
	path := strings.TrimRight(parsedURL.Path, "/")
	switch path {
	case "/api/mcp/browser-relay":
		return callBrowserRelayV2ViaMCP(ctx, client, endpointURL, headers, toolName, payload)
	case "/api/browser-relay/actions":
		return callBrowserRelayV2ViaActions(ctx, client, endpointURL, headers, payload)
	default:
		return "", fmt.Errorf("unsupported browser relay v2 endpoint path: %s", parsedURL.Path)
	}
}

func callBrowserRelayV2ViaActions(
	ctx context.Context,
	client *http.Client,
	actionsURL string,
	headers map[string]string,
	payload map[string]any,
) (string, error) {

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

func callBrowserRelayV2ViaMCP(
	ctx context.Context,
	client *http.Client,
	mcpURL string,
	headers map[string]string,
	toolName string,
	payload map[string]any,
) (string, error) {
	if strings.TrimSpace(toolName) == "" {
		return "", fmt.Errorf("tool name is required for browser relay mcp call")
	}
	id := requestIDFromArgs(payload)
	reqPayload := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      toolName,
			"arguments": payload,
		},
	}
	body, err := json.Marshal(reqPayload)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, mcpURL, bytes.NewReader(body))
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
		return "", fmt.Errorf("browser relay v2 mcp request failed: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return "", fmt.Errorf("browser relay v2 mcp HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}

	var envelope struct {
		Error *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
		Result struct {
			IsError bool `json:"isError"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
			StructuredContent json.RawMessage `json:"structuredContent"`
		} `json:"result"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return "", fmt.Errorf("invalid browser relay v2 mcp response: %w", err)
	}
	if envelope.Error != nil {
		return "", fmt.Errorf(
			"browser relay v2 mcp error (%d): %s",
			envelope.Error.Code,
			strings.TrimSpace(envelope.Error.Message),
		)
	}
	if envelope.Result.IsError {
		if len(envelope.Result.StructuredContent) > 0 && string(envelope.Result.StructuredContent) != "null" {
			return "", fmt.Errorf("browser relay v2 mcp tool error: %s", strings.TrimSpace(string(envelope.Result.StructuredContent)))
		}
		if len(envelope.Result.Content) > 0 {
			return "", fmt.Errorf("browser relay v2 mcp tool error: %s", strings.TrimSpace(envelope.Result.Content[0].Text))
		}
		return "", fmt.Errorf("browser relay v2 mcp tool error")
	}
	if len(envelope.Result.StructuredContent) > 0 && string(envelope.Result.StructuredContent) != "null" {
		return string(envelope.Result.StructuredContent), nil
	}
	if len(envelope.Result.Content) > 0 {
		return strings.TrimSpace(envelope.Result.Content[0].Text), nil
	}
	return string(data), nil
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

func sortedBrowserRelayV2Actions() []string {
	out := make([]string, 0, len(browserRelayV2SupportedActions))
	for action := range browserRelayV2SupportedActions {
		out = append(out, action)
	}
	sort.Strings(out)
	return out
}

func validateBrowserRelayV2ActionPayload(action string, args map[string]any) error {
	switch action {
	case "navigate":
		if strings.TrimSpace(asString(args["url"])) == "" {
			return fmt.Errorf("navigate requires args.url")
		}
	case "click":
		selector := strings.TrimSpace(asString(args["selector"]))
		if selector == "" {
			return fmt.Errorf("click requires args.selector as snapshot ref (@eN)")
		}
		if !isBrowserRelaySnapshotRef(selector) {
			return fmt.Errorf("click args.selector must be snapshot ref @eN")
		}
	case "type":
		selector := strings.TrimSpace(asString(args["selector"]))
		if selector == "" {
			return fmt.Errorf("type requires args.selector as snapshot ref (@eN)")
		}
		if !isBrowserRelaySnapshotRef(selector) {
			return fmt.Errorf("type args.selector must be snapshot ref @eN")
		}
		if strings.TrimSpace(asString(args["text"])) == "" {
			return fmt.Errorf("type requires args.text")
		}
	case "press":
		if strings.TrimSpace(asString(args["key"])) == "" {
			return fmt.Errorf("press requires args.key")
		}
	}
	return nil
}

func isBrowserRelaySnapshotRef(selector string) bool {
	selector = strings.TrimSpace(selector)
	if !strings.HasPrefix(selector, "@e") {
		return false
	}
	num := strings.TrimPrefix(selector, "@e")
	if num == "" {
		return false
	}
	n, err := strconv.Atoi(num)
	return err == nil && n > 0
}
