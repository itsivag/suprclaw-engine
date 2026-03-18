package heartbeat

import (
	"sync"
)

// HeartbeatStatus describes the outcome of a heartbeat cycle.
type HeartbeatStatus string

const (
	StatusSent    HeartbeatStatus = "sent"
	StatusOkToken HeartbeatStatus = "ok-token"
	StatusOkEmpty HeartbeatStatus = "ok-empty"
	StatusSkipped HeartbeatStatus = "skipped"
	StatusFailed  HeartbeatStatus = "failed"
)

// HeartbeatEvent is emitted after every heartbeat cycle.
type HeartbeatEvent struct {
	Ts                   int64           // Unix ms
	Status               HeartbeatStatus
	AgentID              string
	DurationMs           int64
	Preview              string // first 80 chars of response
	SkipReason           string // "idle_window" | "active_hours" | "unchanged_file" | ""
	ConsecutiveOk        int
	EffectiveIntervalMin int
}

var (
	heartbeatMu        sync.RWMutex
	heartbeatHandles   []*listenerHandle
	heartbeatListeners []func(HeartbeatEvent)
	lastHeartbeatEvent *HeartbeatEvent
)

// EmitHeartbeatEvent broadcasts the event to all registered listeners and caches it.
func EmitHeartbeatEvent(evt HeartbeatEvent) {
	heartbeatMu.Lock()
	last := evt
	lastHeartbeatEvent = &last
	listeners := make([]func(HeartbeatEvent), len(heartbeatListeners))
	copy(listeners, heartbeatListeners)
	heartbeatMu.Unlock()

	for _, fn := range listeners {
		fn(evt)
	}
}

// listenerHandle is a unique token used for O(n) unsubscription.
type listenerHandle struct {
	fn func(HeartbeatEvent)
}

// OnHeartbeatEvent registers a listener for heartbeat events.
// Returns an unsubscribe function.
func OnHeartbeatEvent(fn func(HeartbeatEvent)) func() {
	handle := &listenerHandle{fn: fn}

	heartbeatMu.Lock()
	heartbeatHandles = append(heartbeatHandles, handle)
	heartbeatMu.Unlock()

	return func() {
		heartbeatMu.Lock()
		defer heartbeatMu.Unlock()
		newHandles := heartbeatHandles[:0]
		for _, h := range heartbeatHandles {
			if h != handle {
				newHandles = append(newHandles, h)
			}
		}
		heartbeatHandles = newHandles

		// Rebuild the listeners slice.
		heartbeatListeners = make([]func(HeartbeatEvent), 0, len(heartbeatHandles))
		for _, h := range heartbeatHandles {
			heartbeatListeners = append(heartbeatListeners, h.fn)
		}
	}
}

// GetLastHeartbeatEvent returns the most recent heartbeat event, or nil if none has fired.
func GetLastHeartbeatEvent() *HeartbeatEvent {
	heartbeatMu.RLock()
	defer heartbeatMu.RUnlock()
	if lastHeartbeatEvent == nil {
		return nil
	}
	copy := *lastHeartbeatEvent
	return &copy
}
