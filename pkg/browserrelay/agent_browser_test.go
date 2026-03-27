package browserrelay

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fakeAgentBrowserRunner struct {
	calls []agentBrowserCall
	runFn func(call agentBrowserCall) ([]byte, []byte, error)
}

type agentBrowserCall struct {
	binary string
	args   []string
	stdin  []byte
}

func (f *fakeAgentBrowserRunner) Run(_ context.Context, binary string, args []string, stdin []byte) ([]byte, []byte, error) {
	call := agentBrowserCall{
		binary: binary,
		args:   append([]string(nil), args...),
		stdin:  append([]byte(nil), stdin...),
	}
	f.calls = append(f.calls, call)
	if f.runFn != nil {
		return f.runFn(call)
	}
	return []byte(`{"ok":true}`), nil, nil
}

func TestAgentBrowserEngineCreateSessionCreatesABTarget(t *testing.T) {
	runner := &fakeAgentBrowserRunner{}
	engine := NewAgentBrowserEngine(Config{
		AgentBrowserEnabled:        true,
		AgentBrowserBinary:         "agent-browser",
		AgentBrowserMaxSessions:    2,
		AgentBrowserIdleTimeoutSec: 60,
	}, runner)

	res, err := engine.CreateSession(context.Background(), ActionRequest{URL: "https://example.com"})
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}
	payload, ok := res.(map[string]any)
	if !ok {
		t.Fatalf("CreateSession() payload type = %T", res)
	}
	targetID, _ := payload["target_id"].(string)
	if !strings.HasPrefix(targetID, "ab:") {
		t.Fatalf("target_id = %q, want ab:*", targetID)
	}
	if len(runner.calls) == 0 {
		t.Fatal("runner was not called")
	}
}

func TestAgentBrowserEngineNavigateTranslatesToOpenCommand(t *testing.T) {
	runner := &fakeAgentBrowserRunner{}
	engine := NewAgentBrowserEngine(Config{
		AgentBrowserEnabled: true,
		AgentBrowserBinary:  "agent-browser",
	}, runner)

	res, err := engine.CreateSession(context.Background(), ActionRequest{URL: "about:blank"})
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}
	sessionID, _ := res.(map[string]any)["session_id"].(string)
	targetID := BuildAgentBrowserTargetID(sessionID, "main")

	_, err = engine.ExecuteAction(context.Background(), "navigate", ActionRequest{
		TargetID: targetID,
		URL:      "https://example.com",
	})
	if err != nil {
		t.Fatalf("ExecuteAction(navigate) error = %v", err)
	}
	last := runner.calls[len(runner.calls)-1]
	argsJoined := strings.Join(last.args, " ")
	if !strings.Contains(argsJoined, " open https://example.com") {
		t.Fatalf("navigate args = %q, want open https://example.com", argsJoined)
	}
}

func TestAgentBrowserEngineScreenshotReturnsBase64Data(t *testing.T) {
	tmpDir := t.TempDir()
	imagePath := filepath.Join(tmpDir, "shot.png")
	imageBytes := []byte{0x89, 0x50, 0x4e, 0x47}
	if err := os.WriteFile(imagePath, imageBytes, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	runner := &fakeAgentBrowserRunner{
		runFn: func(call agentBrowserCall) ([]byte, []byte, error) {
			if strings.Contains(strings.Join(call.args, " "), " screenshot ") {
				out, _ := json.Marshal(map[string]any{"path": imagePath})
				return out, nil, nil
			}
			return []byte(`{"ok":true}`), nil, nil
		},
	}
	engine := NewAgentBrowserEngine(Config{
		AgentBrowserEnabled:        true,
		AgentBrowserBinary:         "agent-browser",
		AgentBrowserIdleTimeoutSec: 300,
	}, runner)
	engine.now = func() time.Time { return time.Now().UTC() }

	res, err := engine.CreateSession(context.Background(), ActionRequest{URL: "about:blank"})
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}
	sessionID := res.(map[string]any)["session_id"].(string)
	targetID := BuildAgentBrowserTargetID(sessionID, "main")

	resp, err := engine.ExecuteAction(context.Background(), "screenshot", ActionRequest{TargetID: targetID})
	if err != nil {
		t.Fatalf("ExecuteAction(screenshot) error = %v", err)
	}
	payload, ok := resp.(map[string]any)
	if !ok {
		t.Fatalf("screenshot payload type = %T", resp)
	}
	got, _ := payload["data"].(string)
	want := base64.StdEncoding.EncodeToString(imageBytes)
	if got != want {
		t.Fatalf("screenshot data = %q, want %q", got, want)
	}
}
