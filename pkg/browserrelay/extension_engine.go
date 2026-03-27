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

	"github.com/itsivag/suprclaw/pkg/logger"
)

const (
	defaultWaitTimeoutMS       = 10000
	defaultWaitIntervalMS      = 250
	defaultBatchMaxSteps       = 32
	defaultSnapshotRefMax      = 120
	defaultSnapshotRefTTL      = time.Duration(defaultSnapshotRefTTLSec) * time.Second
	defaultSnapshotGenerations = defaultSnapshotMaxGenerations
	networkIdleQuietWindow     = 750 * time.Millisecond

	snapshotModeCompact = "compact"
	snapshotModeFull    = "full"
)

type snapshotRef struct {
	Ref      string
	Selector string
	Role     string
	Name     string
	Text     string
	Tag      string
}

type snapshotGeneration struct {
	Generation string
	CreatedAt  time.Time
	Refs       map[string]snapshotRef
	Order      []string
}

type snapshotElement struct {
	Selector string
	Role     string
	Name     string
	Text     string
	Tag      string
}

type snapshotPolicy struct {
	DefaultMode            string
	MaxPayloadBytes        int
	MaxNodes               int
	MaxTextChars           int
	MaxDepth               int
	InteractiveOnlyDefault bool
	RefTTL                 time.Duration
	MaxGenerations         int
	AllowFullTree          bool
}

