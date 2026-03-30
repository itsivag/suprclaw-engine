package supr

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/itsivag/suprclaw/pkg/bus"
	"github.com/itsivag/suprclaw/pkg/config"
)

func TestSuprBroadcastActivityEvent_EmitsCanonicalEnvelope(t *testing.T) {
	msgBus := bus.NewMessageBus()
	ch, err := NewSuprChannel(config.SuprConfig{
		Token: "test-token",
	}, msgBus, nil, "")
	if err != nil {
		t.Fatalf("NewSuprChannel() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := ch.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer ch.Stop(context.Background())

	srv := httptest.NewServer(ch)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/supr/ws?session_id=sess-1"
	dialer := websocket.Dialer{
		Subprotocols: []string{"token.test-token"},
	}
	conn, _, err := dialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("websocket dial error = %v", err)
	}
	defer conn.Close()

	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline() error = %v", err)
	}

	// First server frame is legacy control event: agent.list.
	var first map[string]any
	if err := conn.ReadJSON(&first); err != nil {
		t.Fatalf("ReadJSON(first) error = %v", err)
	}
	if typ, _ := first["type"].(string); typ != TypeAgentList {
		t.Fatalf("first frame type = %q, want %q", typ, TypeAgentList)
	}

	envelope := bus.ActivityEventEnvelope{
		V:              "1.0",
		EventID:        "evt_1",
		EventType:      "run.started",
		Timestamp:      "2026-03-30T10:12:41.203Z",
		Sequence:       1,
		SessionID:      "sess-1",
		RunID:          "run-1",
		ParentRunID:    nil,
		AgentID:        "agent_main",
		IdempotencyKey: "run-1_1",
		Replay:         false,
		Data: map[string]any{
			"title": "Test run",
		},
	}

	if err := ch.BroadcastActivityEvent(context.Background(), "supr:sess-1", envelope); err != nil {
		t.Fatalf("BroadcastActivityEvent() error = %v", err)
	}

	var frame map[string]any
	if err := conn.ReadJSON(&frame); err != nil {
		t.Fatalf("ReadJSON(activity) error = %v", err)
	}

	if _, hasLegacyType := frame["type"]; hasLegacyType {
		t.Fatalf("activity frame should be canonical envelope, got legacy type wrapper")
	}
	if got, _ := frame["event_type"].(string); got != "run.started" {
		t.Fatalf("event_type = %q, want %q", got, "run.started")
	}
	if got, _ := frame["run_id"].(string); got != "run-1" {
		t.Fatalf("run_id = %q, want %q", got, "run-1")
	}
	if got, _ := frame["session_id"].(string); got != "sess-1" {
		t.Fatalf("session_id = %q, want %q", got, "sess-1")
	}
}
