package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/itsivag/suprclaw/pkg/channels"
)

const (
	defaultRunStoppedMessage = "Stopped by user."
	runCancelledErrorCode    = "RUN_CANCELLED"
)

type activeRunState struct {
	runID         string
	cancel        context.CancelFunc
	stopRequested bool
	stopReason    string
}

type runCancelledError struct {
	RunID  string
	Reason string
}

func (e *runCancelledError) Error() string {
	return "run cancelled"
}

func runControlKey(channel, chatID string) string {
	channel = strings.TrimSpace(channel)
	chatID = strings.TrimSpace(chatID)
	if channel == "" || chatID == "" {
		return ""
	}
	return channel + ":" + chatID
}

func normalizeStopReason(reason string) string {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return defaultRunStoppedMessage
	}
	return reason
}

// CancelRun attempts to cancel the active run for channel+chat.
// Implements channels.RunController.
func (al *AgentLoop) CancelRun(channel, chatID, runID, reason string) (bool, string, error) {
	key := runControlKey(channel, chatID)
	if key == "" {
		return false, "", &channels.RunControlError{
			Code:    "no_active_run",
			Message: "no active run to stop",
		}
	}

	runID = strings.TrimSpace(runID)
	reason = normalizeStopReason(reason)

	al.activeRunsMu.Lock()
	defer al.activeRunsMu.Unlock()

	state, ok := al.activeRuns[key]
	if !ok || state == nil || strings.TrimSpace(state.runID) == "" {
		return false, "", &channels.RunControlError{
			Code:    "no_active_run",
			Message: "no active run to stop",
		}
	}

	activeRunID := state.runID
	if runID != "" && runID != activeRunID {
		return false, activeRunID, &channels.RunControlError{
			Code:    "run_mismatch",
			Message: fmt.Sprintf("requested run_id %q does not match active run %q", runID, activeRunID),
		}
	}

	state.stopRequested = true
	state.stopReason = reason
	cancel := state.cancel

	if cancel != nil {
		cancel()
	}

	return true, activeRunID, nil
}

func (al *AgentLoop) registerActiveRun(channel, chatID, runID string, cancel context.CancelFunc) {
	key := runControlKey(channel, chatID)
	if key == "" || strings.TrimSpace(runID) == "" {
		return
	}

	al.activeRunsMu.Lock()
	al.activeRuns[key] = &activeRunState{
		runID:         runID,
		cancel:        cancel,
		stopRequested: false,
		stopReason:    "",
	}
	al.activeRunsMu.Unlock()
}

func (al *AgentLoop) unregisterActiveRun(channel, chatID, runID string) {
	key := runControlKey(channel, chatID)
	if key == "" || strings.TrimSpace(runID) == "" {
		return
	}

	al.activeRunsMu.Lock()
	state, ok := al.activeRuns[key]
	if ok && state != nil && state.runID == runID {
		delete(al.activeRuns, key)
	}
	al.activeRunsMu.Unlock()
}

func (al *AgentLoop) runCancelState(channel, chatID, runID string) (bool, string) {
	key := runControlKey(channel, chatID)
	if key == "" || strings.TrimSpace(runID) == "" {
		return false, ""
	}

	al.activeRunsMu.RLock()
	defer al.activeRunsMu.RUnlock()

	state, ok := al.activeRuns[key]
	if !ok || state == nil {
		return false, ""
	}
	if state.runID != runID {
		return false, ""
	}
	if !state.stopRequested {
		return false, ""
	}
	return true, normalizeStopReason(state.stopReason)
}

func (al *AgentLoop) runCancelCheckpoint(ctx context.Context, channel, chatID, runID string) error {
	if stopped, reason := al.runCancelState(channel, chatID, runID); stopped {
		return &runCancelledError{RunID: runID, Reason: reason}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return nil
}
