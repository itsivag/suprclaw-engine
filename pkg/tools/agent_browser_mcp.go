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
	defaultAgentBrowserMCPTimeout = 20 * time.Second
)

var agentBrowserSupportedActions = map[string]struct{}{
	"tabs.select": {},
	"navigate":    {},
	"click":       {},
	"type":        {},
	"press":       {},
	"screenshot":  {},
	"snapshot":    {},
	"wait":        {},
}

var agentBrowserActionEnum = []any{
	"tabs.select",
	"navigate",
	"click",
	"type",
	"press",
	"screenshot",
	"snapshot",
	"wait",
}

// AgentBrowserMCPOptions contains request wiring for Agent Browser tools.
type AgentBrowserMCPOptions struct {
	EndpointURL string
	Headers     map[string]string
	Timeout     time.Duration
}

// NewAgentBrowserMCPTools registers strict Agent Browser tools only.
func NewAgentBrowserMCPTools(cfg *config.Config) []Tool {
	opts, ok := ResolveAgentBrowserMCPOptions(cfg)
	if !ok {
		return nil
	}
	client := &http.Client{Timeout: opts.Timeout}
	return []Tool{
		&agentBrowserTargetsListTool{
			endpointURL: opts.EndpointURL,
			headers:     cloneStringMap(opts.Headers),
			client:      client,
		},
		&agentBrowserActionTool{
			endpointURL: opts.EndpointURL,
			headers:     cloneStringMap(opts.Headers),
			client:      client,
		},
		&agentBrowserBatchTool{
			endpointURL: opts.EndpointURL,
			headers:     cloneStringMap(opts.Headers),
			client:      client,
		},
	}
}

// ResolveAgentBrowserMCPOptions resolves connectivity details for Agent Browser actions.
// This is a hard V2 migration path: Agent Browser tools are registered only when
// tools.mcp.servers.agent_browser.url is configured and valid.
func ResolveAgentBrowserMCPOptions(cfg *config.Config) (AgentBrowserMCPOptions, bool) {
	if cfg == nil || !cfg.Tools.AgentBrowser.Enabled {
		return AgentBrowserMCPOptions{}, false
	}
	server, ok := cfg.Tools.MCP.Servers["agent_browser"]
	if !ok || strings.TrimSpace(server.URL) == "" {
		return AgentBrowserMCPOptions{}, false
	}
	endpointURL := normalizeAgentBrowserMCPEndpointURL(server.URL)
	if endpointURL == "" {
		return AgentBrowserMCPOptions{}, false
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
	return AgentBrowserMCPOptions{
		EndpointURL: endpointURL,
		Headers:     headers,
		Timeout:     defaultAgentBrowserMCPTimeout,
	}, true
}

func normalizeAgentBrowserMCPEndpointURL(raw string) string {
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
	if path != "/api/mcp/agent-browser" {
		return ""
	}
	u.Path = path
	return u.String()
}

type agentBrowserTargetsListTool struct {
	endpointURL string
	headers     map[string]string
	client      *http.Client
}

func (t *agentBrowserTargetsListTool) Name() string {
	return "agent_browser_targets_list"
}

func (t *agentBrowserTargetsListTool) Description() string {
	return "List Agent Browser targets. Call this first, then pass the selected target id into agent_browser_action/batch."
}

func (t *agentBrowserTargetsListTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"request_id": map[string]any{"type": "string"},
		},
	}
}

func (t *agentBrowserTargetsListTool) SideEffectType() string { return "external" }

func (t *agentBrowserTargetsListTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	payload := map[string]any{
		"request_id": requestIDFromArgs(args),
		"action":     "tabs.list",
		"args":       map[string]any{},
	}
	res, err := callAgentBrowserMCP(ctx, t.client, t.endpointURL, t.headers, "agent_browser_targets_list", payload)
	if err != nil {
		return ErrorResult(err.Error()).WithError(err)
	}
	return NewToolResult(res)
}

type agentBrowserActionTool struct {
	endpointURL string
	headers     map[string]string
	client      *http.Client
}

func (t *agentBrowserActionTool) Name() string {
	return "agent_browser_action"
}

func (t *agentBrowserActionTool) Description() string {
	return "Execute one Agent Browser action. Allowed actions: tabs.select, navigate, click, type, press, screenshot, snapshot, wait. For click/type, args.selector MUST be a snapshot ref like @e12."
}

