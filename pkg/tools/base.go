package tools

import (
	"context"
	"fmt"
	"sync/atomic"
)

// Tool is the interface that all tools must implement.
type Tool interface {
	Name() string
	Description() string
	UsageContract() ToolUsageContract
	Parameters() map[string]any
	Execute(ctx context.Context, args map[string]any) *ToolResult
}

// --- Request-scoped tool context (channel / chatID) ---
//
// Carried via context.Value so that concurrent tool calls each receive
// their own immutable copy — no mutable state on singleton tool instances.
//
// Keys are unexported pointer-typed vars — guaranteed collision-free,
// and only accessible through the helper functions below.

type toolCtxKey struct{ name string }

var (
	ctxKeyChannel = &toolCtxKey{"channel"}
	ctxKeyChatID  = &toolCtxKey{"chatID"}
	ctxKeyMessage = &toolCtxKey{"messageRoundState"}
)

// WithToolContext returns a child context carrying channel and chatID.
func WithToolContext(ctx context.Context, channel, chatID string) context.Context {
	ctx = context.WithValue(ctx, ctxKeyChannel, channel)
	ctx = context.WithValue(ctx, ctxKeyChatID, chatID)
	return ctx
}

// ToolChannel extracts the channel from ctx, or "" if unset.
func ToolChannel(ctx context.Context) string {
	v, _ := ctx.Value(ctxKeyChannel).(string)
	return v
}

// ToolChatID extracts the chatID from ctx, or "" if unset.
func ToolChatID(ctx context.Context) string {
	v, _ := ctx.Value(ctxKeyChatID).(string)
	return v
}

// MessageRoundState is request-scoped state used by MessageTool to mark whether
// a direct message send already occurred in the current run.
type MessageRoundState struct {
	sent atomic.Bool
}

func NewMessageRoundState() *MessageRoundState {
	return &MessageRoundState{}
}

func (s *MessageRoundState) MarkSent() {
	if s == nil {
		return
	}
	s.sent.Store(true)
}

func (s *MessageRoundState) WasSent() bool {
	if s == nil {
		return false
	}
	return s.sent.Load()
}

// WithMessageRoundState returns a child context carrying request-scoped
// MessageTool state.
func WithMessageRoundState(ctx context.Context, state *MessageRoundState) context.Context {
	return context.WithValue(ctx, ctxKeyMessage, state)
}

// MessageRoundStateFromContext extracts request-scoped MessageTool state.
func MessageRoundStateFromContext(ctx context.Context) *MessageRoundState {
	state, _ := ctx.Value(ctxKeyMessage).(*MessageRoundState)
	return state
}

// AsyncCallback is a function type that async tools use to notify completion.
// When an async tool finishes its work, it calls this callback with the result.
//
// The ctx parameter allows the callback to be canceled if the agent is shutting down.
// The result parameter contains the tool's execution result.
type AsyncCallback func(ctx context.Context, result *ToolResult)

// AsyncExecutor is an optional interface that tools can implement to support
// asynchronous execution with completion callbacks.
//
// Unlike the old AsyncTool pattern (SetCallback + Execute), AsyncExecutor
// receives the callback as a parameter of ExecuteAsync. This eliminates the
// data race where concurrent calls could overwrite each other's callbacks
// on a shared tool instance.
//
// This is useful for:
//   - Long-running operations that shouldn't block the agent loop
//   - Subagent spawns that complete independently
//   - Background tasks that need to report results later
//
// Example:
//
//	func (t *SpawnTool) ExecuteAsync(ctx context.Context, args map[string]any, cb AsyncCallback) *ToolResult {
//	    go func() {
//	        result := t.runSubagent(ctx, args)
//	        if cb != nil { cb(ctx, result) }
//	    }()
//	    return AsyncResult("Subagent spawned, will report back")
//	}
type AsyncExecutor interface {
	Tool
	// ExecuteAsync runs the tool asynchronously. The callback cb will be
	// invoked (possibly from another goroutine) when the async operation
	// completes. cb is guaranteed to be non-nil by the caller (registry).
	ExecuteAsync(ctx context.Context, args map[string]any, cb AsyncCallback) *ToolResult
}

// SideEffectClassifier is an optional interface that tools can implement to
// declare their side-effect category for the checkpoint audit system.
// If a tool does not implement this interface, the audit system assumes
// SideEffectExternal ("external") as the safest conservative default.
//
// Return values:
//
//	"none"     — read-only, no state changes (e.g. read_file, list_dir)
//	"local"    — modifies workspace files only (e.g. write_file, edit_file)
//	"external" — calls external services/processes (e.g. MCP tools, exec, send_message)
type SideEffectClassifier interface {
	SideEffectType() string
}

func ToolToSchema(tool Tool) map[string]any {
	if err := ValidateToolContract(tool); err != nil {
		panic(fmt.Errorf("tool schema build failed: %w", err))
	}
	return map[string]any{
		"type": "function",
		"function": map[string]any{
			"name":        tool.Name(),
			"description": FormatToolDescription(tool.Description(), tool.UsageContract()),
			"parameters":  tool.Parameters(),
		},
	}
}
