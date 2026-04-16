package supr

import "time"

// Protocol message types.
const (
	// TypeMessageSend is sent from client to server.
	TypeMessageSend = "message.send"
	TypeMediaSend   = "media.send"
	TypeRunStop     = "run.stop"
	TypeRunStatusGet = "run.status.get"
	TypePing        = "ping"

	// Legacy typed control frames still used for handshake/errors/media transport.
	TypeRunStatus  = "run.status"
	TypeMediaCreate = "media.create"
	TypeError       = "error"
	TypePong        = "pong"
	TypeAgentList   = "agent.list"
)

// SuprMessage is the wire format for all Supr WebSocket messages.
type SuprMessage struct {
	Type      string         `json:"type"`
	ID        string         `json:"id,omitempty"`
	SessionID string         `json:"session_id,omitempty"`
	Timestamp int64          `json:"timestamp,omitempty"`
	Payload   map[string]any `json:"payload,omitempty"`
}

// newMessage creates a SuprMessage with the given type and payload.
func newMessage(msgType string, payload map[string]any) SuprMessage {
	return SuprMessage{
		Type:      msgType,
		Timestamp: time.Now().UnixMilli(),
		Payload:   payload,
	}
}

// newError creates an error SuprMessage.
func newError(code, message string) SuprMessage {
	return newMessage(TypeError, map[string]any{
		"code":    code,
		"message": message,
	})
}
