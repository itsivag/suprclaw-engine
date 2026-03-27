package browserrelay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	defaultWaitTimeoutMS       = 10000
	defaultWaitIntervalMS      = 250
	defaultBatchMaxSteps       = 32
	defaultSnapshotRefMax      = 120
	defaultSnapshotRefTTL      = 10 * time.Minute
	defaultSnapshotGenerations = 4
	networkIdleQuietWindow     = 750 * time.Millisecond
)

type snapshotRef struct {
	Ref      string
	Selector string
	Role     string
	Name     string
	Text     string
}

type snapshotGeneration struct {
	Generation string
	CreatedAt  time.Time
	Refs       map[string]snapshotRef
	Order      []string
}

// ExtensionEngine executes browser actions through the extension relay manager.
type ExtensionEngine struct {
	manager *Manager

	mu                sync.Mutex
	snapshots         map[string][]snapshotGeneration
	generationCounter uint64
}

func NewExtensionEngine(manager *Manager) *ExtensionEngine {
	return &ExtensionEngine{
		manager:   manager,
		snapshots: make(map[string][]snapshotGeneration),
	}
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

func (e *ExtensionEngine) ExecuteAction(ctx context.Context, action string, req ActionRequest) (any, error) {
	return e.executeActionInternal(ctx, action, req, true)
}

func (e *ExtensionEngine) executeActionInternal(
	ctx context.Context,
	action string,
	req ActionRequest,
	allowBatch bool,
) (any, error) {
	targetID := extensionTargetRawID(req.TargetID)
	switch strings.TrimSpace(action) {
	case "batch":
		if !allowBatch {
			return nil, fmt.Errorf("%w: nested batch is not supported", ErrUnsupportedAction)
		}
		return e.executeBatch(ctx, targetID, req)
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
		selector, err := e.resolveSelectorRef(targetID, req.Selector, req.RefGeneration)
		if err != nil {
			return nil, err
		}
		return e.click(ctx, targetID, selector)
	case "type":
		if targetID == "" || req.Selector == "" {
			return nil, fmt.Errorf("target_id and selector are required")
		}
		selector, err := e.resolveSelectorRef(targetID, req.Selector, req.RefGeneration)
		if err != nil {
			return nil, err
		}
		return e.typeText(ctx, targetID, selector, req.Text)
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
		if targetID == "" {
			return nil, fmt.Errorf("target_id is required")
		}
		waitMode := strings.ToLower(strings.TrimSpace(req.WaitMode))
		switch waitMode {
		case "", "expression":
			if strings.TrimSpace(req.Expression) == "" {
				return nil, fmt.Errorf("target_id and expression are required")
			}
			return e.waitExpression(ctx, targetID, req.Expression, req.TimeoutMS, req.IntervalMS)
		case "selector":
			if strings.TrimSpace(req.Selector) == "" {
				return nil, fmt.Errorf("target_id and selector are required")
			}
			selector, err := e.resolveSelectorRef(targetID, req.Selector, req.RefGeneration)
			if err != nil {
				return nil, err
			}
			return e.waitSelector(ctx, targetID, selector, req.TimeoutMS, req.IntervalMS)
		case "navigation":
			return e.waitNavigation(ctx, targetID, req.TimeoutMS, req.IntervalMS)
		case "network_idle":
			return e.waitNetworkIdle(ctx, targetID, req.TimeoutMS, req.IntervalMS)
		default:
			return nil, fmt.Errorf("unsupported wait_mode %q", waitMode)
		}
	default:
		return nil, fmt.Errorf("%w: %s", ErrUnsupportedAction, action)
	}
}

func (e *ExtensionEngine) executeBatch(ctx context.Context, targetID string, req ActionRequest) (any, error) {
	if targetID == "" || len(req.Steps) == 0 {
		return nil, fmt.Errorf("target_id and steps are required")
	}
	if len(req.Steps) > defaultBatchMaxSteps {
		return nil, fmt.Errorf("steps exceeds max batch size of %d", defaultBatchMaxSteps)
	}
	stopOnError := true
	if req.StopOnErrorSet {
		stopOnError = req.StopOnError
	}

	results := make([]map[string]any, 0, len(req.Steps))
	successCount := 0
	failedCount := 0
	start := time.Now()

	for i, step := range req.Steps {
		stepStart := time.Now()
		stepAction := strings.TrimSpace(step.Action)
		stepReq := ActionRequest{
			TargetID:      targetID,
			URL:           step.URL,
			Selector:      step.Selector,
			Text:          step.Text,
			Key:           step.Key,
			Expression:    step.Expression,
			WaitMode:      step.WaitMode,
			RefGeneration: step.RefGeneration,
			TimeoutMS:     step.TimeoutMS,
			IntervalMS:    step.IntervalMS,
		}
		if explicit := extensionTargetRawID(step.TargetID); explicit != "" && explicit != targetID {
			err := fmt.Errorf("step[%d] target_id must match batch target_id", i)
			results = append(results, map[string]any{
				"index":       i,
				"action":      stepAction,
				"ok":          false,
				"error":       err.Error(),
				"duration_ms": time.Since(stepStart).Milliseconds(),
			})
			failedCount++
			if stopOnError {
				break
			}
			continue
		}

		if stepAction == "" || stepAction == "batch" {
			err := fmt.Errorf("%w: invalid batch step action %q", ErrUnsupportedAction, stepAction)
			results = append(results, map[string]any{
				"index":       i,
				"action":      stepAction,
				"ok":          false,
				"error":       err.Error(),
				"duration_ms": time.Since(stepStart).Milliseconds(),
			})
			failedCount++
			if stopOnError {
				break
			}
			continue
		}

		stepResult, err := e.executeActionInternal(ctx, stepAction, stepReq, false)
		if err != nil {
			results = append(results, map[string]any{
				"index":       i,
				"action":      stepAction,
				"ok":          false,
				"error":       err.Error(),
				"duration_ms": time.Since(stepStart).Milliseconds(),
			})
			failedCount++
			if stopOnError {
				break
			}
			continue
		}

		successCount++
		results = append(results, map[string]any{
			"index":       i,
			"action":      stepAction,
			"ok":          true,
			"result":      stepResult,
			"duration_ms": time.Since(stepStart).Milliseconds(),
		})
	}

	return map[string]any{
		"ok":        failedCount == 0,
		"target_id": normalizeExtensionTargetID(targetID),
		"results":   results,
		"stats": map[string]any{
			"step_count":        len(req.Steps),
			"success_count":     successCount,
			"failed_count":      failedCount,
			"total_duration_ms": time.Since(start).Milliseconds(),
		},
	}, nil
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
	out := map[string]any{}
	raw, err := e.manager.SendCommand(ctx, targetID, "Accessibility.getFullAXTree", nil, true)
	if err == nil {
		out["kind"] = "ax_tree"
		out["value"] = decodeRawResult(raw)
	} else {
		fallback, fallbackErr := e.manager.SendCommand(ctx, targetID, "DOMSnapshot.captureSnapshot", map[string]any{
			"computedStyles": []string{},
		}, true)
		if fallbackErr != nil {
			return nil, err
		}
		out["kind"] = "dom_snapshot"
		out["value"] = decodeRawResult(fallback)
	}

	refs, generation, refErr := e.captureSnapshotRefs(ctx, targetID)
	if refErr == nil && len(refs) > 0 {
		out["refs"] = refs
		out["ref_generation"] = generation
	}
	return out, nil
}

func (e *ExtensionEngine) captureSnapshotRefs(
	ctx context.Context,
	targetID string,
) ([]map[string]any, string, error) {
	expr := `(() => {
		const max = 120;
		const picked = [];
		const seen = new Set();
		const isVisible = (el) => {
			const style = window.getComputedStyle(el);
			if (!style || style.display === "none" || style.visibility === "hidden") return false;
			const r = el.getBoundingClientRect();
			return r.width > 0 && r.height > 0;
		};
		const cssPath = (el) => {
			if (!el || el.nodeType !== 1) return "";
			if (el.id) return "#" + CSS.escape(el.id);
			const parts = [];
			let node = el;
			let depth = 0;
			while (node && node.nodeType === 1 && depth < 6) {
				let part = node.nodeName.toLowerCase();
				if (!part) break;
				const parent = node.parentElement;
				if (parent) {
					const siblings = Array.from(parent.children).filter((n) => n.nodeName === node.nodeName);
					if (siblings.length > 1) {
						const idx = siblings.indexOf(node) + 1;
						part += ":nth-of-type(" + idx + ")";
					}
				}
				parts.unshift(part);
				node = node.parentElement;
				depth += 1;
			}
			return parts.join(" > ");
		};
		const candidates = document.querySelectorAll("a,button,input,textarea,select,[role='button'],[role='link'],[data-testid],[onclick]");
		for (const el of candidates) {
			if (picked.length >= max) break;
			if (!isVisible(el)) continue;
			const selector = cssPath(el);
			if (!selector || seen.has(selector)) continue;
			seen.add(selector);
			picked.push({
				selector,
				role: ((el.getAttribute("role") || el.tagName || "").toLowerCase()),
				name: (el.getAttribute("aria-label") || el.getAttribute("name") || el.getAttribute("placeholder") || "").trim(),
				text: ((el.innerText || el.value || "").trim()).slice(0, 120)
			});
		}
		return picked;
	})()`

	raw, err := e.manager.SendCommand(ctx, targetID, "Runtime.evaluate", map[string]any{
		"expression":    expr,
		"returnByValue": true,
		"awaitPromise":  true,
	}, true)
	if err != nil {
		return nil, "", err
	}
	value, parseErr := runtimeEvaluateValue(raw)
	if parseErr != nil {
		return nil, "", parseErr
	}
	list, ok := value.([]any)
	if !ok {
		return nil, "", fmt.Errorf("snapshot refs expected array")
	}
	if len(list) == 0 {
		return nil, "", nil
	}
	if len(list) > defaultSnapshotRefMax {
		list = list[:defaultSnapshotRefMax]
	}

	nextID := atomic.AddUint64(&e.generationCounter, 1)
	generation := fmt.Sprintf("g-%d", nextID)
	refs := make([]map[string]any, 0, len(list))
	gen := snapshotGeneration{
		Generation: generation,
		CreatedAt:  time.Now().UTC(),
		Refs:       make(map[string]snapshotRef, len(list)),
		Order:      make([]string, 0, len(list)),
	}

	for i, item := range list {
		obj, ok := item.(map[string]any)
		if !ok {
			continue
		}
		selector := strings.TrimSpace(anyToString(obj["selector"]))
		if selector == "" {
			continue
		}
		ref := fmt.Sprintf("@e%d", i+1)
		entry := snapshotRef{
			Ref:      ref,
			Selector: selector,
			Role:     strings.TrimSpace(anyToString(obj["role"])),
			Name:     strings.TrimSpace(anyToString(obj["name"])),
			Text:     strings.TrimSpace(anyToString(obj["text"])),
		}
		gen.Refs[ref] = entry
		gen.Order = append(gen.Order, ref)
		refs = append(refs, map[string]any{
			"ref":      ref,
			"selector": selector,
			"role":     entry.Role,
			"name":     entry.Name,
			"text":     entry.Text,
		})
	}

	e.mu.Lock()
	e.pruneSnapshotCacheLocked(targetID)
	e.snapshots[targetID] = append(e.snapshots[targetID], gen)
	if len(e.snapshots[targetID]) > defaultSnapshotGenerations {
		e.snapshots[targetID] = e.snapshots[targetID][len(e.snapshots[targetID])-defaultSnapshotGenerations:]
	}
	e.mu.Unlock()

	return refs, generation, nil
}

func (e *ExtensionEngine) pruneSnapshotCacheLocked(targetID string) {
	gens := e.snapshots[targetID]
	if len(gens) == 0 {
		return
	}
	cutoff := time.Now().Add(-defaultSnapshotRefTTL)
	kept := make([]snapshotGeneration, 0, len(gens))
	for _, g := range gens {
		if g.CreatedAt.After(cutoff) {
			kept = append(kept, g)
		}
	}
	if len(kept) == 0 {
		delete(e.snapshots, targetID)
		return
	}
	e.snapshots[targetID] = kept
}

func (e *ExtensionEngine) resolveSelectorRef(targetID, selector, generation string) (string, error) {
	selector = strings.TrimSpace(selector)
	if !strings.HasPrefix(selector, "@e") {
		return selector, nil
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	e.pruneSnapshotCacheLocked(targetID)
	gens := e.snapshots[targetID]
	if len(gens) == 0 {
		return "", fmt.Errorf("%w: no active snapshot refs for target", ErrSnapshotRefNotFound)
	}

	var chosen *snapshotGeneration
	if strings.TrimSpace(generation) != "" {
		for i := range gens {
			if gens[i].Generation == generation {
				chosen = &gens[i]
				break
			}
		}
	} else {
		chosen = &gens[len(gens)-1]
	}
	if chosen == nil {
		return "", fmt.Errorf("%w: generation %q", ErrSnapshotRefNotFound, generation)
	}
	entry, ok := chosen.Refs[selector]
	if !ok || strings.TrimSpace(entry.Selector) == "" {
		return "", fmt.Errorf("%w: ref %q", ErrSnapshotRefNotFound, selector)
	}
	return entry.Selector, nil
}

func (e *ExtensionEngine) waitExpression(
	ctx context.Context,
	targetID string,
	expression string,
	timeoutMS int,
	intervalMS int,
) (any, error) {
	expr := "(() => { try { return !!(" + expression + "); } catch (_) { return false; } })()"
	return e.waitForTruthyEval(ctx, targetID, expr, timeoutMS, intervalMS)
}

func (e *ExtensionEngine) waitSelector(
	ctx context.Context,
	targetID string,
	selector string,
	timeoutMS int,
	intervalMS int,
) (any, error) {
	expr := fmt.Sprintf(`(() => {
		const el = document.querySelector(%s);
		if (!el) return false;
		const style = window.getComputedStyle(el);
		if (!style) return false;
		if (style.display === "none" || style.visibility === "hidden") return false;
		const r = el.getBoundingClientRect();
		return r.width > 0 && r.height > 0;
	})()`, strconv.Quote(selector))
	return e.waitForTruthyEval(ctx, targetID, expr, timeoutMS, intervalMS)
}

func (e *ExtensionEngine) waitNavigation(
	ctx context.Context,
	targetID string,
	timeoutMS int,
	intervalMS int,
) (any, error) {
	timeoutMS, intervalMS = normalizeWaitDurations(timeoutMS, intervalMS)
	_, _ = e.manager.SendCommand(ctx, targetID, "Page.enable", nil, true)

	_, events, cancel := e.manager.SubscribeTargetEvents(targetID)
	defer cancel()

	timer := time.NewTimer(time.Duration(timeoutMS) * time.Millisecond)
	defer timer.Stop()
	ticker := time.NewTicker(time.Duration(intervalMS) * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil, ErrRequestCanceled
		case <-timer.C:
			return nil, fmt.Errorf("wait condition timed out")
		case event, ok := <-events:
			if !ok {
				continue
			}
			switch event.Method {
			case "Page.loadEventFired":
				return map[string]any{"ok": true, "mode": "navigation", "event": event.Method}, nil
			case "Page.lifecycleEvent":
				name := eventStringField(event.Params, "name")
				if name == "load" || name == "networkIdle" {
					return map[string]any{"ok": true, "mode": "navigation", "event": event.Method, "name": name}, nil
				}
			}
		case <-ticker.C:
			ready, err := e.readyStateComplete(ctx, targetID)
			if err == nil && ready {
				return map[string]any{"ok": true, "mode": "navigation", "fallback": "ready_state_complete"}, nil
			}
		}
	}
}

func (e *ExtensionEngine) waitNetworkIdle(
	ctx context.Context,
	targetID string,
	timeoutMS int,
	intervalMS int,
) (any, error) {
	timeoutMS, intervalMS = normalizeWaitDurations(timeoutMS, intervalMS)
	_, _ = e.manager.SendCommand(ctx, targetID, "Network.enable", nil, true)

	_, events, cancel := e.manager.SubscribeTargetEvents(targetID)
	defer cancel()

	timer := time.NewTimer(time.Duration(timeoutMS) * time.Millisecond)
	defer timer.Stop()
	ticker := time.NewTicker(time.Duration(intervalMS) * time.Millisecond)
	defer ticker.Stop()

	inFlight := map[string]struct{}{}
	seenActivity := false
	idleSince := time.Now()

	for {
		if len(inFlight) == 0 && seenActivity && time.Since(idleSince) >= networkIdleQuietWindow {
			return map[string]any{"ok": true, "mode": "network_idle"}, nil
		}

		select {
		case <-ctx.Done():
			return nil, ErrRequestCanceled
		case <-timer.C:
			return nil, fmt.Errorf("wait condition timed out")
		case event, ok := <-events:
			if !ok {
				continue
			}
			switch event.Method {
			case "Network.requestWillBeSent":
				requestID := eventStringField(event.Params, "requestId")
				if requestID != "" {
					inFlight[requestID] = struct{}{}
				}
				seenActivity = true
			case "Network.loadingFinished", "Network.loadingFailed":
				requestID := eventStringField(event.Params, "requestId")
				if requestID != "" {
					delete(inFlight, requestID)
				}
				seenActivity = true
				if len(inFlight) == 0 {
					idleSince = time.Now()
				}
			}
		case <-ticker.C:
			if len(inFlight) != 0 {
				continue
			}
			ready, err := e.readyStateComplete(ctx, targetID)
			if err == nil && ready {
				if time.Since(idleSince) >= networkIdleQuietWindow {
					return map[string]any{"ok": true, "mode": "network_idle", "fallback": "ready_state_complete"}, nil
				}
			}
		}
	}
}

func (e *ExtensionEngine) waitForTruthyEval(
	ctx context.Context,
	targetID string,
	expression string,
	timeoutMS int,
	intervalMS int,
) (any, error) {
	timeoutMS, intervalMS = normalizeWaitDurations(timeoutMS, intervalMS)
	deadline := time.Now().Add(time.Duration(timeoutMS) * time.Millisecond)
	for {
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("wait condition timed out")
		}
		raw, err := e.manager.SendCommand(ctx, targetID, "Runtime.evaluate", map[string]any{
			"expression":    expression,
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

func normalizeWaitDurations(timeoutMS, intervalMS int) (int, int) {
	if timeoutMS <= 0 {
		timeoutMS = defaultWaitTimeoutMS
	}
	if intervalMS <= 0 {
		intervalMS = defaultWaitIntervalMS
	}
	return timeoutMS, intervalMS
}

func (e *ExtensionEngine) readyStateComplete(ctx context.Context, targetID string) (bool, error) {
	raw, err := e.manager.SendCommand(ctx, targetID, "Runtime.evaluate", map[string]any{
		"expression":    "document.readyState === 'complete'",
		"returnByValue": true,
		"awaitPromise":  true,
	}, true)
	if err != nil {
		return false, err
	}
	return runtimeEvaluateTruthy(raw)
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

func anyToString(v any) string {
	switch val := v.(type) {
	case string:
		return val
	case fmt.Stringer:
		return val.String()
	default:
		return fmt.Sprintf("%v", v)
	}
}

func eventStringField(raw json.RawMessage, key string) string {
	if len(raw) == 0 || strings.TrimSpace(key) == "" {
		return ""
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return ""
	}
	return strings.TrimSpace(anyToString(obj[key]))
}
