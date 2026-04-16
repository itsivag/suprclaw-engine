package supr

import (
	"context"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/itsivag/suprclaw/pkg/bus"
	"github.com/itsivag/suprclaw/pkg/channels"
	"github.com/itsivag/suprclaw/pkg/config"
)

type stubRunController struct {
	mu            sync.Mutex
	cancelled     bool
	resolvedRunID string
	err           error
	callCount     int
	channel       string
	chatID        string
	runID         string
	reason        string
}

func (s *stubRunController) CancelRun(channel, chatID, runID, reason string) (bool, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.callCount++
	s.channel = channel
	s.chatID = chatID
	s.runID = runID
	s.reason = reason
	return s.cancelled, s.resolvedRunID, s.err
}

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
	if got, _ := frame["agent_id"].(string); got != "agent_main" {
		t.Fatalf("agent_id = %q, want %q", got, "agent_main")
	}
	data, _ := frame["data"].(map[string]any)
	if got, _ := data["agent_id"].(string); got != "agent_main" {
		t.Fatalf("data.agent_id = %q, want %q", got, "agent_main")
	}
}

func TestSuprSend_EmitsCanonicalMessageCompleted(t *testing.T) {
	ch, conn, cleanup := openSuprTestSocket(t)
	defer cleanup()

	ch.rememberRunStatus("supr:sess-1", bus.ActivityEventEnvelope{
		EventType: "run.started",
		RunID:     "run-1",
		SessionID: "sess-1",
		Sequence:  3,
	})
	if err := ch.Send(context.Background(), bus.OutboundMessage{
		Channel:         "supr",
		ChatID:          "supr:sess-1",
		Content:         "hello world",
		ResolvedAgentID: "agent_writer",
	}); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	var frame map[string]any
	if err := conn.ReadJSON(&frame); err != nil {
		t.Fatalf("ReadJSON(message.completed) error = %v", err)
	}
	if _, hasLegacyType := frame["type"]; hasLegacyType {
		t.Fatalf("expected canonical envelope, got legacy typed frame")
	}
	if got, _ := frame["event_type"].(string); got != "message.completed" {
		t.Fatalf("event_type = %q, want %q", got, "message.completed")
	}
	if got, _ := frame["run_id"].(string); got != "run-1" {
		t.Fatalf("run_id = %q, want %q", got, "run-1")
	}
	data, _ := frame["data"].(map[string]any)
	if got, _ := data["text"].(string); got != "hello world" {
		t.Fatalf("data.text = %q, want %q", got, "hello world")
	}
	if got, _ := frame["agent_id"].(string); got != "agent_writer" {
		t.Fatalf("agent_id = %q, want %q", got, "agent_writer")
	}
	if got, _ := data["agent_id"].(string); got != "agent_writer" {
		t.Fatalf("data.agent_id = %q, want %q", got, "agent_writer")
	}
	if got, _ := frame["sequence"].(float64); got <= 3 {
		t.Fatalf("sequence = %v, want > 3", got)
	}
}

func TestSuprSend_EmitsCanonicalErrorRaised(t *testing.T) {
	ch, conn, cleanup := openSuprTestSocket(t)
	defer cleanup()

	ch.rememberRunStatus("supr:sess-1", bus.ActivityEventEnvelope{
		EventType: "run.started",
		RunID:     "run-err",
		SessionID: "sess-1",
		Sequence:  2,
	})
	if err := ch.Send(context.Background(), bus.OutboundMessage{
		Channel:         "supr",
		ChatID:          "supr:sess-1",
		ErrorCode:       "TEST_ERROR",
		ErrorMessage:    "boom",
		ResolvedAgentID: "agent_writer",
	}); err != nil {
		t.Fatalf("Send(error) error = %v", err)
	}

	var frame map[string]any
	if err := conn.ReadJSON(&frame); err != nil {
		t.Fatalf("ReadJSON(error.raised) error = %v", err)
	}
	if _, hasLegacyType := frame["type"]; hasLegacyType {
		t.Fatalf("expected canonical envelope, got legacy typed frame")
	}
	if got, _ := frame["event_type"].(string); got != "error.raised" {
		t.Fatalf("event_type = %q, want %q", got, "error.raised")
	}
	data, _ := frame["data"].(map[string]any)
	if got, _ := data["code"].(string); got != "TEST_ERROR" {
		t.Fatalf("data.code = %q, want %q", got, "TEST_ERROR")
	}
	if got, _ := data["message"].(string); got != "boom" {
		t.Fatalf("data.message = %q, want %q", got, "boom")
	}
	if got, _ := frame["agent_id"].(string); got != "agent_writer" {
		t.Fatalf("agent_id = %q, want %q", got, "agent_writer")
	}
	if got, _ := data["agent_id"].(string); got != "agent_writer" {
		t.Fatalf("data.agent_id = %q, want %q", got, "agent_writer")
	}
}

