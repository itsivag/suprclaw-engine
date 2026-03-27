package browserrelay

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func setupAttachedManager(t *testing.T, onWrite func(v any)) *Manager {
	t.Helper()
	return setupAttachedManagerWithConfig(t, Config{Enabled: true, IdleTimeoutSec: 60}, onWrite)
}

func setupAttachedManagerWithConfig(t *testing.T, cfg Config, onWrite func(v any)) *Manager {
	t.Helper()
	m := NewManager(cfg)
	t.Cleanup(m.Close)

	extConn := &fakeConn{onWrite: onWrite}
	m.AttachExtension("ext-1", extConn)

	if err := m.HandleExtensionMessage("ext-1", []byte(`{"type":"targets","targets":[{"id":"tab-1","title":"T"}]}`)); err != nil {
		t.Fatalf("targets message error: %v", err)
	}
	if err := m.HandleExtensionMessage("ext-1", []byte(`{"type":"attach","targetId":"tab-1"}`)); err != nil {
		t.Fatalf("attach error: %v", err)
	}
	return m
}

func TestExtensionEngineBatchStopOnError(t *testing.T) {
	var (
		mu          sync.Mutex
		methodCalls = map[string]int{}
		mgr         *Manager
	)
	mgr = setupAttachedManager(t, func(v any) {
		req, ok := v.(requestEnvelope)
		if !ok {
			return
		}
		mu.Lock()
		methodCalls[req.Method]++
		mu.Unlock()

		go func() {
			if req.Method == "Page.navigate" {
				_ = mgr.HandleExtensionMessage("ext-1", []byte(fmt.Sprintf(`{"type":"response","requestId":"%s","error":"nav failed"}`, req.RequestID)))
				return
			}
			_ = mgr.HandleExtensionMessage("ext-1", []byte(fmt.Sprintf(`{"type":"response","requestId":"%s","result":{"ok":true}}`, req.RequestID)))
		}()
	})

	engine := NewExtensionEngine(mgr)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	result, err := engine.ExecuteAction(ctx, "batch", ActionRequest{
		TargetID:    "tab-1",
		StopOnError: true,
		Steps: []BatchStep{
			{Action: "navigate", URL: "https://example.com"},
			{Action: "screenshot"},
		},
	})
	if err != nil {
		t.Fatalf("ExecuteAction(batch) error = %v", err)
	}
	payload, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("batch payload type = %T, want map[string]any", result)
	}
	if okValue, _ := payload["ok"].(bool); okValue {
		t.Fatalf("batch ok = true, want false, payload=%v", payload)
	}

	mu.Lock()
	defer mu.Unlock()
	if got := methodCalls["Page.navigate"]; got != 1 {
		t.Fatalf("Page.navigate calls = %d, want 1", got)
	}
	if got := methodCalls["Page.captureScreenshot"]; got != 0 {
		t.Fatalf("Page.captureScreenshot calls = %d, want 0 with stop_on_error", got)
	}
}

