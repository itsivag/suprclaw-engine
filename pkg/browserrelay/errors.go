package browserrelay

import "errors"

var (
	ErrUnsupportedAction               = errors.New("unsupported browser relay action")
	ErrInvalidTargetID                 = errors.New("invalid target id")
	ErrAgentBrowserUnavailable         = errors.New("agent-browser runtime unavailable")
	ErrSessionNotFound                 = errors.New("agent-browser session not found")
	ErrAgentBrowserRuntimeDisconnected = errors.New("agent_browser_runtime_disconnected")
	ErrAgentBrowserQueueCanceled       = errors.New("agent_browser_queue_canceled")
	ErrAgentBrowserBatchFailed         = errors.New("agent_browser_batch_failed")
	ErrRelayLoopGuardTriggered         = errors.New("relay_loop_guard_triggered")
	ErrSnapshotRefRequired             = errors.New("snapshot_ref_required")
	ErrSnapshotRefNotFound             = errors.New("snapshot_ref_not_found")
	ErrSnapshotPayloadTooLarge         = errors.New("snapshot_payload_too_large")
	ErrSnapshotScopeNotFound           = errors.New("snapshot_scope_not_found")
	ErrSnapshotModeUnsupported         = errors.New("snapshot_mode_unsupported")
	ErrSnapshotProgressBlocked         = errors.New("snapshot_progress_blocked")
	ErrActionabilityTimeout            = errors.New("actionability_timeout")
	ErrActionabilityNotEvents          = errors.New("actionability_not_receiving_events")
	ErrActionabilityNotEnabled         = errors.New("actionability_not_enabled")
	ErrRelayQueueCanceled              = errors.New("relay_queue_canceled")
	ErrRelayQueueFull                  = errors.New("relay_queue_full")
)
