package api

import (
	"errors"
	"testing"

	"github.com/itsivag/suprclaw/pkg/browserrelay"
	"github.com/itsivag/suprclaw/pkg/config"
)

func TestNormalizeBrowserRelayConfigDefaultsEngineMode(t *testing.T) {
	got := normalizeBrowserRelayConfig(config.BrowserRelayConfig{})
	if got.EngineMode != "hybrid" {
		t.Fatalf("EngineMode = %q, want %q", got.EngineMode, "hybrid")
	}
	if got.AgentBrowserBinary == "" {
		t.Fatal("AgentBrowserBinary is empty, want non-empty default")
	}
	if got.AgentBrowserMaxSessions <= 0 {
		t.Fatalf("AgentBrowserMaxSessions = %d, want > 0", got.AgentBrowserMaxSessions)
	}
	if got.AgentBrowserIdleTimeoutSec <= 0 {
		t.Fatalf("AgentBrowserIdleTimeoutSec = %d, want > 0", got.AgentBrowserIdleTimeoutSec)
	}
	if got.AgentBrowserBatchWindowMS <= 0 {
		t.Fatalf("AgentBrowserBatchWindowMS = %d, want > 0", got.AgentBrowserBatchWindowMS)
	}
	if got.AgentBrowserBatchMaxSteps <= 0 {
		t.Fatalf("AgentBrowserBatchMaxSteps = %d, want > 0", got.AgentBrowserBatchMaxSteps)
	}
	if got.AgentBrowserRuntimeCommandTimeoutMS <= 0 {
		t.Fatalf("AgentBrowserRuntimeCommandTimeoutMS = %d, want > 0", got.AgentBrowserRuntimeCommandTimeoutMS)
	}
	if !got.AgentBrowserStreamEnabled {
		t.Fatal("AgentBrowserStreamEnabled default should be true")
	}
	if got.SnapshotDefaultMode != "compact" {
		t.Fatalf("SnapshotDefaultMode = %q, want compact", got.SnapshotDefaultMode)
	}
	if got.SnapshotMaxPayloadBytes <= 0 {
		t.Fatalf("SnapshotMaxPayloadBytes = %d, want > 0", got.SnapshotMaxPayloadBytes)
	}
	if got.SnapshotMaxNodes <= 0 {
		t.Fatalf("SnapshotMaxNodes = %d, want > 0", got.SnapshotMaxNodes)
	}
	if got.SnapshotMaxTextChars <= 0 {
		t.Fatalf("SnapshotMaxTextChars = %d, want > 0", got.SnapshotMaxTextChars)
	}
	if got.SnapshotMaxDepth <= 0 {
		t.Fatalf("SnapshotMaxDepth = %d, want > 0", got.SnapshotMaxDepth)
	}
	if !got.SnapshotInteractiveOnly {
		t.Fatal("SnapshotInteractiveOnly default should be true")
	}
	if got.SnapshotRefTTLSec <= 0 {
		t.Fatalf("SnapshotRefTTLSec = %d, want > 0", got.SnapshotRefTTLSec)
	}
	if got.SnapshotMaxGenerations <= 0 {
		t.Fatalf("SnapshotMaxGenerations = %d, want > 0", got.SnapshotMaxGenerations)
	}
	if !got.SnapshotAllowFullTree {
		t.Fatal("SnapshotAllowFullTree default should be true")
	}

	got2 := normalizeBrowserRelayConfig(config.BrowserRelayConfig{
		EngineMode:          "hybrid",
		AgentBrowserEnabled: true,
	})
	if !got2.AgentBrowserEnabled {
		t.Fatal("AgentBrowserEnabled should remain true when explicitly configured")
	}
}

