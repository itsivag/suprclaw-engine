package browserrelay

import "errors"

var (
	ErrUnsupportedAction       = errors.New("unsupported browser relay action")
	ErrInvalidTargetID         = errors.New("invalid target id")
	ErrAgentBrowserUnavailable = errors.New("agent-browser runtime unavailable")
	ErrSessionNotFound         = errors.New("agent-browser session not found")
	ErrRelayLoopGuardTriggered = errors.New("relay_loop_guard_triggered")
	ErrSnapshotRefNotFound     = errors.New("snapshot_ref_not_found")
	ErrRelayQueueCanceled      = errors.New("relay_queue_canceled")
	ErrRelayQueueFull          = errors.New("relay_queue_full")
)