func TestManagerLoopGuardTriggersOnRepeatedFailure(t *testing.T) {
	var mgr *Manager
	mgr = setupAttachedManager(t, func(v any) {
		req, ok := v.(requestEnvelope)
		if !ok {
			return
		}
		go func() {
			_ = mgr.HandleExtensionMessage("ext-1", []byte(fmt.Sprintf(`{"type":"response","requestId":"%s","error":"same failure"}`, req.RequestID)))
		}()
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	for i := 0; i < 3; i++ {
		_, err := mgr.SendCommand(ctx, "tab-1", "Page.navigate", map[string]any{"url": "https://example.com"}, true)
		if err == nil {
			t.Fatalf("attempt %d expected failure", i+1)
		}
		if errors.Is(err, ErrRelayLoopGuardTriggered) {
			t.Fatalf("attempt %d should not trigger loop guard yet: %v", i+1, err)
		}
	}

	_, err := mgr.SendCommand(ctx, "tab-1", "Page.navigate", map[string]any{"url": "https://example.com"}, true)
	if !errors.Is(err, ErrRelayLoopGuardTriggered) {
		t.Fatalf("4th repeated failure error = %v, want ErrRelayLoopGuardTriggered", err)
	}
}

func TestExtensionEngineSelectorRefMissing(t *testing.T) {
	var mgr *Manager
	mgr = setupAttachedManager(t, func(v any) {
		req, ok := v.(requestEnvelope)
		if !ok {
			return
		}
		go func() {
			_ = mgr.HandleExtensionMessage("ext-1", []byte(fmt.Sprintf(`{"type":"response","requestId":"%s","result":{"ok":true}}`, req.RequestID)))
		}()
	})

	engine := NewExtensionEngine(mgr)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err := engine.ExecuteAction(ctx, "click", ActionRequest{
		TargetID: "tab-1",
		Selector: "@e1",
	})
	if !errors.Is(err, ErrSnapshotRefNotFound) {
		t.Fatalf("click(@e1) error = %v, want ErrSnapshotRefNotFound", err)
	}
}

func TestExtensionEngineSnapshotReturnsRefs(t *testing.T) {
	var mgr *Manager
	mgr = setupAttachedManager(t, func(v any) {
		req, ok := v.(requestEnvelope)
		if !ok {
			return
		}
		go func() {
			switch req.Method {
			case "Runtime.evaluate":
				_ = mgr.HandleExtensionMessage("ext-1", []byte(fmt.Sprintf(`{"type":"response","requestId":"%s","result":{"result":{"value":{"ok":true,"page":{"url":"https://example.com","title":"Example"},"elements":[{"selector":"#submit","role":"button","name":"Submit","text":"Submit","tag":"button"}]}}}}`, req.RequestID)))
			default:
				_ = mgr.HandleExtensionMessage("ext-1", []byte(fmt.Sprintf(`{"type":"response","requestId":"%s","result":{"ok":true}}`, req.RequestID)))
			}
		}()
	})

	engine := NewExtensionEngine(mgr)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	result, err := engine.ExecuteAction(ctx, "snapshot", ActionRequest{TargetID: "tab-1"})
	if err != nil {
		t.Fatalf("snapshot error = %v", err)
	}
	payload, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("snapshot payload type = %T, want map[string]any", result)
	}
	if strings.TrimSpace(anyToString(payload["ref_generation"])) == "" {
		t.Fatalf("ref_generation missing in snapshot payload: %v", payload)
	}
	refs, ok := payload["refs"].([]map[string]any)
	if !ok || len(refs) == 0 {
		t.Fatalf("refs = %T %v, want non-empty []map[string]any", payload["refs"], payload["refs"])
	}
	if mode := strings.TrimSpace(anyToString(payload["mode"])); mode != "compact" {
		t.Fatalf("mode = %q, want compact", mode)
	}
	if _, ok := payload["page"].(map[string]any); !ok {
		t.Fatalf("page type = %T, want map[string]any", payload["page"])
	}
	if _, ok := payload["elements"].([]map[string]any); !ok {
		t.Fatalf("elements type = %T, want []map[string]any", payload["elements"])
	}
	if _, exists := payload["full_tree"]; exists {
		t.Fatalf("full_tree should not exist in compact snapshot: %v", payload)
	}
}

func TestExtensionEngineSelectorRefExpired(t *testing.T) {
	var mgr *Manager
	mgr = setupAttachedManager(t, func(v any) {
		req, ok := v.(requestEnvelope)
		if !ok {
			return
		}
		go func() {
			_ = mgr.HandleExtensionMessage("ext-1", []byte(fmt.Sprintf(`{"type":"response","requestId":"%s","result":{"ok":true}}`, req.RequestID)))
		}()
	})

	engine := NewExtensionEngine(mgr)
	engine.mu.Lock()
	engine.snapshots["tab-1"] = []snapshotGeneration{
		{
			Generation: "g-old",
			CreatedAt:  time.Now().Add(-defaultSnapshotRefTTL - time.Minute),
			Refs: map[string]snapshotRef{
				"@e1": {Ref: "@e1", Selector: "#submit"},
			},
			Order: []string{"@e1"},
		},
	}
	engine.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := engine.ExecuteAction(ctx, "click", ActionRequest{
		TargetID: "tab-1",
		Selector: "@e1",
	})
	if !errors.Is(err, ErrSnapshotRefNotFound) {
		t.Fatalf("click(@e1) with expired refs error = %v, want ErrSnapshotRefNotFound", err)
	}
}

func TestExtensionEngineSnapshotScopeSelectorNotFound(t *testing.T) {
	var mgr *Manager
	mgr = setupAttachedManager(t, func(v any) {
		req, ok := v.(requestEnvelope)
		if !ok {
			return
		}
		go func() {
			if req.Method == "Runtime.evaluate" {
				_ = mgr.HandleExtensionMessage("ext-1", []byte(fmt.Sprintf(
					`{"type":"response","requestId":"%s","result":{"result":{"value":{"ok":false,"error":"scope_not_found"}}}}`,
					req.RequestID,
				)))
				return
			}
			_ = mgr.HandleExtensionMessage("ext-1", []byte(fmt.Sprintf(`{"type":"response","requestId":"%s","result":{"ok":true}}`, req.RequestID)))
		}()
	})

	engine := NewExtensionEngine(mgr)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := engine.ExecuteAction(ctx, "snapshot", ActionRequest{
		TargetID:      "tab-1",
		ScopeSelector: "#missing-scope",
	})
	if !errors.Is(err, ErrSnapshotScopeNotFound) {
		t.Fatalf("snapshot scope error = %v, want ErrSnapshotScopeNotFound", err)
	}
}

func TestExtensionEngineSnapshotFullModeOmitsTreeWhenOverCap(t *testing.T) {
	var mgr *Manager
	mgr = setupAttachedManagerWithConfig(t, Config{
		Enabled:                 true,
		IdleTimeoutSec:          60,
		SnapshotMaxPayloadBytes: 320,
		SnapshotAllowFullTree:   true,
		SnapshotInteractiveOnly: true,
	}, func(v any) {
		req, ok := v.(requestEnvelope)
		if !ok {
			return
		}
		go func() {
			switch req.Method {
			case "Runtime.evaluate":
				_ = mgr.HandleExtensionMessage("ext-1", []byte(fmt.Sprintf(`{"type":"response","requestId":"%s","result":{"result":{"value":{"ok":true,"page":{"url":"https://example.com","title":"Example"},"elements":[{"selector":"#submit","role":"button","name":"Submit","text":"Submit","tag":"button"}]}}}}`, req.RequestID)))
			case "Accessibility.getFullAXTree":
				_ = mgr.HandleExtensionMessage("ext-1", []byte(fmt.Sprintf(`{"type":"response","requestId":"%s","result":{"nodes":[{"name":{"value":"%s"}}]}}`, req.RequestID, strings.Repeat("x", 3000))))
			default:
				_ = mgr.HandleExtensionMessage("ext-1", []byte(fmt.Sprintf(`{"type":"response","requestId":"%s","result":{"ok":true}}`, req.RequestID)))
			}
		}()
	})

	engine := NewExtensionEngine(mgr)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	result, err := engine.ExecuteAction(ctx, "snapshot", ActionRequest{
		TargetID:        "tab-1",
		SnapshotMode:    "full",
		InteractiveOnly: boolPtr(true),
	})
	if err != nil {
		t.Fatalf("snapshot full mode error = %v", err)
	}
	payload, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("snapshot payload type = %T, want map[string]any", result)
	}
	if _, exists := payload["full_tree"]; exists {
		t.Fatalf("full_tree must be omitted when exceeding payload cap: %v", payload)
	}
	if omitted, _ := payload["full_tree_omitted"].(bool); !omitted {
		t.Fatalf("full_tree_omitted = %v, want true", payload["full_tree_omitted"])
	}
}