func TestValidateAndConvertRelayActionV2SnapshotArgs(t *testing.T) {
	h := &Handler{}
	action, payload, err := h.validateAndConvertRelayActionV2(relayActionV2Request{
		Target: "ext:tab-1",
		Action: "snapshot",
		Args: map[string]any{
			"mode":             "full",
			"interactive_only": false,
			"scope_selector":   "#content",
			"depth":            float64(4),
			"max_nodes":        float64(90),
			"max_text_chars":   float64(80),
		},
	})
	if err != nil {
		t.Fatalf("validateAndConvertRelayActionV2 error = %v", err)
	}
	if action != "snapshot" {
		t.Fatalf("action = %q, want snapshot", action)
	}
	if payload.SnapshotMode != "full" {
		t.Fatalf("SnapshotMode = %q, want full", payload.SnapshotMode)
	}
	if payload.InteractiveOnly == nil || *payload.InteractiveOnly != false {
		t.Fatalf("InteractiveOnly = %v, want pointer(false)", payload.InteractiveOnly)
	}
	if payload.ScopeSelector != "#content" {
		t.Fatalf("ScopeSelector = %q, want #content", payload.ScopeSelector)
	}
	if payload.Depth != 4 || payload.MaxNodes != 90 || payload.MaxTextChars != 80 {
		t.Fatalf(
			"snapshot args parsed incorrectly: depth=%d nodes=%d text=%d",
			payload.Depth,
			payload.MaxNodes,
			payload.MaxTextChars,
		)
	}
}

func TestClassifyRelayErrorSnapshotCodes(t *testing.T) {
	payloadTooLarge := classifyRelayError(errors.Join(browserrelay.ErrSnapshotPayloadTooLarge, errors.New("boom")))
	if payloadTooLarge.Code != "snapshot_payload_too_large" || payloadTooLarge.RetryClass != retryClassNever {
		t.Fatalf("payloadTooLarge = %+v", payloadTooLarge)
	}

	scopeMissing := classifyRelayError(browserrelay.ErrSnapshotScopeNotFound)
	if scopeMissing.Code != "snapshot_scope_not_found" || scopeMissing.RetryClass != retryClassNever {
		t.Fatalf("scopeMissing = %+v", scopeMissing)
	}

	modeUnsupported := classifyRelayError(browserrelay.ErrSnapshotModeUnsupported)
	if modeUnsupported.Code != "snapshot_mode_unsupported" || modeUnsupported.RetryClass != retryClassNever {
		t.Fatalf("modeUnsupported = %+v", modeUnsupported)
	}

	refRequired := classifyRelayError(browserrelay.ErrSnapshotRefRequired)
	if refRequired.Code != "snapshot_ref_required" || refRequired.RetryClass != retryClassNever {
		t.Fatalf("refRequired = %+v", refRequired)
	}

	progressBlocked := classifyRelayError(browserrelay.ErrSnapshotProgressBlocked)
	if progressBlocked.Code != "snapshot_progress_blocked" || progressBlocked.RetryClass != retryClassNever {
		t.Fatalf("progressBlocked = %+v", progressBlocked)
	}

	notEnabled := classifyRelayError(browserrelay.ErrActionabilityNotEnabled)
	if notEnabled.Code != "actionability_not_enabled" || notEnabled.RetryClass != retryClassAfterStateChange {
		t.Fatalf("notEnabled = %+v", notEnabled)
	}

	notReceiving := classifyRelayError(browserrelay.ErrActionabilityNotEvents)
	if notReceiving.Code != "actionability_not_receiving_events" || notReceiving.RetryClass != retryClassAfterStateChange {
		t.Fatalf("notReceiving = %+v", notReceiving)
	}

	timeout := classifyRelayError(browserrelay.ErrActionabilityTimeout)
	if timeout.Code != "actionability_timeout" || timeout.RetryClass != retryClassAfterStateChange {
		t.Fatalf("timeout = %+v", timeout)
	}

	runtimeDisconnected := classifyRelayError(browserrelay.ErrAgentBrowserRuntimeDisconnected)
	if runtimeDisconnected.Code != "agent_browser_runtime_disconnected" ||
		runtimeDisconnected.RetryClass != retryClassAfterStateChange {
		t.Fatalf("runtimeDisconnected = %+v", runtimeDisconnected)
	}

	queueCanceled := classifyRelayError(browserrelay.ErrAgentBrowserQueueCanceled)
	if queueCanceled.Code != "agent_browser_queue_canceled" ||
		queueCanceled.RetryClass != retryClassAfterStateChange {
		t.Fatalf("queueCanceled = %+v", queueCanceled)
	}

	batchFailed := classifyRelayError(browserrelay.ErrAgentBrowserBatchFailed)
	if batchFailed.Code != "agent_browser_batch_failed" ||
		batchFailed.RetryClass != retryClassAfterStateChange {
		t.Fatalf("batchFailed = %+v", batchFailed)
	}
}
