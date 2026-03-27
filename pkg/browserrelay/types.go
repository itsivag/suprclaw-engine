package browserrelay

import "time"

// Config controls browser relay behavior.
type Config struct {
	Enabled                     bool
	Host                        string
	Port                        int
	Token                       string
	CompatOpenClaw              bool
	MaxClients                  int
	IdleTimeoutSec              int
	AllowTokenQuery             bool
	EngineMode                  string
	AgentBrowserEnabled         bool
	AgentBrowserBinary          string
	AgentBrowserDefaultHeadless bool
	AgentBrowserMaxSessions     int
	AgentBrowserIdleTimeoutSec  int
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
	Source    string    `json:"source,omitempty"`
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

// Session describes a dedicated automation browser session.
type Session struct {
	ID        string    `json:"id"`
	TargetID  string    `json:"target_id"`
	Source    string    `json:"source"`
	CreatedAt time.Time `json:"created_at"`
	LastSeen  time.Time `json:"last_seen"`
}

// ActionRequest captures browser-relay action payload fields.
type ActionRequest struct {
	TargetID   string
	SessionID  string
	URL        string
	Selector   string
	Text       string
	Key        string
	Expression string
	TimeoutMS  int
	IntervalMS int
}

const (
	TargetSourceExtension    = "extension"
	TargetSourceAgentBrowser = "agent_browser"

	EngineModeHybrid       = "hybrid"
	EngineModeExtension    = "extension_only"
	EngineModeAgentBrowser = "agent_browser_only"
)
