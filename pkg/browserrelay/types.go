package browserrelay

import "time"

// Config controls browser relay behavior.
type Config struct {
	Enabled         bool
	Host            string
	Port            int
	Token           string
	CompatOpenClaw  bool
	MaxClients      int
	IdleTimeoutSec  int
	AllowTokenQuery bool
}

// Target describes a browser tab/target known by the relay.
type Target struct {
	ID        string    `json:"id"`
	Type      string    `json:"type,omitempty"`
	Title     string    `json:"title,omitempty"`
	URL       string    `json:"url,omitempty"`
	Attached  bool      `json:"attached"`
	OwnerID   string    `json:"owner_id,omitempty"`
	LastSeen  time.Time `json:"last_seen"`
	SessionID string    `json:"session_id,omitempty"`
}

// Status summarizes current runtime state of the relay manager.
type Status struct {
	Enabled             bool `json:"enabled"`
	ConnectedExtensions int  `json:"connected_extensions"`
	ConnectedClients    int  `json:"connected_clients"`
	AttachedTargets     int  `json:"attached_targets"`
	MaxClients          int  `json:"max_clients"`
	IdleTimeoutSec      int  `json:"idle_timeout_sec"`
}

// JSONConn is the minimal transport contract used by the relay manager.
// It intentionally avoids direct websocket dependencies for easier testing
// and future remote transport backends.
type JSONConn interface {
	WriteJSON(v any) error
	Close() error
}
