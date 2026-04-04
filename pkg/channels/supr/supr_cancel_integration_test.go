package supr

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"net/http/httptest"

	"github.com/gorilla/websocket"

	"github.com/itsivag/suprclaw/pkg/agent"
	"github.com/itsivag/suprclaw/pkg/bus"
	"github.com/itsivag/suprclaw/pkg/config"
	"github.com/itsivag/suprclaw/pkg/providers"
)

type blockingProvider struct {
	startOnce sync.Once
}

func (p *blockingProvider) Chat(
	ctx context.Context,
	_ []providers.Message,
	_ []providers.ToolDefinition,
	_ string,
	_ map[string]any,
) (*providers.LLMResponse, error) {
	p.startOnce.Do(func() {})
	<-ctx.Done()
	return nil, ctx.Err()
}

func (p *blockingProvider) GetDefaultModel() string {
	return "blocking-model"
}

func waitForCanonicalEvent(t *testing.T, conn *websocket.Conn, timeout time.Duration) map[string]any {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		t.Fatalf("SetReadDeadline() error = %v", err)
	}
	for {
		var frame map[string]any
		if err := conn.ReadJSON(&frame); err != nil {
			t.Fatalf("ReadJSON() error = %v", err)
		}
		if _, ok := frame["event_type"].(string); ok {
			return frame
		}
	}
}

func TestSuprRunStop_StopsActiveRunAndEmitsStoppedEvents(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "supr-run-stop-*")
	if err != nil {
		t.Fatalf("MkdirTemp() error = %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			MaxParallelRuns: 1,
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "blocking-model",
				MaxTokens:         8192,
				MaxToolIterations: 4,
				ContextGuard: config.ContextGuardConfig{
					Enabled: false,
				},
			},
		},
	}

	msgBus := bus.NewMessageBus()
	provider := &blockingProvider{}
	al := agent.NewAgentLoop(cfg, msgBus, provider)

	ch, err := NewSuprChannel(config.SuprConfig{
		Token: "test-token",
	}, msgBus, nil, "")
	if err != nil {
		t.Fatalf("NewSuprChannel() error = %v", err)
	}
	ch.SetRunController(al)

	rootCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := ch.Start(rootCtx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	go func() {
		_ = al.Run(rootCtx)
	}()

	go func() {
		for {
			select {
			case <-rootCtx.Done():
				return
			case evt, ok := <-msgBus.OutboundActivityChan():
				if !ok {
					return
				}
				_ = ch.BroadcastActivityEvent(rootCtx, evt.ChatID, evt.Event)
			}
		}
	}()

	srv := httptest.NewServer(ch)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/supr/ws?session_id=sess-stop"
	dialer := websocket.Dialer{
		Subprotocols: []string{"token.test-token"},
	}
	conn, _, err := dialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("websocket dial error = %v", err)
	}
	defer conn.Close()

	// First frame is agent.list.
	var first map[string]any
	if err := conn.ReadJSON(&first); err != nil {
		t.Fatalf("ReadJSON(first) error = %v", err)
	}
	if typ, _ := first["type"].(string); typ != TypeAgentList {
		t.Fatalf("first frame type = %q, want %q", typ, TypeAgentList)
	}

	if err := conn.WriteJSON(SuprMessage{
		Type: TypeMessageSend,
		ID:   "msg-1",
		Payload: map[string]any{
			"content": "long running task",
		},
	}); err != nil {
		t.Fatalf("WriteJSON(message.send) error = %v", err)
	}

	runID := ""
	startDeadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(startDeadline) {
		frame := waitForCanonicalEvent(t, conn, 3*time.Second)
		if got, _ := frame["event_type"].(string); got == "run.started" {
			runID, _ = frame["run_id"].(string)
			break
		}
	}
	if runID == "" {
		t.Fatal("missing run.started event")
	}

	if err := conn.WriteJSON(SuprMessage{
		Type: TypeRunStop,
		Payload: map[string]any{
			"run_id": runID,
		},
	}); err != nil {
		t.Fatalf("WriteJSON(run.stop) error = %v", err)
	}

	seenStoppedMessage := false
	seenRunFailed := false
	seenRunCompleted := false
	messageIndex := -1
	failedIndex := -1
	index := 0

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		frame := waitForCanonicalEvent(t, conn, 5*time.Second)
		eventType, _ := frame["event_type"].(string)
		switch eventType {
		case "message.completed":
			data, _ := frame["data"].(map[string]any)
			if text, _ := data["text"].(string); text == "Stopped by user." {
				seenStoppedMessage = true
				messageIndex = index
			}
		case "run.failed":
			data, _ := frame["data"].(map[string]any)
			if code, _ := data["error_code"].(string); code == "RUN_CANCELLED" {
				seenRunFailed = true
				failedIndex = index
			}
		case "run.completed":
			seenRunCompleted = true
		}
		index++
		if seenStoppedMessage && seenRunFailed {
			break
		}
	}

	if !seenStoppedMessage {
		t.Fatal("missing message.completed with stop text")
	}
	if !seenRunFailed {
		t.Fatal("missing run.failed RUN_CANCELLED event")
	}
	if seenRunCompleted {
		t.Fatal("unexpected run.completed event after run.stop")
	}
	if failedIndex != -1 && messageIndex != -1 && failedIndex <= messageIndex {
		t.Fatalf("event order invalid: run.failed index=%d, message.completed index=%d", failedIndex, messageIndex)
	}
}
