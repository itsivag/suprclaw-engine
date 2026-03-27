package browserrelay

import (
	"context"
	"errors"
	"testing"
)

type fakeExecEngine struct {
	targets     []Target
	sessions    []Session
	executed    []string
	lastRequest ActionRequest
	createResp  any
	createErr   error
	execResp    any
	execErr     error
	closeErr    error
}

func (f *fakeExecEngine) ListTargets(context.Context) ([]Target, error) {
	return append([]Target(nil), f.targets...), nil
}

func (f *fakeExecEngine) ListSessions(context.Context) ([]Session, error) {
	return append([]Session(nil), f.sessions...), nil
}

func (f *fakeExecEngine) CreateSession(_ context.Context, req ActionRequest) (any, error) {
	f.lastRequest = req
	if f.createErr != nil {
		return nil, f.createErr
	}
	if f.createResp != nil {
		return f.createResp, nil
	}
	return map[string]any{"ok": true}, nil
}

func (f *fakeExecEngine) CloseSession(context.Context, string) error {
	return f.closeErr
}

func (f *fakeExecEngine) ExecuteAction(_ context.Context, action string, req ActionRequest) (any, error) {
	f.executed = append(f.executed, action)
	f.lastRequest = req
	if f.execErr != nil {
		return nil, f.execErr
	}
	if f.execResp != nil {
		return f.execResp, nil
	}
	return map[string]any{"ok": true}, nil
}

func TestEngineRouterTabsListMergesTargets(t *testing.T) {
	ext := &fakeExecEngine{
		targets: []Target{{ID: "ext:101", Source: TargetSourceExtension}},
	}
	ab := &fakeExecEngine{
		targets: []Target{{ID: "ab:s-1:main", Source: TargetSourceAgentBrowser}},
	}
	router := NewEngineRouter(Config{EngineMode: EngineModeHybrid, AgentBrowserEnabled: true}, ext, ab)

	result, err := router.ExecuteAction(context.Background(), "tabs.list", ActionRequest{})
	if err != nil {
		t.Fatalf("ExecuteAction(tabs.list) error = %v", err)
	}
	payload, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("payload type = %T, want map[string]any", result)
	}
	targets, ok := payload["targets"].([]Target)
	if !ok {
		t.Fatalf("targets type = %T, want []Target", payload["targets"])
	}
	if len(targets) != 2 {
		t.Fatalf("len(targets) = %d, want 2", len(targets))
	}
}

func TestEngineRouterDispatchesByTargetPrefix(t *testing.T) {
	ext := &fakeExecEngine{}
	ab := &fakeExecEngine{}
	router := NewEngineRouter(Config{EngineMode: EngineModeHybrid, AgentBrowserEnabled: true}, ext, ab)

	if _, err := router.ExecuteAction(context.Background(), "navigate", ActionRequest{
		TargetID: "ext:10",
		URL:      "https://example.com",
	}); err != nil {
		t.Fatalf("extension navigate error = %v", err)
	}
	if len(ext.executed) != 1 || ext.executed[0] != "navigate" {
		t.Fatalf("extension engine calls = %v, want [navigate]", ext.executed)
	}

	if _, err := router.ExecuteAction(context.Background(), "navigate", ActionRequest{
		TargetID: "ab:session-a:main",
		URL:      "https://example.com",
	}); err != nil {
		t.Fatalf("agent-browser navigate error = %v", err)
	}
	if len(ab.executed) != 1 || ab.executed[0] != "navigate" {
		t.Fatalf("agent-browser engine calls = %v, want [navigate]", ab.executed)
	}
}

func TestEngineRouterSessionActionsUseAgentBrowserEngine(t *testing.T) {
	ext := &fakeExecEngine{}
	ab := &fakeExecEngine{
		createResp: map[string]any{"session_id": "s-1"},
		sessions:   []Session{{ID: "s-1"}},
	}
	router := NewEngineRouter(Config{EngineMode: EngineModeHybrid, AgentBrowserEnabled: true}, ext, ab)

	createRes, err := router.ExecuteAction(context.Background(), "session.create", ActionRequest{
		URL: "https://example.com",
	})
	if err != nil {
		t.Fatalf("session.create error = %v", err)
	}
	if createRes == nil {
		t.Fatal("session.create result is nil")
	}

	listRes, err := router.ExecuteAction(context.Background(), "session.list", ActionRequest{})
	if err != nil {
		t.Fatalf("session.list error = %v", err)
	}
	listPayload, ok := listRes.(map[string]any)
	if !ok {
		t.Fatalf("session.list payload type = %T", listRes)
	}
	if _, ok := listPayload["sessions"].([]Session); !ok {
		t.Fatalf("session.list sessions type = %T, want []Session", listPayload["sessions"])
	}

	if _, err = router.ExecuteAction(context.Background(), "session.close", ActionRequest{
		TargetID: "ab:s-1:main",
	}); err != nil {
		t.Fatalf("session.close error = %v", err)
	}
}

func TestEngineRouterAgentBrowserUnavailableForABTarget(t *testing.T) {
	ext := &fakeExecEngine{}
	ab := &fakeExecEngine{execErr: errors.New("boom")}
	router := NewEngineRouter(Config{EngineMode: EngineModeHybrid, AgentBrowserEnabled: false}, ext, ab)

	_, err := router.ExecuteAction(context.Background(), "navigate", ActionRequest{
		TargetID: "ab:s-1:main",
		URL:      "https://example.com",
	})
	if !errors.Is(err, ErrAgentBrowserUnavailable) {
		t.Fatalf("error = %v, want ErrAgentBrowserUnavailable", err)
	}
}