// ExtensionEngine executes browser actions through the extension relay manager.
type ExtensionEngine struct {
	manager *Manager

	mu                sync.Mutex
	snapshots         map[string][]snapshotGeneration
	generationCounter uint64

	snapshotCompactTotal  uint64
	snapshotFullTotal     uint64
	snapshotTruncateTotal uint64
	snapshotTooLargeTotal uint64
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
		return e.snapshot(ctx, targetID, req)
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
			TargetID:        targetID,
			URL:             step.URL,
			Selector:        step.Selector,
			Text:            step.Text,
			Key:             step.Key,
			Expression:      step.Expression,
			WaitMode:        step.WaitMode,
			RefGeneration:   step.RefGeneration,
			SnapshotMode:    step.SnapshotMode,
			InteractiveOnly: step.InteractiveOnly,
			ScopeSelector:   step.ScopeSelector,
			Depth:           step.Depth,
			MaxNodes:        step.MaxNodes,
			MaxTextChars:    step.MaxTextChars,
			TimeoutMS:       step.TimeoutMS,
			IntervalMS:      step.IntervalMS,
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

func (e *ExtensionEngine) snapshot(ctx context.Context, targetID string, req ActionRequest) (any, error) {
	start := time.Now()
	policy := e.snapshotPolicy()
	mode, err := normalizeSnapshotMode(req.SnapshotMode, policy.DefaultMode)
	if err != nil {
		return nil, err
	}

	if mode == snapshotModeFull {
		atomic.AddUint64(&e.snapshotFullTotal, 1)
		if !policy.AllowFullTree {
			return nil, fmt.Errorf("%w: full mode disabled", ErrSnapshotModeUnsupported)
		}
	} else {
		atomic.AddUint64(&e.snapshotCompactTotal, 1)
	}

	options := e.snapshotOptions(req, policy)
	page, elements, compactRawBytes, err := e.captureCompactSnapshot(ctx, targetID, options)
	if err != nil {
		return nil, err
	}
	refs, generation, elementPayload, refTruncated, err := e.captureSnapshotRefs(targetID, elements, policy)
	if err != nil {
		return nil, err
	}

	rawBytes := compactRawBytes
	out := map[string]any{
		"mode":           mode,
		"page":           page,
		"refs":           refs,
		"ref_generation": generation,
		"elements":       elementPayload,
		"truncated":      refTruncated,
		"stats": map[string]any{
			"raw_bytes":    compactRawBytes,
			"output_bytes": 0,
		},
	}

	if mode == snapshotModeFull {
		fullKind, fullTree, fullRawBytes, fullErr := e.captureFullSnapshot(ctx, targetID)
		if fullErr != nil {
			return nil, fullErr
		}
		out["full_tree_kind"] = fullKind
		out["full_tree"] = fullTree
		rawBytes += fullRawBytes
	}

	stats := out["stats"].(map[string]any)
	stats["raw_bytes"] = rawBytes
	outputBytes, capTruncated, capErr := e.enforceSnapshotPayloadCap(out, policy.MaxPayloadBytes, mode)
	if capErr != nil {
		atomic.AddUint64(&e.snapshotTooLargeTotal, 1)
		return nil, capErr
	}
	if refTruncated || capTruncated {
		out["truncated"] = true
		atomic.AddUint64(&e.snapshotTruncateTotal, 1)
	}
	stats["output_bytes"] = outputBytes

	logger.DebugCF("browser-relay", "snapshot generated", map[string]any{
		"mode":         mode,
		"target":       normalizeExtensionTargetID(targetID),
		"ref_count":    len(refs),
		"raw_bytes":    rawBytes,
		"output_bytes": outputBytes,
		"truncated":    out["truncated"],
		"queue_depth":  e.manager.QueueDepth(targetID),
		"latency_ms":   time.Since(start).Milliseconds(),
	})
	return out, nil
}

type snapshotOptions struct {
	InteractiveOnly bool
	ScopeSelector   string
	Depth           int
	MaxNodes        int
	MaxTextChars    int
}

func (e *ExtensionEngine) snapshotPolicy() snapshotPolicy {
	cfg := e.manager.Config()
	p := snapshotPolicy{
		DefaultMode:            strings.ToLower(strings.TrimSpace(cfg.SnapshotDefaultMode)),
		MaxPayloadBytes:        cfg.SnapshotMaxPayloadBytes,
		MaxNodes:               cfg.SnapshotMaxNodes,
		MaxTextChars:           cfg.SnapshotMaxTextChars,
		MaxDepth:               cfg.SnapshotMaxDepth,
		InteractiveOnlyDefault: cfg.SnapshotInteractiveOnly,
		RefTTL:                 time.Duration(cfg.SnapshotRefTTLSec) * time.Second,
		MaxGenerations:         cfg.SnapshotMaxGenerations,
		AllowFullTree:          cfg.SnapshotAllowFullTree,
	}
	if p.DefaultMode == "" {
		p.DefaultMode = snapshotModeCompact
	}
	if p.MaxPayloadBytes <= 0 {
		p.MaxPayloadBytes = defaultSnapshotMaxPayloadBytes
	}
	if p.MaxNodes <= 0 {
		p.MaxNodes = defaultSnapshotRefMax
	}
	if p.MaxTextChars <= 0 {
		p.MaxTextChars = defaultSnapshotMaxTextChars
	}
	if p.MaxDepth <= 0 {
		p.MaxDepth = defaultSnapshotMaxDepth
	}
	if p.RefTTL <= 0 {
		p.RefTTL = defaultSnapshotRefTTL
	}
	if p.MaxGenerations <= 0 {
		p.MaxGenerations = defaultSnapshotGenerations
	}
	return p
}

func (e *ExtensionEngine) snapshotOptions(req ActionRequest, policy snapshotPolicy) snapshotOptions {
	interactiveOnly := policy.InteractiveOnlyDefault
	if req.InteractiveOnly != nil {
		interactiveOnly = *req.InteractiveOnly
	}
	return snapshotOptions{
		InteractiveOnly: interactiveOnly,
		ScopeSelector:   strings.TrimSpace(req.ScopeSelector),
		Depth:           clampBoundedInt(req.Depth, policy.MaxDepth, 1, 10),
		MaxNodes:        clampBoundedInt(req.MaxNodes, policy.MaxNodes, 1, 300),
		MaxTextChars:    clampBoundedInt(req.MaxTextChars, policy.MaxTextChars, 16, 1024),
	}
}

func normalizeSnapshotMode(requested, fallback string) (string, error) {
	mode := strings.ToLower(strings.TrimSpace(requested))
	if mode == "" {
		mode = strings.ToLower(strings.TrimSpace(fallback))
	}
	if mode == "" {
		mode = snapshotModeCompact
	}
	switch mode {
	case snapshotModeCompact, snapshotModeFull:
		return mode, nil
	default:
		return "", fmt.Errorf("%w: %s", ErrSnapshotModeUnsupported, mode)
	}
}

func clampBoundedInt(value, fallback, minValue, maxValue int) int {
	if value <= 0 {
		value = fallback
	}
	if value < minValue {
		return minValue
	}
	if maxValue > 0 && value > maxValue {
		return maxValue
	}
	return value
}

func (e *ExtensionEngine) captureCompactSnapshot(
	ctx context.Context,
	targetID string,
	options snapshotOptions,
) (map[string]any, []snapshotElement, int, error) {
	expr := fmt.Sprintf(`(() => {
		const scopeSelector = %s;
		const maxNodes = %d;
		const maxTextChars = %d;
		const maxDepth = %d;
		const interactiveOnly = %t;
		const interactiveSelector = "a,button,input,textarea,select,summary,[role='button'],[role='link'],[data-testid],[onclick],[tabindex]";
		const root = scopeSelector ? document.querySelector(scopeSelector) : document;
		if (!root) {
			return { ok: false, error: "scope_not_found" };
		}

		const isVisible = (el) => {
			if (!el || !(el instanceof Element)) return false;
			const style = window.getComputedStyle(el);
			if (!style || style.display === "none" || style.visibility === "hidden") return false;
			const r = el.getBoundingClientRect();
			return r.width > 0 && r.height > 0;
		};

		const isInteractive = (el) => {
			if (!el || !(el instanceof Element)) return false;
			if (el.matches(interactiveSelector)) return true;
			const role = (el.getAttribute("role") || "").toLowerCase();
			if (role === "button" || role === "link" || role === "textbox" || role === "combobox") return true;
			const tabIndex = el.getAttribute("tabindex");
			return tabIndex !== null && tabIndex !== "-1";
		};

		const withinDepth = (el) => {
			let depth = 0;
			let node = el;
			while (node && node !== root && node.parentElement) {
				depth += 1;
				if (depth > maxDepth) return false;
				node = node.parentElement;
			}
			return true;
		};

		const cssPath = (el) => {
			if (!el || el.nodeType !== 1) return "";
			if (el.id) return "#" + CSS.escape(el.id);
			const parts = [];
			let node = el;
			let depth = 0;
			const pathDepth = Math.max(2, maxDepth);
			while (node && node.nodeType === 1 && depth < pathDepth) {
				let part = node.nodeName.toLowerCase();
				if (!part) break;
				const parent = node.parentElement;
				if (parent) {
					const siblings = Array.from(parent.children).filter((n) => n.nodeName === node.nodeName);
					if (siblings.length > 1) {
						part += ":nth-of-type(" + (siblings.indexOf(node) + 1) + ")";
					}
				}
				parts.unshift(part);
				node = parent;
				depth += 1;
			}
			return parts.join(" > ");
		};

		const source = interactiveOnly ? root.querySelectorAll(interactiveSelector) : root.querySelectorAll("*");
		const picked = [];
		const seen = new Set();
		for (const el of source) {
			if (picked.length >= maxNodes) break;
			if (!withinDepth(el)) continue;
			if (!isVisible(el)) continue;
			if (interactiveOnly && !isInteractive(el)) continue;
			const selector = cssPath(el);
			if (!selector || seen.has(selector)) continue;
			seen.add(selector);
			const tag = ((el.tagName || "").toLowerCase());
			const role = ((el.getAttribute("role") || tag || "").toLowerCase());
			const name = (el.getAttribute("aria-label") || el.getAttribute("name") || el.getAttribute("placeholder") || "").trim();
			const text = ((el.innerText || el.textContent || el.value || "").replace(/\s+/g, " ").trim()).slice(0, maxTextChars);
			picked.push({ selector, role, name, text, tag });
		}

		return {
			ok: true,
			page: { url: String(location.href || ""), title: String(document.title || "") },
			elements: picked,
		};
	})()`, strconv.Quote(options.ScopeSelector), options.MaxNodes, options.MaxTextChars, options.Depth, options.InteractiveOnly)

	raw, err := e.manager.SendCommand(ctx, targetID, "Runtime.evaluate", map[string]any{
		"expression":    expr,
		"returnByValue": true,
		"awaitPromise":  true,
	}, true)
	if err != nil {
		return nil, nil, 0, err
	}
	value, parseErr := runtimeEvaluateValue(raw)
	if parseErr != nil {
		return nil, nil, 0, parseErr
	}
	obj, ok := value.(map[string]any)
	if !ok {
		return nil, nil, 0, fmt.Errorf("snapshot compact expected object")
	}
	if okValue, _ := obj["ok"].(bool); !okValue {
		if strings.TrimSpace(anyToString(obj["error"])) == "scope_not_found" {
			return nil, nil, 0, fmt.Errorf("%w: %s", ErrSnapshotScopeNotFound, options.ScopeSelector)
		}
		return nil, nil, 0, fmt.Errorf("snapshot compact evaluation failed")
	}

	pageObj, _ := obj["page"].(map[string]any)
	page := map[string]any{
		"url":   strings.TrimSpace(anyToString(pageObj["url"])),
		"title": strings.TrimSpace(anyToString(pageObj["title"])),
	}
	rawElements, _ := obj["elements"].([]any)
	elements := make([]snapshotElement, 0, len(rawElements))
	for _, item := range rawElements {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		selector := strings.TrimSpace(anyToString(entry["selector"]))
		if selector == "" {
			continue
		}
		elements = append(elements, snapshotElement{
			Selector: selector,
			Role:     strings.TrimSpace(anyToString(entry["role"])),
			Name:     strings.TrimSpace(anyToString(entry["name"])),
			Text:     strings.TrimSpace(anyToString(entry["text"])),
			Tag:      strings.TrimSpace(anyToString(entry["tag"])),
		})
	}
	return page, elements, len(raw), nil
}

func (e *ExtensionEngine) captureFullSnapshot(
	ctx context.Context,
	targetID string,
) (string, any, int, error) {
	raw, err := e.manager.SendCommand(ctx, targetID, "Accessibility.getFullAXTree", nil, true)
	if err == nil {
		return "ax_tree", decodeRawResult(raw), len(raw), nil
	}
	fallback, fallbackErr := e.manager.SendCommand(ctx, targetID, "DOMSnapshot.captureSnapshot", map[string]any{
		"computedStyles": []string{},
	}, true)
	if fallbackErr != nil {
		return "", nil, 0, err
	}
	return "dom_snapshot", decodeRawResult(fallback), len(fallback), nil
}

func (e *ExtensionEngine) captureSnapshotRefs(
	targetID string,
	elements []snapshotElement,
	policy snapshotPolicy,
) ([]map[string]any, string, []map[string]any, bool, error) {
	trimmed := false
	if len(elements) > policy.MaxNodes {
		elements = elements[:policy.MaxNodes]
		trimmed = true
	}

	nextID := atomic.AddUint64(&e.generationCounter, 1)
	generation := fmt.Sprintf("g-%d", nextID)
	gen := snapshotGeneration{
		Generation: generation,
		CreatedAt:  time.Now().UTC(),
		Refs:       make(map[string]snapshotRef, len(elements)),
		Order:      make([]string, 0, len(elements)),
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	e.pruneSnapshotCacheLocked(targetID, policy.RefTTL)
	prevBySelector := make(map[string]string, len(elements))
	if existing := e.snapshots[targetID]; len(existing) > 0 {
		latest := existing[len(existing)-1]
		for _, ref := range latest.Order {
			entry, ok := latest.Refs[ref]
			if !ok || strings.TrimSpace(entry.Selector) == "" {
				continue
			}
			prevBySelector[entry.Selector] = ref
		}
	}
	usedRefs := make(map[string]struct{}, len(elements))
	nextRefIndex := e.nextRefIndexLocked(targetID)

	for _, item := range elements {
		if strings.TrimSpace(item.Selector) == "" {
			continue
		}
		ref := ""
		if prev := strings.TrimSpace(prevBySelector[item.Selector]); prev != "" {
			if _, exists := usedRefs[prev]; !exists {
				ref = prev
			}
		}
		if ref == "" {
			for {
				candidate := fmt.Sprintf("@e%d", nextRefIndex)
				nextRefIndex++
				if _, exists := usedRefs[candidate]; exists {
					continue
				}
				ref = candidate
				break
			}
		}
		usedRefs[ref] = struct{}{}
		gen.Refs[ref] = snapshotRef{
			Ref:      ref,
			Selector: item.Selector,
			Role:     item.Role,
			Name:     item.Name,
			Text:     item.Text,
			Tag:      item.Tag,
		}
		gen.Order = append(gen.Order, ref)
	}

	e.snapshots[targetID] = append(e.snapshots[targetID], gen)
	if len(e.snapshots[targetID]) > policy.MaxGenerations {
		e.snapshots[targetID] = e.snapshots[targetID][len(e.snapshots[targetID])-policy.MaxGenerations:]
	}

	refs := make([]map[string]any, 0, len(gen.Order))
	elementPayload := make([]map[string]any, 0, len(gen.Order))
	for _, ref := range gen.Order {
		entry := gen.Refs[ref]
		refs = append(refs, map[string]any{
			"ref":      entry.Ref,
			"selector": entry.Selector,
			"role":     entry.Role,
			"name":     entry.Name,
			"text":     entry.Text,
		})
		elementPayload = append(elementPayload, map[string]any{
			"ref":      entry.Ref,
			"selector": entry.Selector,
			"role":     entry.Role,
			"name":     entry.Name,
			"text":     entry.Text,
			"tag":      entry.Tag,
		})
	}
	return refs, generation, elementPayload, trimmed, nil
}

func (e *ExtensionEngine) nextRefIndexLocked(targetID string) int {
	maxIndex := 0
	for _, generation := range e.snapshots[targetID] {
		for ref := range generation.Refs {
			index := parseSnapshotRefIndex(ref)
			if index > maxIndex {
				maxIndex = index
			}
		}
	}
	if maxIndex <= 0 {
		return 1
	}
	return maxIndex + 1
}

func parseSnapshotRefIndex(ref string) int {
	ref = strings.TrimSpace(ref)
	if !strings.HasPrefix(ref, "@e") {
		return 0
	}
	num, err := strconv.Atoi(strings.TrimPrefix(ref, "@e"))
	if err != nil || num <= 0 {
		return 0
	}
	return num
}

func (e *ExtensionEngine) enforceSnapshotPayloadCap(
	payload map[string]any,
	maxPayloadBytes int,
	mode string,
) (int, bool, error) {
	encode := func() (int, error) {
		b, err := json.Marshal(payload)
		if err != nil {
			return 0, err
		}
		return len(b), nil
	}

	outputBytes, err := encode()
	if err != nil {
		return 0, false, err
	}
	if maxPayloadBytes <= 0 || outputBytes <= maxPayloadBytes {
		return outputBytes, false, nil
	}

	truncated := false
	if mode == snapshotModeFull {
		if _, ok := payload["full_tree"]; ok {
			delete(payload, "full_tree")
			payload["full_tree_omitted"] = true
			payload["truncated"] = true
			truncated = true
			outputBytes, err = encode()
			if err != nil {
				return 0, truncated, err
			}
			if outputBytes <= maxPayloadBytes {
				return outputBytes, truncated, nil
			}
		}
	}

	elements, _ := payload["elements"].([]map[string]any)
	refs, _ := payload["refs"].([]map[string]any)
	for len(elements) > 0 {
		elements = elements[:len(elements)-1]
		if len(refs) > len(elements) {
			refs = refs[:len(elements)]
		}
		payload["elements"] = elements
		payload["refs"] = refs
		payload["truncated"] = true
		truncated = true
		outputBytes, err = encode()
		if err != nil {
			return 0, truncated, err
		}
		if outputBytes <= maxPayloadBytes {
			return outputBytes, truncated, nil
		}
	}

	return 0, truncated, fmt.Errorf(
		"%w: %d > %d",
		ErrSnapshotPayloadTooLarge,
		outputBytes,
		maxPayloadBytes,
	)
}

func (e *ExtensionEngine) pruneSnapshotCacheLocked(targetID string, ttl time.Duration) {
	gens := e.snapshots[targetID]
	if len(gens) == 0 {
		return
	}
	if ttl <= 0 {
		ttl = defaultSnapshotRefTTL
	}
	cutoff := time.Now().Add(-ttl)
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
	e.pruneSnapshotCacheLocked(targetID, e.snapshotPolicy().RefTTL)
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
