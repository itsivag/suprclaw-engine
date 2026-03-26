package browserrelay

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"
)

type fakeConn struct {
	mu      sync.Mutex
	onWrite func(v any)
	closed  bool
}

func (f *fakeConn) WriteJSON(v any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return errors.New("closed")
	}
	if f.onWrite != nil {
		f.onWrite(v)
	}
	return nil
}

func (f *fakeConn) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	return nil
}

func TestSendCommandRequestCorrelation(t *testing.T) {
	m := NewManager(Config{Enabled: true, IdleTimeoutSec: 60})
	t.Cleanup(m.Close)

	extConn := &fakeConn{}
	m.AttachExtension("ext-1", extConn)

	if err := m.HandleExtensionMessage("ext-1", []byte(`{"type":"targets","targets":[{"id":"tab-1","title":"T"}]}`)); err != nil {
		t.Fatalf("targets message error: %v", err)
	}
	if err := m.HandleExtensionMessage("ext-1", []byte(`{"type":"attach","targetId":"tab-1"}`)); err != nil {
		t.Fatalf("attach error: %v", err)
	}

	extConn.onWrite = func(v any) {
		req, ok := v.(requestEnvelope)
		if !ok {
			return
		}
		go func() {
			_ = m.HandleExtensionMessage("ext-1", []byte(`{"type":"response","requestId":"`+req.RequestID+`","result":{"ok":true}}`))
		}()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	result, err := m.SendCommand(ctx, "tab-1", "Page.navigate", map[string]any{"url": "https://example.com"}, true)
	if err != nil {
		t.Fatalf("SendCommand() error = %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(result, &decoded); err != nil {
		t.Fatalf("result unmarshal error: %v", err)
	}
	if decoded["ok"] != true {
		t.Fatalf("result ok = %v, want true", decoded["ok"])
	}
}

func TestTargetOwnershipConflict(t *testing.T) {
	m := NewManager(Config{Enabled: true})
	t.Cleanup(m.Close)

	m.AttachExtension("ext-1", &fakeConn{})
	m.AttachExtension("ext-2", &fakeConn{})

	if err := m.HandleExtensionMessage("ext-1", []byte(`{"type":"targets","targets":[{"id":"tab-1"}]}`)); err != nil {
		t.Fatalf("targets ext-1 error: %v", err)
	}
	if err := m.HandleExtensionMessage("ext-2", []byte(`{"type":"targets","targets":[{"id":"tab-1"}]}`)); err != nil {
		t.Fatalf("targets ext-2 error: %v", err)
	}
	if err := m.HandleExtensionMessage("ext-1", []byte(`{"type":"attach","targetId":"tab-1"}`)); err != nil {
		t.Fatalf("first attach error: %v", err)
	}
	if err := m.HandleExtensionMessage("ext-2", []byte(`{"type":"attach","targetId":"tab-1"}`)); !errors.Is(err, ErrTargetOwned) {
		t.Fatalf("second attach error = %v, want ErrTargetOwned", err)
	}
}

func TestEvictStaleExtensionSession(t *testing.T) {
	m := NewManager(Config{Enabled: true, IdleTimeoutSec: 1})
	t.Cleanup(m.Close)

	now := time.Date(2026, 3, 26, 12, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return now }

	m.AttachExtension("ext-1", &fakeConn{})
	if err := m.HandleExtensionMessage("ext-1", []byte(`{"type":"targets","targets":[{"id":"tab-1"}]}`)); err != nil {
		t.Fatalf("targets error: %v", err)
	}
	if err := m.HandleExtensionMessage("ext-1", []byte(`{"type":"attach","targetId":"tab-1"}`)); err != nil {
		t.Fatalf("attach error: %v", err)
	}

	now = now.Add(3 * time.Second)
	m.EvictStaleSessions()

	status := m.Status()
	if status.ConnectedExtensions != 0 {
		t.Fatalf("connected extensions = %d, want 0", status.ConnectedExtensions)
	}
	if status.AttachedTargets != 0 {
		t.Fatalf("attached targets = %d, want 0", status.AttachedTargets)
	}
}
