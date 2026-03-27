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
	SnapshotDefaultMode         string
	SnapshotMaxPayloadBytes     int
	SnapshotMaxNodes            int
	SnapshotMaxTextChars        int
	SnapshotMaxDepth            int
	SnapshotInteractiveOnly     bool
	SnapshotRefTTLSec           int
	SnapshotMaxGenerations      int
	SnapshotAllowFullTree       bool
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
	TargetID        string
	SessionID       string
	URL             string
	Selector        string
	Text            string
	Key             string
	Expression      string
	WaitMode        string
	RefGeneration   string
	SnapshotMode    string
	InteractiveOnly *bool
	ScopeSelector   string
	Depth           int
	MaxNodes        int
	MaxTextChars    int
	TimeoutMS       int
	IntervalMS      int
	StopOnError     bool
	StopOnErrorSet  bool
	Steps           []BatchStep
}

// BatchStep represents one operation inside a browser-relay batch action.
type BatchStep struct {
	Action          string `json:"action"`
	TargetID        string `json:"target_id,omitempty"`
	URL             string `json:"url,omitempty"`
	Selector        string `json:"selector,omitempty"`
	Text            string `json:"text,omitempty"`
	Key             string `json:"key,omitempty"`
	Expression      string `json:"expression,omitempty"`
	WaitMode        string `json:"wait_mode,omitempty"`
	RefGeneration   string `json:"ref_generation,omitempty"`
	SnapshotMode    string `json:"mode,omitempty"`
	InteractiveOnly *bool  `json:"interactive_only,omitempty"`
	ScopeSelector   string `json:"scope_selector,omitempty"`
	Depth           int    `json:"depth,omitempty"`
	MaxNodes        int    `json:"max_nodes,omitempty"`
	MaxTextChars    int    `json:"max_text_chars,omitempty"`
	TimeoutMS       int    `json:"timeout_ms,omitempty"`
	IntervalMS      int    `json:"interval_ms,omitempty"`
}

const (
	TargetSourceExtension    = "extension"
	TargetSourceAgentBrowser = "agent_browser"

	EngineModeHybrid       = "hybrid"
	EngineModeExtension    = "extension_only"
	EngineModeAgentBrowser = "agent_browser_only"
)
