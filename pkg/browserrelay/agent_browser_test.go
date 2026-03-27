package browserrelay

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

type fakeAgentBrowserRunner struct {
	mu    sync.Mutex
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
	f.mu.Lock()
	f.calls = append(f.calls, call)
	f.mu.Unlock()

	if f.runFn != nil {
		return f.runFn(call)
	}
	return []byte(`{"ok":true}`), nil, nil
}

func (f *fakeAgentBrowserRunner) snapshotCalls() []agentBrowserCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]agentBrowserCall, len(f.calls))
	copy(out, f.calls)
	return out
}

func TestAgentBrowserEngineCreateSessionCreatesABTarget(t *testing.T) {
	runner := &fakeAgentBrowserRunner{
		runFn: func(call agentBrowserCall) ([]byte, []byte, error) {
			joined := strings.Join(call.args, " ")
			if strings.Contains(joined, " stream status") {
				return []byte(`{"enabled":true,"port":9223,"ws_url":"ws://127.0.0.1:9223"}`), nil, nil
			}
			return []byte(`{"ok":true}`), nil, nil
		},
	}
	engine := NewAgentBrowserEngine(Config{
		AgentBrowserEnabled:                 true,
		AgentBrowserBinary:                  "agent-browser",
		AgentBrowserMaxSessions:             2,
		AgentBrowserIdleTimeoutSec:          60,
		AgentBrowserStreamEnabled:           true,
		AgentBrowserBatchWindowMS:           25,
		AgentBrowserBatchMaxSteps:           24,
		AgentBrowserRuntimeCommandTimeoutMS: 30000,
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
	if enabled, _ := payload["stream_enabled"].(bool); !enabled {
		t.Fatalf("stream_enabled = %v, want true", payload["stream_enabled"])
	}

	calls := runner.snapshotCalls()
	if len(calls) == 0 {
		t.Fatal("runner was not called")
	}
	if !strings.Contains(strings.Join(calls[0].args, " "), " open https://example.com") {
		t.Fatalf("first command = %q, want open https://example.com", strings.Join(calls[0].args, " "))
	}
}

func TestAgentBrowserEngineNavigateUsesBatchTransport(t *testing.T) {
	runner := &fakeAgentBrowserRunner{}
	engine := NewAgentBrowserEngine(Config{
		AgentBrowserEnabled:                 true,
		AgentBrowserBinary:                  "agent-browser",
		AgentBrowserStreamEnabled:           false,
		AgentBrowserBatchWindowMS:           25,
		AgentBrowserBatchMaxSteps:           24,
		AgentBrowserRuntimeCommandTimeoutMS: 30000,
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

	var batchCall *agentBrowserCall
	for _, call := range runner.snapshotCalls() {
		if strings.Contains(strings.Join(call.args, " "), " batch") {
			copyCall := call
			batchCall = &copyCall
			break
		}
	}
	if batchCall == nil {
		t.Fatal("no batch call captured")
	}
	var commands [][]string
	if err = json.Unmarshal(batchCall.stdin, &commands); err != nil {
		t.Fatalf("batch stdin json error = %v", err)
	}
	if len(commands) != 1 {
		t.Fatalf("batch commands len = %d, want 1", len(commands))
	}
	if len(commands[0]) < 2 || commands[0][0] != "open" || commands[0][1] != "https://example.com" {
		t.Fatalf("batch command = %#v, want [open https://example.com]", commands[0])
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
			if strings.Contains(strings.Join(call.args, " "), " batch") {
				out, _ := json.Marshal([]any{map[string]any{"ok": true, "data": map[string]any{"path": imagePath}}})
				return out, nil, nil
			}
			return []byte(`{"ok":true}`), nil, nil
		},
	}
	engine := NewAgentBrowserEngine(Config{
		AgentBrowserEnabled:                 true,
		AgentBrowserBinary:                  "agent-browser",
		AgentBrowserStreamEnabled:           false,
		AgentBrowserBatchWindowMS:           25,
		AgentBrowserBatchMaxSteps:           24,
		AgentBrowserRuntimeCommandTimeoutMS: 30000,
	}, runner)

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

func TestAgentBrowserEngineCoalescesQueuedRequests(t *testing.T) {
	var (
		mu           sync.Mutex
		batchPayload [][][]string
	)
	runner := &fakeAgentBrowserRunner{
		runFn: func(call agentBrowserCall) ([]byte, []byte, error) {
			if strings.Contains(strings.Join(call.args, " "), " batch") {
				var commands [][]string
				_ = json.Unmarshal(call.stdin, &commands)
				mu.Lock()
				batchPayload = append(batchPayload, commands)
				mu.Unlock()
				results := make([]any, 0, len(commands))
				for range commands {
					results = append(results, map[string]any{"ok": true})
				}
				out, _ := json.Marshal(results)
				return out, nil, nil
			}
			return []byte(`{"ok":true}`), nil, nil
		},
	}
	engine := NewAgentBrowserEngine(Config{
		AgentBrowserEnabled:                 true,
		AgentBrowserBinary:                  "agent-browser",
		AgentBrowserStreamEnabled:           false,
		AgentBrowserBatchWindowMS:           40,
		AgentBrowserBatchMaxSteps:           24,
		AgentBrowserRuntimeCommandTimeoutMS: 30000,
	}, runner)

	res, err := engine.CreateSession(context.Background(), ActionRequest{URL: "about:blank"})
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}
	sessionID := res.(map[string]any)["session_id"].(string)
	targetID := BuildAgentBrowserTargetID(sessionID, "main")

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = engine.ExecuteAction(context.Background(), "navigate", ActionRequest{
			TargetID: targetID,
			URL:      "https://example.com",
		})
	}()
	go func() {
		defer wg.Done()
		_, _ = engine.ExecuteAction(context.Background(), "press", ActionRequest{
			TargetID: targetID,
			Key:      "Enter",
		})
	}()
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	if len(batchPayload) == 0 {
		t.Fatal("expected at least one batch payload")
	}
	foundTwoStepBatch := false
	for _, payload := range batchPayload {
		if len(payload) >= 2 {
			foundTwoStepBatch = true
			break
		}
	}
	if !foundTwoStepBatch {
		t.Fatalf("expected coalesced batch with >=2 steps, got payloads: %#v", batchPayload)
	}
}
