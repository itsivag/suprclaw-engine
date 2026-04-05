package heartbeat

import (
	"context"
	"testing"

	"github.com/itsivag/suprclaw/pkg/routing"
)

type pruneCaptureExecutor struct {
	agentID    string
	sessionKey string
}

func (p *pruneCaptureExecutor) ProcessHeartbeat(
	ctx context.Context,
	agentID string,
	prompt string,
	deliverChannel, deliverChatID string,
	maxTokens int,
) (string, error) {
	return "", nil
}

func (p *pruneCaptureExecutor) PruneLastTurn(agentID, sessionKey string) error {
	p.agentID = agentID
	p.sessionKey = sessionKey
	return nil
}

func TestPruneLastTurn_UsesHeartbeatSessionKey(t *testing.T) {
	exec := &pruneCaptureExecutor{}
	deps := RunnerDeps{AgentLoop: exec}

	if err := pruneLastTurn(deps, "writer"); err != nil {
		t.Fatalf("pruneLastTurn() error = %v", err)
	}
	if exec.agentID != "writer" {
		t.Fatalf("agentID = %q, want writer", exec.agentID)
	}
	want := routing.BuildAgentHeartbeatSessionKey("writer")
	if exec.sessionKey != want {
		t.Fatalf("sessionKey = %q, want %q", exec.sessionKey, want)
	}
}