func TestExtensionEngineSnapshotStableRefsAcrossGenerations(t *testing.T) {
	var (
		mgr         *Manager
		evalCounter int
	)
	mgr = setupAttachedManager(t, func(v any) {
		req, ok := v.(requestEnvelope)
		if !ok {
			return
		}
		go func() {
			if req.Method == "Runtime.evaluate" {
				evalCounter++
				if evalCounter == 1 {
					_ = mgr.HandleExtensionMessage("ext-1", []byte(fmt.Sprintf(`{"type":"response","requestId":"%s","result":{"result":{"value":{"ok":true,"page":{"url":"https://example.com","title":"Example"},"elements":[{"selector":"#submit","role":"button","name":"Submit","text":"Submit","tag":"button"},{"selector":"#search","role":"textbox","name":"Search","text":"Search","tag":"input"}]}}}}`, req.RequestID)))
					return
				}
				_ = mgr.HandleExtensionMessage("ext-1", []byte(fmt.Sprintf(`{"type":"response","requestId":"%s","result":{"result":{"value":{"ok":true,"page":{"url":"https://example.com","title":"Example"},"elements":[{"selector":"#submit","role":"button","name":"Submit","text":"Submit","tag":"button"},{"selector":"#buy","role":"button","name":"Buy","text":"Buy","tag":"button"}]}}}}`, req.RequestID)))
				return
			}
			_ = mgr.HandleExtensionMessage("ext-1", []byte(fmt.Sprintf(`{"type":"response","requestId":"%s","result":{"ok":true}}`, req.RequestID)))
		}()
	})

	engine := NewExtensionEngine(mgr)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	first, err := engine.ExecuteAction(ctx, "snapshot", ActionRequest{TargetID: "tab-1"})
	if err != nil {
		t.Fatalf("first snapshot error = %v", err)
	}
	second, err := engine.ExecuteAction(ctx, "snapshot", ActionRequest{TargetID: "tab-1"})
	if err != nil {
		t.Fatalf("second snapshot error = %v", err)
	}

	firstPayload, ok := first.(map[string]any)
	if !ok {
		t.Fatalf("first snapshot payload type = %T", first)
	}
	secondPayload, ok := second.(map[string]any)
	if !ok {
		t.Fatalf("second snapshot payload type = %T", second)
	}

	firstRef := findRefBySelector(firstPayload["refs"], "#submit")
	secondRef := findRefBySelector(secondPayload["refs"], "#submit")
	if firstRef == "" || secondRef == "" {
		t.Fatalf("missing stable selector ref: first=%q second=%q", firstRef, secondRef)
	}
	if firstRef != secondRef {
		t.Fatalf("expected stable ref for #submit, got first=%q second=%q", firstRef, secondRef)
	}
}

