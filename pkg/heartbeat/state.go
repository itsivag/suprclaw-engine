package heartbeat

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/itsivag/suprclaw/pkg/fileutil"
)

const stateFileName = "heartbeat-state.json"

// HeartbeatState is the persisted heartbeat state that survives restarts.
type HeartbeatState struct {
	LastRunAtMs  int64  `json:"last_run_at_ms,omitempty"`
	LastFileHash string `json:"last_file_hash,omitempty"` // SHA-256 hex of HEARTBEAT.md
	ConsecutiveOk int   `json:"consecutive_ok"`
}

// LoadState loads heartbeat state from <workspace>/heartbeat-state.json.
// Returns a zero-value state if the file does not exist.
func LoadState(workspace string) (*HeartbeatState, error) {
	path := filepath.Join(workspace, stateFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &HeartbeatState{}, nil
		}
		return nil, err
	}
	var s HeartbeatState
	if err := json.Unmarshal(data, &s); err != nil {
		return &HeartbeatState{}, nil
	}
	return &s, nil
}

// SaveState atomically writes heartbeat state to <workspace>/heartbeat-state.json.
func SaveState(workspace string, s *HeartbeatState) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(workspace, stateFileName)
	return fileutil.WriteFileAtomic(path, data, 0o600)
}
