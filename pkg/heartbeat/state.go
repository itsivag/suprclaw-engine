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
	LastRunAtMs   int64  `json:"last_run_at_ms,omitempty"`
	LastFileHash  string `json:"last_file_hash,omitempty"` // SHA-256 hex of HEARTBEAT.md
	ConsecutiveOk int    `json:"consecutive_ok"`
}

type heartbeatStateFile struct {
	Jobs map[string]*HeartbeatState `json:"jobs"`
}

// LoadStates loads heartbeat state for all configured jobs from
// <workspace>/heartbeat-state.json. Returns an empty map when the file does not exist.
func LoadStates(workspace string) (map[string]*HeartbeatState, error) {
	path := filepath.Join(workspace, stateFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]*HeartbeatState{}, nil
		}
		return nil, err
	}

	var payload heartbeatStateFile
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, err
	}
	if payload.Jobs == nil {
		return map[string]*HeartbeatState{}, nil
	}
	return payload.Jobs, nil
}

// SaveStates atomically writes heartbeat states to <workspace>/heartbeat-state.json.
func SaveStates(workspace string, states map[string]*HeartbeatState) error {
	payload := heartbeatStateFile{
		Jobs: states,
	}

	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(workspace, stateFileName)
	return fileutil.WriteFileAtomic(path, data, 0o600)
}
