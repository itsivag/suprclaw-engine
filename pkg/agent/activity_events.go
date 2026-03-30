package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/itsivag/suprclaw/pkg/bus"
	"github.com/itsivag/suprclaw/pkg/constants"
	"github.com/itsivag/suprclaw/pkg/tools"
	"github.com/itsivag/suprclaw/pkg/utils"
)

const (
	activitySchemaVersion = "1.0"
)

type activityRunEmitter struct {
	al        *AgentLoop
	ctx       context.Context
	chatID    string
	channel   string
	sessionID string
	agentID   string
	runID     string

	sequence       int
	stepCounter    int
	messageCounter int
	startedAt      time.Time
}

func newActivityRunEmitter(
	ctx context.Context,
	al *AgentLoop,
	agentID string,
	opts processOptions,
) *activityRunEmitter {
	if al == nil || ctx == nil {
		return nil
	}
	if constants.IsInternalChannel(opts.Channel) || !opts.SendResponse || opts.Channel != "supr" {
		return nil
	}
	if strings.TrimSpace(opts.ChatID) == "" {
		return nil
	}
	sessionID := strings.TrimPrefix(opts.ChatID, "supr:")
	if sessionID == "" {
		sessionID = opts.ChatID
	}
	return &activityRunEmitter{
		al:        al,
		ctx:       ctx,
		chatID:    opts.ChatID,
		channel:   opts.Channel,
		sessionID: sessionID,
		agentID:   strings.TrimSpace(agentID),
		runID:     "run_" + strings.ReplaceAll(uuid.NewString(), "-", ""),
		startedAt: time.Now().UTC(),
	}
}

func (e *activityRunEmitter) nextStepID() string {
	e.stepCounter++
	return fmt.Sprintf("step_%03d", e.stepCounter)
}

func (e *activityRunEmitter) nextMessageID() string {
	e.messageCounter++
	return fmt.Sprintf("msg_%03d", e.messageCounter)
}

func (e *activityRunEmitter) emit(eventType string, data map[string]any) {
	if e == nil {
		return
	}
	e.sequence++
	if data == nil {
		data = map[string]any{}
	}
	envelope := bus.ActivityEventEnvelope{
		V:              activitySchemaVersion,
		EventID:        "evt_" + strings.ReplaceAll(uuid.NewString(), "-", ""),
		EventType:      eventType,
		Timestamp:      time.Now().UTC().Format(time.RFC3339Nano),
		Sequence:       e.sequence,
		SessionID:      e.sessionID,
		RunID:          e.runID,
		ParentRunID:    nil,
		AgentID:        e.agentID,
		IdempotencyKey: fmt.Sprintf("%s_%d", e.runID, e.sequence),
		Replay:         false,
		Data:           data,
	}
	_ = e.al.bus.PublishOutboundActivity(e.ctx, bus.OutboundActivityEvent{
		Channel: e.channel,
		ChatID:  e.chatID,
		Event:   envelope,
	})
}

func toolCallID(callID string, iteration, index int) string {
	id := strings.TrimSpace(callID)
	if id != "" {
		return id
	}
	return fmt.Sprintf("tc_%d_%d", iteration, index+1)
}

func toolResultPreview(result *tools.ToolResult) string {
	if result == nil {
		return ""
	}
	switch {
	case strings.TrimSpace(result.ForUser) != "":
		return utils.Truncate(strings.TrimSpace(result.ForUser), 140)
	case strings.TrimSpace(result.ForLLM) != "":
		return utils.Truncate(strings.TrimSpace(result.ForLLM), 140)
	case result.Err != nil:
		return utils.Truncate(strings.TrimSpace(result.Err.Error()), 140)
	default:
		return ""
	}
}

func sanitizeToolArgPreview(args map[string]any) string {
	if len(args) == 0 {
		return "{}"
	}
	sanitized := sanitizeMap(args)
	raw, err := json.Marshal(sanitized)
	if err != nil {
		return "{}"
	}
	return utils.Truncate(string(raw), 200)
}

func sanitizeMap(input map[string]any) map[string]any {
	out := make(map[string]any, len(input))
	for key, value := range input {
		out[key] = sanitizeValue(key, value)
	}
	return out
}

func sanitizeSlice(input []any) []any {
	out := make([]any, 0, len(input))
	for _, item := range input {
		out = append(out, sanitizeValue("", item))
	}
	return out
}

func sanitizeValue(key string, value any) any {
	if isSensitiveKey(key) {
		return "[REDACTED]"
	}
	switch v := value.(type) {
	case map[string]any:
		return sanitizeMap(v)
	case []any:
		return sanitizeSlice(v)
	case map[string]string:
		m := make(map[string]any, len(v))
		for k, inner := range v {
			m[k] = inner
		}
		return sanitizeMap(m)
	default:
		return value
	}
}

func isSensitiveKey(key string) bool {
	if key == "" {
		return false
	}
	k := strings.ToLower(strings.TrimSpace(key))
	return strings.Contains(k, "token") ||
		strings.Contains(k, "password") ||
		strings.Contains(k, "secret") ||
		strings.Contains(k, "api_key") ||
		strings.Contains(k, "apikey") ||
		strings.Contains(k, "authorization")
}
