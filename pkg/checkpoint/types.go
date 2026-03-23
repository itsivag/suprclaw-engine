package checkpoint

import "time"

// SideEffect type constants classify the external impact of a tool call.
const (
	SideEffectNone     = "none"     // read-only, no side effects
	SideEffectLocal    = "local"    // workspace files only
	SideEffectExternal = "external" // MCP/exec/channel calls — cannot be undone
)

// Config holds checkpoint configuration.
type Config struct {
	Enabled            bool               `json:"enabled"`
	EveryNToolCalls    int                `json:"every_n_tool_calls"`    // 0 = disabled
	CheckpointBefore   []string           `json:"checkpoint_before"`     // tool names that trigger pre-checkpoint
	StoreSnapData      bool               `json:"store_snap_data"`       // whether to copy workspace files + session
	MaxSnapFileSize    int64              `json:"max_snap_file_size"`    // bytes, default 5 MB
	MaxCommitsPerAgent int                `json:"max_commits_per_agent"` // default 100
	Compensations      []CompensationRule `json:"compensations,omitempty"` // inverse-call rules for external tools
}

// MaxSnapFileSizeBytes returns the effective file size limit.
func (c *Config) MaxSnapFileSizeBytes() int64 {
	if c.MaxSnapFileSize <= 0 {
		return 5 * 1024 * 1024
	}
	return c.MaxSnapFileSize
}

// HasTool reports whether the given tool name is in checkpoint_before.
func (c *Config) HasTool(name string) bool {
	for _, n := range c.CheckpointBefore {
		if n == name {
			return true
		}
	}
	return false
}

// ActionEntry is one JSONL line in the per-agent audit log.
type ActionEntry struct {
	Seq           int64             `json:"seq"`
	Ts            time.Time         `json:"ts"`
	AgentID       string            `json:"agent_id"`
	SessionKey    string            `json:"session_key"`
	ToolName      string            `json:"tool_name"`
	ArgsDigest    string            `json:"args_digest"`
	ArgsFull      map[string]any    `json:"args_full,omitempty"`    // only populated for external side-effects
	ResultPreview string            `json:"result_preview"`
	ResultFull    string            `json:"result_full,omitempty"`  // full result for external tools
	Compensation  *CompensationPlan `json:"compensation,omitempty"` // resolved inverse call, if a rule matched
	IsError       bool              `json:"is_error"`
	SideEffect    string            `json:"side_effect"`
	CommitID      string            `json:"commit_id,omitempty"`
	Revoked       bool              `json:"revoked"`
}

// FileSnapshot records one file's metadata at checkpoint time.
type FileSnapshot struct {
	RelPath     string `json:"rel_path"`
	SHA256      string `json:"sha256"`
	Size        int64  `json:"size"`
	ModTimeUnix int64  `json:"mod_time_unix"`
}

// CommitManifest is the checkpoint record stored as JSON.
type CommitManifest struct {
	ID               string         `json:"id"`
	AgentID          string         `json:"agent_id"`
	SessionKey       string         `json:"session_key"`
	CreatedAt        time.Time      `json:"created_at"`
	Label            string         `json:"label,omitempty"`
	Trigger          string         `json:"trigger,omitempty"`
	ParentID         string         `json:"parent_id,omitempty"`
	SessionLineCount int            `json:"session_line_count"` // informational: messages at commit time
	WorkspaceFiles   []FileSnapshot `json:"workspace_files"`
	HasSnapData      bool           `json:"has_snap_data"`
	Revoked          bool           `json:"revoked"`
	RevokedAt        *time.Time     `json:"revoked_at,omitempty"`
}
