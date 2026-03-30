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

func openSuprTestSocket(t *testing.T) (*SuprChannel, *websocket.Conn, func()) {
	t.Helper()

	msgBus := bus.NewMessageBus()
	ch, err := NewSuprChannel(config.SuprConfig{
		Token: "test-token",
	}, msgBus, nil, "")
	if err != nil {
		t.Fatalf("NewSuprChannel() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	if err := ch.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	srv := httptest.NewServer(ch)
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/supr/ws?session_id=sess-1"
	dialer := websocket.Dialer{
		Subprotocols: []string{"token.test-token"},
	}
	conn, _, err := dialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("websocket dial error = %v", err)
	}

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

	cleanup := func() {
		conn.Close()
		srv.Close()
		ch.Stop(context.Background())
		cancel()
	}
	return ch, conn, cleanup
}

func TestSuprBroadcastActivityEvent_EmitsCanonicalEnvelope(t *testing.T) {
	ch, conn, cleanup := openSuprTestSocket(t)
	defer cleanup()

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

func TestSuprRunStatusGet_UnknownWhenNoRunState(t *testing.T) {
	_, conn, cleanup := openSuprTestSocket(t)
	defer cleanup()

	req := SuprMessage{
		Type:      TypeRunStatusGet,
		ID:        "req-1",
		SessionID: "sess-1",
	}
	if err := conn.WriteJSON(req); err != nil {
		t.Fatalf("WriteJSON(run.status.get) error = %v", err)
	}

	var frame map[string]any
	if err := conn.ReadJSON(&frame); err != nil {
		t.Fatalf("ReadJSON(run.status) error = %v", err)
	}
	if got, _ := frame["type"].(string); got != TypeRunStatus {
		t.Fatalf("type = %q, want %q", got, TypeRunStatus)
	}
	payload, _ := frame["payload"].(map[string]any)
	if got, _ := payload["status"].(string); got != runStatusUnknown {
		t.Fatalf("status = %q, want %q", got, runStatusUnknown)
	}
	if got, _ := payload["run_id"].(string); got != "" {
		t.Fatalf("run_id = %q, want empty", got)
	}
}

func TestSuprRunStatusGet_HonorsRequestedRunID(t *testing.T) {
	ch, conn, cleanup := openSuprTestSocket(t)
	defer cleanup()

	ch.rememberRunStatus("supr:sess-1", bus.ActivityEventEnvelope{
		EventType: "run.started",
		RunID:     "run-1",
		SessionID: "sess-1",
	})
	ch.rememberRunStatus("supr:sess-1", bus.ActivityEventEnvelope{
		EventType: "run.completed",
		RunID:     "run-1",
		SessionID: "sess-1",
	})
	ch.rememberRunStatus("supr:sess-1", bus.ActivityEventEnvelope{
		EventType: "run.started",
		RunID:     "run-2",
		SessionID: "sess-1",
	})

	req := SuprMessage{
		Type:      TypeRunStatusGet,
		ID:        "req-2",
		SessionID: "sess-1",
		Payload: map[string]any{
			"run_id": "run-1",
		},
	}
	if err := conn.WriteJSON(req); err != nil {
		t.Fatalf("WriteJSON(run.status.get) error = %v", err)
	}

	var frame map[string]any
	if err := conn.ReadJSON(&frame); err != nil {
		t.Fatalf("ReadJSON(run.status) error = %v", err)
	}
	payload, _ := frame["payload"].(map[string]any)
	if got, _ := payload["run_id"].(string); got != "run-1" {
		t.Fatalf("run_id = %q, want %q", got, "run-1")
	}
	if got, _ := payload["status"].(string); got != runStatusCompleted {
		t.Fatalf("status = %q, want %q", got, runStatusCompleted)
	}

	latestReq := SuprMessage{
		Type:      TypeRunStatusGet,
		ID:        "req-2-latest",
		SessionID: "sess-1",
	}
	if err := conn.WriteJSON(latestReq); err != nil {
		t.Fatalf("WriteJSON(run.status.get latest) error = %v", err)
	}
	var latestFrame map[string]any
	if err := conn.ReadJSON(&latestFrame); err != nil {
		t.Fatalf("ReadJSON(run.status latest) error = %v", err)
	}
	latestPayload, _ := latestFrame["payload"].(map[string]any)
	if got, _ := latestPayload["run_id"].(string); got != "run-2" {
		t.Fatalf("latest run_id = %q, want %q", got, "run-2")
	}
	if got, _ := latestPayload["status"].(string); got != runStatusInProgress {
		t.Fatalf("latest status = %q, want %q", got, runStatusInProgress)
	}
}

func TestSuprRunStatusGet_UnknownForMissingRequestedRun(t *testing.T) {
	ch, conn, cleanup := openSuprTestSocket(t)
	defer cleanup()

	ch.rememberRunStatus("supr:sess-1", bus.ActivityEventEnvelope{
		EventType: "run.started",
		RunID:     "run-2",
		SessionID: "sess-1",
	})

	req := SuprMessage{
		Type:      TypeRunStatusGet,
		ID:        "req-3",
		SessionID: "sess-1",
		Payload: map[string]any{
			"run_id": "run-missing",
		},
	}
	if err := conn.WriteJSON(req); err != nil {
		t.Fatalf("WriteJSON(run.status.get) error = %v", err)
	}

	var frame map[string]any
	if err := conn.ReadJSON(&frame); err != nil {
		t.Fatalf("ReadJSON(run.status) error = %v", err)
	}
	payload, _ := frame["payload"].(map[string]any)
	if got, _ := payload["run_id"].(string); got != "run-missing" {
		t.Fatalf("run_id = %q, want %q", got, "run-missing")
	}
	if got, _ := payload["status"].(string); got != runStatusUnknown {
		t.Fatalf("status = %q, want %q", got, runStatusUnknown)
	}
}