func TestSuprSend_UsesRouteMetadataAgentFallback(t *testing.T) {
	ch, conn, cleanup := openSuprTestSocket(t)
	defer cleanup()

	ch.rememberRouteMetadata("supr:sess-1", "agent_route", "binding.channel")
	ch.rememberRunStatus("supr:sess-1", bus.ActivityEventEnvelope{
		EventType: "run.started",
		RunID:     "run-1",
		SessionID: "sess-1",
		Sequence:  1,
	})

	if err := ch.Send(context.Background(), bus.OutboundMessage{
		Channel: "supr",
		ChatID:  "supr:sess-1",
		Content: "hello from fallback",
	}); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	var frame map[string]any
	if err := conn.ReadJSON(&frame); err != nil {
		t.Fatalf("ReadJSON(message.completed) error = %v", err)
	}
	if got, _ := frame["event_type"].(string); got != "message.completed" {
		t.Fatalf("event_type = %q, want %q", got, "message.completed")
	}
	if got, _ := frame["agent_id"].(string); got != "agent_route" {
		t.Fatalf("agent_id = %q, want %q", got, "agent_route")
	}
	data, _ := frame["data"].(map[string]any)
	if got, _ := data["agent_id"].(string); got != "agent_route" {
		t.Fatalf("data.agent_id = %q, want %q", got, "agent_route")
	}
	if got, _ := data["resolved_agent_id"].(string); got != "agent_route" {
		t.Fatalf("data.resolved_agent_id = %q, want %q", got, "agent_route")
	}
}

func TestSuprStartTyping_DoesNotEmitLegacyFrames(t *testing.T) {
	ch, conn, cleanup := openSuprTestSocket(t)
	defer cleanup()

	stop, err := ch.StartTyping(context.Background(), "supr:sess-1")
	if err != nil {
		t.Fatalf("StartTyping() error = %v", err)
	}
	stop()

	if err := conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond)); err != nil {
		t.Fatalf("SetReadDeadline() error = %v", err)
	}
	var frame map[string]any
	if err := conn.ReadJSON(&frame); err == nil {
		t.Fatalf("unexpected frame after StartTyping no-op: %+v", frame)
	}
}

func TestSuprRunStop_CancelsActiveRunWithoutTypedError(t *testing.T) {
	ch, conn, cleanup := openSuprTestSocket(t)
	defer cleanup()

	controller := &stubRunController{
		cancelled:     true,
		resolvedRunID: "run-1",
	}
	ch.SetRunController(controller)

	if err := conn.WriteJSON(SuprMessage{
		Type: TypeRunStop,
		Payload: map[string]any{
			"run_id": "run-1",
			"reason": "Stop now",
		},
	}); err != nil {
		t.Fatalf("WriteJSON(run.stop) error = %v", err)
	}

	if err := conn.SetReadDeadline(time.Now().Add(250 * time.Millisecond)); err != nil {
		t.Fatalf("SetReadDeadline() error = %v", err)
	}
	var frame map[string]any
	if err := conn.ReadJSON(&frame); err == nil {
		t.Fatalf("unexpected frame for successful run.stop: %+v", frame)
	}

	controller.mu.Lock()
	defer controller.mu.Unlock()
	if controller.callCount != 1 {
		t.Fatalf("CancelRun call count = %d, want 1", controller.callCount)
	}
	if controller.channel != "supr" {
		t.Fatalf("CancelRun channel = %q, want %q", controller.channel, "supr")
	}
	if controller.chatID != "supr:sess-1" {
		t.Fatalf("CancelRun chatID = %q, want %q", controller.chatID, "supr:sess-1")
	}
	if controller.runID != "run-1" {
		t.Fatalf("CancelRun runID = %q, want %q", controller.runID, "run-1")
	}
	if controller.reason != "Stop now" {
		t.Fatalf("CancelRun reason = %q, want %q", controller.reason, "Stop now")
	}
}