func TestExtensionEngineSnapshotPayloadTooLargeDeterministicError(t *testing.T) {
	var mgr *Manager
	mgr = setupAttachedManagerWithConfig(t, Config{
		Enabled:                 true,
		IdleTimeoutSec:          60,
		SnapshotMaxPayloadBytes: 32,
		SnapshotInteractiveOnly: true,
	}, func(v any) {
		req, ok := v.(requestEnvelope)
		if !ok {
			return
		}
		go func() {
			if req.Method == "Runtime.evaluate" {
				_ = mgr.HandleExtensionMessage("ext-1", []byte(fmt.Sprintf(`{"type":"response","requestId":"%s","result":{"result":{"value":{"ok":true,"page":{"url":"https://example.com","title":"Example"},"elements":[{"selector":"#submit","role":"button","name":"Submit","text":"Submit","tag":"button"}]}}}}`, req.RequestID)))
				return
			}
			_ = mgr.HandleExtensionMessage("ext-1", []byte(fmt.Sprintf(`{"type":"response","requestId":"%s","result":{"ok":true}}`, req.RequestID)))
		}()
	})

	engine := NewExtensionEngine(mgr)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := engine.ExecuteAction(ctx, "snapshot", ActionRequest{TargetID: "tab-1"})
	if !errors.Is(err, ErrSnapshotPayloadTooLarge) {
		t.Fatalf("snapshot error = %v, want ErrSnapshotPayloadTooLarge", err)
	}
}

func boolPtr(v bool) *bool {
	return &v
}

func findRefBySelector(raw any, selector string) string {
	list, ok := raw.([]map[string]any)
	if !ok {
		return ""
	}
	for _, entry := range list {
		if strings.TrimSpace(anyToString(entry["selector"])) == selector {
			return strings.TrimSpace(anyToString(entry["ref"]))
		}
	}
	return ""
}

func TestExtensionEngineWaitModeInvalid(t *testing.T) {
	var mgr *Manager
	mgr = setupAttachedManager(t, func(v any) {
		req, ok := v.(requestEnvelope)
		if !ok {
			return
		}
		go func() {
			_ = mgr.HandleExtensionMessage("ext-1", []byte(fmt.Sprintf(`{"type":"response","requestId":"%s","result":{"ok":true}}`, req.RequestID)))
		}()
	})

	engine := NewExtensionEngine(mgr)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := engine.ExecuteAction(ctx, "wait", ActionRequest{
		TargetID: "tab-1",
		WaitMode: "unknown",
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported wait_mode") {
		t.Fatalf("wait unknown mode error = %v, want unsupported wait_mode", err)
	}
}

func TestExtensionEngineWaitNavigationFallbackReadyState(t *testing.T) {
	var mgr *Manager
	mgr = setupAttachedManager(t, func(v any) {
		req, ok := v.(requestEnvelope)
		if !ok {
			return
		}
		go func() {
			switch req.Method {
			case "Page.enable":
				_ = mgr.HandleExtensionMessage("ext-1", []byte(fmt.Sprintf(`{"type":"response","requestId":"%s","result":{"ok":true}}`, req.RequestID)))
			case "Runtime.evaluate":
				_ = mgr.HandleExtensionMessage("ext-1", []byte(fmt.Sprintf(`{"type":"response","requestId":"%s","result":{"result":{"value":true}}}`, req.RequestID)))
			default:
				_ = mgr.HandleExtensionMessage("ext-1", []byte(fmt.Sprintf(`{"type":"response","requestId":"%s","result":{"ok":true}}`, req.RequestID)))
			}
		}()
	})

	engine := NewExtensionEngine(mgr)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	result, err := engine.ExecuteAction(ctx, "wait", ActionRequest{
		TargetID:   "tab-1",
		WaitMode:   "navigation",
		TimeoutMS:  1000,
		IntervalMS: 50,
	})
	if err != nil {
		t.Fatalf("wait navigation error = %v", err)
	}
	payload, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("wait payload type = %T, want map[string]any", result)
	}
	if payload["ok"] != true {
		t.Fatalf("wait payload ok = %v, want true", payload["ok"])
	}
}
