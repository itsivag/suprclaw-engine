package browserrelay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ExtensionEngine executes browser actions through the extension relay manager.
type ExtensionEngine struct {
	manager *Manager
}

func NewExtensionEngine(manager *Manager) *ExtensionEngine {
	return &ExtensionEngine{manager: manager}
}

func (e *ExtensionEngine) ListTargets(_ context.Context) ([]Target, error) {
	targets := e.manager.Targets()
	out := make([]Target, 0, len(targets))
	for _, t := range targets {
		t.ID = normalizeExtensionTargetID(t.ID)
		if strings.TrimSpace(t.Source) == "" {
			t.Source = TargetSourceExtension
		}
		out = append(out, t)
	}
	return out, nil
}

func (e *ExtensionEngine) ListSessions(context.Context) ([]Session, error) {
	return []Session{}, nil
}

func (e *ExtensionEngine) CreateSession(context.Context, ActionRequest) (any, error) {
	return nil, ErrUnsupportedAction
}

func (e *ExtensionEngine) CloseSession(context.Context, string) error {
	return ErrUnsupportedAction
}

func (e *ExtensionEngine) ExecuteAction(
	ctx context.Context,
	action string,
	req ActionRequest,
) (any, error) {
	targetID := extensionTargetRawID(req.TargetID)
	switch action {
	case "tabs.select":
		if targetID == "" {
			return nil, fmt.Errorf("target_id is required")
		}
		raw, err := e.manager.SendCommand(ctx, targetID, "Target.activateTarget", map[string]any{"targetId": targetID}, false)
		if err != nil {
			return nil, err
		}
		return decodeRawResult(raw), nil
	case "navigate":
		if targetID == "" || req.URL == "" {
			return nil, fmt.Errorf("target_id and url are required")
		}
		raw, err := e.manager.SendCommand(ctx, targetID, "Page.navigate", map[string]any{"url": req.URL}, true)
		if err != nil {
			return nil, err
		}
		return decodeRawResult(raw), nil
	case "click":
		if targetID == "" || req.Selector == "" {
			return nil, fmt.Errorf("target_id and selector are required")
		}
		return e.click(ctx, targetID, req.Selector)
	case "type":
		if targetID == "" || req.Selector == "" {
			return nil, fmt.Errorf("target_id and selector are required")
		}
		return e.typeText(ctx, targetID, req.Selector, req.Text)
	case "press":
		if targetID == "" || req.Key == "" {
			return nil, fmt.Errorf("target_id and key are required")
		}
		return e.press(ctx, targetID, req.Key)
	case "screenshot":
		if targetID == "" {
			return nil, fmt.Errorf("target_id is required")
		}
		raw, err := e.manager.SendCommand(ctx, targetID, "Page.captureScreenshot", map[string]any{"format": "png"}, true)
		if err != nil {
			return nil, err
		}
		return decodeRawResult(raw), nil
	case "snapshot":
		if targetID == "" {
			return nil, fmt.Errorf("target_id is required")
		}
		return e.snapshot(ctx, targetID)
	case "wait":
		if targetID == "" || strings.TrimSpace(req.Expression) == "" {
			return nil, fmt.Errorf("target_id and expression are required")
		}
		return e.wait(ctx, targetID, req.Expression, req.TimeoutMS, req.IntervalMS)
	default:
		return nil, fmt.Errorf("%w: %s", ErrUnsupportedAction, action)
	}
}

func (e *ExtensionEngine) click(ctx context.Context, targetID, selector string) (any, error) {
	x, y, err := e.resolveElementCenter(ctx, targetID, selector)
	if err != nil {
		return nil, err
	}

	if _, err = e.manager.SendCommand(ctx, targetID, "Input.dispatchMouseEvent", map[string]any{
		"type":       "mousePressed",
		"x":          x,
		"y":          y,
		"button":     "left",
		"clickCount": 1,
	}, true); err != nil {
		return nil, err
	}
	if _, err = e.manager.SendCommand(ctx, targetID, "Input.dispatchMouseEvent", map[string]any{
		"type":       "mouseReleased",
		"x":          x,
		"y":          y,
		"button":     "left",
		"clickCount": 1,
	}, true); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "x": x, "y": y}, nil
}