func TestSuprRunStop_NoActiveRunReturnsTypedError(t *testing.T) {
	ch, conn, cleanup := openSuprTestSocket(t)
	defer cleanup()

	ch.SetRunController(&stubRunController{
		err: &channels.RunControlError{
			Code:    "no_active_run",
			Message: "no active run to stop",
		},
	})

	if err := conn.WriteJSON(SuprMessage{
		Type:    TypeRunStop,
		Payload: map[string]any{},
	}); err != nil {
		t.Fatalf("WriteJSON(run.stop) error = %v", err)
	}

	var frame map[string]any
	if err := conn.ReadJSON(&frame); err != nil {
		t.Fatalf("ReadJSON(error) error = %v", err)
	}
	if got, _ := frame["type"].(string); got != TypeError {
		t.Fatalf("type = %q, want %q", got, TypeError)
	}
	payload, _ := frame["payload"].(map[string]any)
	if got, _ := payload["code"].(string); got != "no_active_run" {
		t.Fatalf("code = %q, want %q", got, "no_active_run")
	}
}

func TestSuprRunStop_RunMismatchReturnsTypedError(t *testing.T) {
	ch, conn, cleanup := openSuprTestSocket(t)
	defer cleanup()

	ch.SetRunController(&stubRunController{
		err: &channels.RunControlError{
			Code:    "run_mismatch",
			Message: "requested run does not match active run",
		},
	})

	if err := conn.WriteJSON(SuprMessage{
		Type: TypeRunStop,
		Payload: map[string]any{
			"run_id": "run-x",
		},
	}); err != nil {
		t.Fatalf("WriteJSON(run.stop) error = %v", err)
	}

	var frame map[string]any
	if err := conn.ReadJSON(&frame); err != nil {
		t.Fatalf("ReadJSON(error) error = %v", err)
	}
	if got, _ := frame["type"].(string); got != TypeError {
		t.Fatalf("type = %q, want %q", got, TypeError)
	}
	payload, _ := frame["payload"].(map[string]any)
	if got, _ := payload["code"].(string); got != "run_mismatch" {
		t.Fatalf("code = %q, want %q", got, "run_mismatch")
	}
}

func TestSuprRunStatusGet_ReturnsUnknownWhenNoTrackedRun(t *testing.T) {
	_, conn, cleanup := openSuprTestSocket(t)
	defer cleanup()

	if err := conn.WriteJSON(SuprMessage{
		Type: TypeRunStatusGet,
		ID:   "status-1",
	}); err != nil {
		t.Fatalf("WriteJSON(run.status.get) error = %v", err)
	}

	var frame map[string]any
	if err := conn.ReadJSON(&frame); err != nil {
		t.Fatalf("ReadJSON(run.status) error = %v", err)
	}

	if got, _ := frame["type"].(string); got != TypeRunStatus {
		t.Fatalf("type = %q, want %q", got, TypeRunStatus)
	}
	if got, _ := frame["id"].(string); got != "status-1" {
		t.Fatalf("id = %q, want %q", got, "status-1")
	}
	payload, _ := frame["payload"].(map[string]any)
	if got, _ := payload["status"].(string); got != runStatusUnknown {
		t.Fatalf("status = %q, want %q", got, runStatusUnknown)
	}
}

func TestSuprRunStatusGet_ReturnsTrackedRunStatus(t *testing.T) {
	ch, conn, cleanup := openSuprTestSocket(t)
	defer cleanup()

	ch.rememberRunStatus("supr:sess-1", bus.ActivityEventEnvelope{
		EventType: "run.started",
		RunID:     "run-42",
		SessionID: "sess-1",
		Sequence:  1,
	})

	if err := conn.WriteJSON(SuprMessage{
		Type: TypeRunStatusGet,
		ID:   "status-2",
		Payload: map[string]any{
			"run_id": "run-42",
		},
	}); err != nil {
		t.Fatalf("WriteJSON(run.status.get) error = %v", err)
	}

	var frame map[string]any
	if err := conn.ReadJSON(&frame); err != nil {
		t.Fatalf("ReadJSON(run.status) error = %v", err)
	}

	if got, _ := frame["type"].(string); got != TypeRunStatus {
		t.Fatalf("type = %q, want %q", got, TypeRunStatus)
	}
	if got, _ := frame["id"].(string); got != "status-2" {
		t.Fatalf("id = %q, want %q", got, "status-2")
	}
	payload, _ := frame["payload"].(map[string]any)
	if got, _ := payload["run_id"].(string); got != "run-42" {
		t.Fatalf("run_id = %q, want %q", got, "run-42")
	}
	if got, _ := payload["status"].(string); got != runStatusInProgress {
		t.Fatalf("status = %q, want %q", got, runStatusInProgress)
	}
}
