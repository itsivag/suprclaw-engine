package heartbeat

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/itsivag/suprclaw/pkg/routing"
)

type pruneCaptureExecutor struct {
	agentID           string
	sessionKey        string
	processedAgentID  string
	processedSession  string
	processedPrompt   string
	processReturnText string
}

func (p *pruneCaptureExecutor) ProcessHeartbeat(
	ctx context.Context,
	agentID string,
	sessionKey string,
	prompt string,
	deliverChannel, deliverChatID string,
	maxTokens int,
) (string, error) {
	p.processedAgentID = agentID
	p.processedSession = sessionKey
	p.processedPrompt = prompt
	if p.processReturnText == "" {
		return "HEARTBEAT_OK", nil
	}
	return p.processReturnText, nil
}

func (p *pruneCaptureExecutor) PruneLastTurn(agentID, sessionKey string) error {
	p.agentID = agentID
	p.sessionKey = sessionKey
	return nil
}

func TestPruneLastTurn_UsesProvidedHeartbeatRunSessionKey(t *testing.T) {
	exec := &pruneCaptureExecutor{}
	deps := RunnerDeps{AgentLoop: exec}
	sessionKey := routing.BuildAgentHeartbeatRunSessionKey("writer", "run-1")

	if err := pruneLastTurn(deps, "writer", sessionKey); err != nil {
		t.Fatalf("pruneLastTurn() error = %v", err)
	}
	if exec.agentID != "writer" {
		t.Fatalf("agentID = %q, want writer", exec.agentID)
	}
	if exec.sessionKey != sessionKey {
		t.Fatalf("sessionKey = %q, want %q", exec.sessionKey, sessionKey)
	}
}

func TestRunOnce_UsesFreshHeartbeatRunSessionKeyAndPrunesSameSession(t *testing.T) {
	tmp := t.TempDir()
	heartbeatFile := filepath.Join(tmp, "HEARTBEAT.md")
	if err := os.WriteFile(heartbeatFile, []byte("perform heartbeat"), 0o600); err != nil {
		t.Fatalf("WriteFile(HEARTBEAT.md) error = %v", err)
	}

	exec := &pruneCaptureExecutor{processReturnText: "HEARTBEAT_OK"}
	result := RunOnce(context.Background(), RunnerDeps{
		Cfg: HeartbeatRunConfig{
			AgentID:         "writer",
			Workspace:       tmp,
			IntervalMinutes: 30,
		},
		State:     HeartbeatState{},
		AgentLoop: exec,
	})

	base := routing.BuildAgentHeartbeatSessionKey("writer") + ":"
	if !strings.HasPrefix(exec.processedSession, base) {
		t.Fatalf("processed session %q does not start with %q", exec.processedSession, base)
	}
	if !routing.IsHeartbeatSessionKey(exec.processedSession) {
		t.Fatalf("processed session %q not recognized as heartbeat session", exec.processedSession)
	}
	if exec.sessionKey != exec.processedSession {
		t.Fatalf("pruned session = %q, processed session = %q; want equal", exec.sessionKey, exec.processedSession)
	}
	if result.NextState.LastRunAtMs <= 0 {
		t.Fatalf("NextState.LastRunAtMs = %d, want > 0", result.NextState.LastRunAtMs)
	}
	if result.NextState.LastFileHash == "" {
		t.Fatal("NextState.LastFileHash is empty")
	}
}