func (t *agentBrowserActionTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"request_id": map[string]any{"type": "string"},
			"target": map[string]any{
				"type":        "string",
				"description": "Target id from agent_browser_targets_list (required for every action).",
			},
			"action": map[string]any{
				"type": "string",
				"enum": agentBrowserActionEnum,
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

func (t *agentBrowserActionTool) SideEffectType() string { return "external" }

func (t *agentBrowserActionTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	action := strings.TrimSpace(asString(args["action"]))
	if action == "" {
		return ErrorResult("action is required")
	}
	target := strings.TrimSpace(asString(args["target"]))
	if target == "" {
		return ErrorResult("target is required")
	}
	if _, ok := agentBrowserSupportedActions[action]; !ok {
		return ErrorResult(fmt.Sprintf("unsupported action %q; allowed actions: %s", action, strings.Join(sortedAgentBrowserActions(), ", ")))
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
	if err := validateAgentBrowserActionPayload(action, body); err != nil {
		return ErrorResult(err.Error())
	}
	if len(body) > 0 {
		payload["args"] = body
	}
	if policy, ok := asObject(args["execution_policy"]); ok {
		payload["execution_policy"] = policy
	}
	res, err := callAgentBrowserMCP(ctx, t.client, t.endpointURL, t.headers, "agent_browser_action", payload)
	if err != nil {
		return ErrorResult(err.Error()).WithError(err)
	}
	return NewToolResult(res)
}

type agentBrowserBatchTool struct {
	endpointURL string
	headers     map[string]string
	client      *http.Client
}

func (t *agentBrowserBatchTool) Name() string {
	return "agent_browser_batch"
}

func (t *agentBrowserBatchTool) Description() string {
	return "Execute a Agent Browser batch on one target. Each step action must be one of: tabs.select, navigate, click, type, press, screenshot, snapshot, wait. click/type require ref selectors (@eN)."
}

func (t *agentBrowserBatchTool) Parameters() map[string]any {
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
							"enum": agentBrowserActionEnum,
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

func (t *agentBrowserBatchTool) SideEffectType() string { return "external" }

func (t *agentBrowserBatchTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
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
		if _, ok := agentBrowserSupportedActions[action]; !ok {
			return ErrorResult(fmt.Sprintf("steps[%d].action %q is unsupported; allowed actions: %s", i, action, strings.Join(sortedAgentBrowserActions(), ", ")))
		}
		body := map[string]any{}
		if b, ok := asObject(step["args"]); ok {
			body = b
		}
		if err := validateAgentBrowserActionPayload(action, body); err != nil {
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
	res, err := callAgentBrowserMCP(ctx, t.client, t.endpointURL, t.headers, "agent_browser_batch", payload)
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

func callAgentBrowserMCP(
	ctx context.Context,
	client *http.Client,
	endpointURL string,
	headers map[string]string,
	toolName string,
	payload map[string]any,
) (string, error) {
	if strings.TrimSpace(endpointURL) == "" {
		return "", fmt.Errorf("agent browser is not configured")
	}
	parsedURL, err := url.Parse(endpointURL)
	if err != nil {
		return "", fmt.Errorf("invalid agent browser endpoint: %w", err)
	}
	path := strings.TrimRight(parsedURL.Path, "/")
	switch path {
	case "/api/mcp/agent-browser":
		return callAgentBrowserMCPViaMCP(ctx, client, endpointURL, headers, toolName, payload)
	default:
		return "", fmt.Errorf("unsupported agent browser endpoint path: %s", parsedURL.Path)
	}
}

func callAgentBrowserMCPViaMCP(
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
		return "", fmt.Errorf("agent browser mcp request failed: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return "", fmt.Errorf("agent browser mcp HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
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
		return "", fmt.Errorf("invalid agent browser mcp response: %w", err)
	}
	if envelope.Error != nil {
		return "", fmt.Errorf(
			"agent browser mcp error (%d): %s",
			envelope.Error.Code,
			strings.TrimSpace(envelope.Error.Message),
		)
	}
	if envelope.Result.IsError {
		if len(envelope.Result.StructuredContent) > 0 && string(envelope.Result.StructuredContent) != "null" {
			return "", fmt.Errorf("agent browser mcp tool error: %s", strings.TrimSpace(string(envelope.Result.StructuredContent)))
		}
		if len(envelope.Result.Content) > 0 {
			return "", fmt.Errorf("agent browser mcp tool error: %s", strings.TrimSpace(envelope.Result.Content[0].Text))
		}
		return "", fmt.Errorf("agent browser mcp tool error")
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

func sortedAgentBrowserActions() []string {
	out := make([]string, 0, len(agentBrowserSupportedActions))
	for action := range agentBrowserSupportedActions {
		out = append(out, action)
	}
	sort.Strings(out)
	return out
}

func validateAgentBrowserActionPayload(action string, args map[string]any) error {
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
		if !isAgentBrowserSnapshotRef(selector) {
			return fmt.Errorf("click args.selector must be snapshot ref @eN")
		}
	case "type":
		selector := strings.TrimSpace(asString(args["selector"]))
		if selector == "" {
			return fmt.Errorf("type requires args.selector as snapshot ref (@eN)")
		}
		if !isAgentBrowserSnapshotRef(selector) {
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

func isAgentBrowserSnapshotRef(selector string) bool {
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
