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
	"github.com/itsivag/suprclaw/pkg/media"
)

func openSuprReasoningTestSocket(t *testing.T, withMediaStore bool) (*SuprChannel, *bus.MessageBus, *websocket.Conn, func()) {
	t.Helper()

	msgBus := bus.NewMessageBus()
	ch, err := NewSuprChannel(config.SuprConfig{
		Token: "test-token",
	}, msgBus, nil, "")
	if err != nil {
		t.Fatalf("NewSuprChannel() error = %v", err)
	}
	if withMediaStore {
		ch.SetMediaStore(media.NewFileMediaStore())
	}

	ctx, cancel := context.WithCancel(context.Background())
	if err := ch.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	srv := httptest.NewServer(ch)
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/supr/ws?session_id=sess-reasoning"
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

	// First frame is agent.list.
	var first map[string]any
	if err := conn.ReadJSON(&first); err != nil {
		t.Fatalf("ReadJSON(first) error = %v", err)
	}

	cleanup := func() {
		conn.Close()
		srv.Close()
		_ = ch.Stop(context.Background())
		cancel()
	}
	return ch, msgBus, conn, cleanup
}

func waitInbound(t *testing.T, msgBus *bus.MessageBus) bus.InboundMessage {
	t.Helper()
	select {
	case inbound := <-msgBus.InboundChan():
		return inbound
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for inbound message")
		return bus.InboundMessage{}
	}
}

func assertNoInbound(t *testing.T, msgBus *bus.MessageBus) {
	t.Helper()
	select {
	case inbound := <-msgBus.InboundChan():
		t.Fatalf("unexpected inbound message: %+v", inbound)
	case <-time.After(250 * time.Millisecond):
	}
}

func TestSuprMessageSend_ValidReasoningMetadata(t *testing.T) {
	_, msgBus, conn, cleanup := openSuprReasoningTestSocket(t, false)
	defer cleanup()

	err := conn.WriteJSON(SuprMessage{
		Type: TypeMessageSend,
		ID:   "msg-1",
		Payload: map[string]any{
			"content":   "hello",
			"reasoning": "High",
		},
	})
	if err != nil {
		t.Fatalf("WriteJSON(message.send) error = %v", err)
	}

	inbound := waitInbound(t, msgBus)
	if got := inbound.Metadata["reasoning_override"]; got != "high" {
		t.Fatalf("reasoning_override = %q, want %q", got, "high")
	}
}

func TestSuprMessageSend_InvalidReasoningRejected(t *testing.T) {
	_, msgBus, conn, cleanup := openSuprReasoningTestSocket(t, false)
	defer cleanup()

	err := conn.WriteJSON(SuprMessage{
		Type: TypeMessageSend,
		ID:   "msg-2",
		Payload: map[string]any{
			"content":   "hello",
			"reasoning": "ultra",
		},
	})
	if err != nil {
		t.Fatalf("WriteJSON(message.send) error = %v", err)
	}

	var frame map[string]any
	if err := conn.ReadJSON(&frame); err != nil {
		t.Fatalf("ReadJSON(error) error = %v", err)
	}
	if got, _ := frame["type"].(string); got != TypeError {
		t.Fatalf("type = %q, want %q", got, TypeError)
	}
	payload, _ := frame["payload"].(map[string]any)
	if got, _ := payload["code"].(string); got != "invalid_reasoning" {
		t.Fatalf("code = %q, want %q", got, "invalid_reasoning")
	}
	assertNoInbound(t, msgBus)
}

func TestSuprMediaSend_ValidReasoningMetadata(t *testing.T) {
	_, msgBus, conn, cleanup := openSuprReasoningTestSocket(t, true)
	defer cleanup()

	err := conn.WriteJSON(SuprMessage{
		Type: TypeMediaSend,
		ID:   "msg-3",
		Payload: map[string]any{
			"data":         "aGVsbG8=",
			"filename":     "hello.txt",
			"content_type": "text/plain",
			"reasoning":    "xHigh",
		},
	})
	if err != nil {
		t.Fatalf("WriteJSON(media.send) error = %v", err)
	}

	inbound := waitInbound(t, msgBus)
	if got := inbound.Metadata["reasoning_override"]; got != "xhigh" {
		t.Fatalf("reasoning_override = %q, want %q", got, "xhigh")
	}
	if got := inbound.Metadata["model_override"]; got != mediaModelOverride {
		t.Fatalf("model_override = %q, want %q", got, mediaModelOverride)
	}
}

func TestSuprMediaSend_InvalidReasoningRejected(t *testing.T) {
	_, msgBus, conn, cleanup := openSuprReasoningTestSocket(t, true)
	defer cleanup()

	err := conn.WriteJSON(SuprMessage{
		Type: TypeMediaSend,
		ID:   "msg-4",
		Payload: map[string]any{
			"data":         "aGVsbG8=",
			"filename":     "hello.txt",
			"content_type": "text/plain",
			"reasoning":    "bad",
		},
	})
	if err != nil {
		t.Fatalf("WriteJSON(media.send) error = %v", err)
	}

	var frame map[string]any
	if err := conn.ReadJSON(&frame); err != nil {
		t.Fatalf("ReadJSON(error) error = %v", err)
	}
	if got, _ := frame["type"].(string); got != TypeError {
		t.Fatalf("type = %q, want %q", got, TypeError)
	}
	payload, _ := frame["payload"].(map[string]any)
	if got, _ := payload["code"].(string); got != "invalid_reasoning" {
		t.Fatalf("code = %q, want %q", got, "invalid_reasoning")
	}
	assertNoInbound(t, msgBus)
}
