package heartbeat

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadStates_FileNotFoundReturnsEmptyMap(t *testing.T) {
	workspace := t.TempDir()
	states, err := LoadStates(workspace)
	if err != nil {
		t.Fatalf("LoadStates() error = %v", err)
	}
	if len(states) != 0 {
		t.Fatalf("states len = %d, want 0", len(states))
	}
}

func TestSaveStates_LoadStates_RoundTripByAgent(t *testing.T) {
	workspace := t.TempDir()
	input := map[string]*HeartbeatState{
		"main": {
			LastRunAtMs:   1710000000000,
			LastFileHash:  "hash-main",
			ConsecutiveOk: 2,
		},
		"ops": {
			LastRunAtMs:   1710001234000,
			LastFileHash:  "hash-ops",
			ConsecutiveOk: 5,
		},
	}

	if err := SaveStates(workspace, input); err != nil {
		t.Fatalf("SaveStates() error = %v", err)
	}

	got, err := LoadStates(workspace)
	if err != nil {
		t.Fatalf("LoadStates() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("states len = %d, want 2", len(got))
	}
	if got["main"] == nil || got["main"].LastFileHash != "hash-main" || got["main"].ConsecutiveOk != 2 {
		t.Fatalf("main state mismatch: %+v", got["main"])
	}
	if got["ops"] == nil || got["ops"].LastFileHash != "hash-ops" || got["ops"].ConsecutiveOk != 5 {
		t.Fatalf("ops state mismatch: %+v", got["ops"])
	}
}

func TestLoadStates_InvalidJSONReturnsError(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, stateFileName)
	if err := os.WriteFile(path, []byte("{invalid"), 0o600); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}

	_, err := LoadStates(workspace)
	if err == nil {
		t.Fatal("expected LoadStates() to fail for invalid JSON")
	}
}