func (e *ExtensionEngine) typeText(ctx context.Context, targetID, selector, text string) (any, error) {
	if _, err := e.click(ctx, targetID, selector); err != nil {
		return nil, err
	}
	if _, err := e.manager.SendCommand(ctx, targetID, "Input.insertText", map[string]any{"text": text}, true); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

func (e *ExtensionEngine) press(ctx context.Context, targetID, key string) (any, error) {
	if _, err := e.manager.SendCommand(ctx, targetID, "Input.dispatchKeyEvent", map[string]any{
		"type": "keyDown",
		"key":  key,
	}, true); err != nil {
		return nil, err
	}
	if _, err := e.manager.SendCommand(ctx, targetID, "Input.dispatchKeyEvent", map[string]any{
		"type": "keyUp",
		"key":  key,
	}, true); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

func (e *ExtensionEngine) snapshot(ctx context.Context, targetID string) (any, error) {
	raw, err := e.manager.SendCommand(ctx, targetID, "Accessibility.getFullAXTree", nil, true)
	if err == nil {
		return map[string]any{"kind": "ax_tree", "value": decodeRawResult(raw)}, nil
	}
	fallback, fallbackErr := e.manager.SendCommand(ctx, targetID, "DOMSnapshot.captureSnapshot", map[string]any{
		"computedStyles": []string{},
	}, true)
	if fallbackErr != nil {
		return nil, err
	}
	return map[string]any{"kind": "dom_snapshot", "value": decodeRawResult(fallback)}, nil
}

func (e *ExtensionEngine) wait(
	ctx context.Context,
	targetID string,
	expression string,
	timeoutMS int,
	intervalMS int,
) (any, error) {
	if timeoutMS <= 0 {
		timeoutMS = 10000
	}
	if intervalMS <= 0 {
		intervalMS = 250
	}
	deadline := time.Now().Add(time.Duration(timeoutMS) * time.Millisecond)
	wrapped := "(() => { try { return !!(" + expression + "); } catch (_) { return false; } })()"
	for {
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("wait condition timed out")
		}
		raw, err := e.manager.SendCommand(ctx, targetID, "Runtime.evaluate", map[string]any{
			"expression":    wrapped,
			"returnByValue": true,
			"awaitPromise":  true,
		}, true)
		if err != nil {
			return nil, err
		}
		if truthy, parseErr := runtimeEvaluateTruthy(raw); parseErr == nil && truthy {
			return map[string]any{"ok": true}, nil
		}
		select {
		case <-ctx.Done():
			return nil, ErrRequestCanceled
		case <-time.After(time.Duration(intervalMS) * time.Millisecond):
		}
	}
}

func (e *ExtensionEngine) resolveElementCenter(
	ctx context.Context,
	targetID string,
	selector string,
) (float64, float64, error) {
	expr := fmt.Sprintf(`(() => {
		const el = document.querySelector(%s);
		if (!el) return {ok:false,error:"not_found"};
		const r = el.getBoundingClientRect();
		return {ok:true,x:r.left + (r.width / 2), y:r.top + (r.height / 2)};
	})()`, strconv.Quote(selector))

	raw, err := e.manager.SendCommand(ctx, targetID, "Runtime.evaluate", map[string]any{
		"expression":    expr,
		"returnByValue": true,
		"awaitPromise":  true,
	}, true)
	if err != nil {
		return 0, 0, err
	}

	value, parseErr := runtimeEvaluateValue(raw)
	if parseErr != nil {
		return 0, 0, parseErr
	}
	obj, ok := value.(map[string]any)
	if !ok {
		return 0, 0, fmt.Errorf("unexpected evaluation result type")
	}
	okValue, _ := obj["ok"].(bool)
	if !okValue {
		if msg, _ := obj["error"].(string); msg != "" {
			return 0, 0, errors.New(msg)
		}
		return 0, 0, fmt.Errorf("selector resolution failed")
	}

	x, okX := toFloat(obj["x"])
	y, okY := toFloat(obj["y"])
	if !okX || !okY {
		return 0, 0, fmt.Errorf("invalid click coordinates")
	}
	return x, y, nil
}

func runtimeEvaluateValue(raw json.RawMessage) (any, error) {
	var payload struct {
		Result struct {
			Value any `json:"value"`
		} `json:"result"`
		ExceptionDetails any `json:"exceptionDetails"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}
	if payload.ExceptionDetails != nil {
		return nil, fmt.Errorf("runtime evaluate exception")
	}
	return payload.Result.Value, nil
}

func runtimeEvaluateTruthy(raw json.RawMessage) (bool, error) {
	value, err := runtimeEvaluateValue(raw)
	if err != nil {
		return false, err
	}
	truthy, ok := value.(bool)
	if !ok {
		return false, fmt.Errorf("runtime evaluate did not return bool")
	}
	return truthy, nil
}

func decodeRawResult(raw json.RawMessage) any {
	if len(raw) == 0 {
		return map[string]any{}
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return map[string]any{"raw": string(raw)}
	}
	return decoded
}

func toFloat(value any) (float64, bool) {
	switch v := value.(type) {
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case json.Number:
		f, err := v.Float64()
		if err == nil {
			return f, true
		}
	}
	return 0, false
}
