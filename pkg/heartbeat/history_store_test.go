package heartbeat

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHistoryStore_AppendsEventsAndListsNewestFirst(t *testing.T) {
	workspace := t.TempDir()
	store := NewHistoryStore(workspace)
	if err := store.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(store.Stop)

	EmitHeartbeatEvent(HeartbeatEvent{
		Ts:      1000,
		Status:  StatusSent,
		AgentID: "main",
	})
	EmitHeartbeatEvent(HeartbeatEvent{
		Ts:      2000,
		Status:  StatusSkipped,
		AgentID: "main",
	})

	got := store.List(HistoryFilter{Limit: 10})
	if len(got) != 2 {
		t.Fatalf("len(List) = %d, want 2", len(got))
	}
	if got[0].Ts != 2000 || got[1].Ts != 1000 {
		t.Fatalf("unexpected order: %+v", got)
	}
}

func TestHistoryStore_RetentionPrunesOldest(t *testing.T) {
	workspace := t.TempDir()
	store := NewHistoryStore(workspace)
	store.maxRecords = 2
	if err := store.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(store.Stop)

	EmitHeartbeatEvent(HeartbeatEvent{Ts: 1000, Status: StatusSent, AgentID: "a"})
	EmitHeartbeatEvent(HeartbeatEvent{Ts: 2000, Status: StatusSent, AgentID: "a"})
	EmitHeartbeatEvent(HeartbeatEvent{Ts: 3000, Status: StatusSent, AgentID: "a"})

	got := store.List(HistoryFilter{Limit: 10})
	if len(got) != 2 {
		t.Fatalf("len(List) = %d, want 2", len(got))
	}
	if got[0].Ts != 3000 || got[1].Ts != 2000 {
		t.Fatalf("unexpected retention set: %+v", got)
	}
}

func TestHistoryStore_DeleteAndClearPersist(t *testing.T) {
	workspace := t.TempDir()
	store := NewHistoryStore(workspace)
	if err := store.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(store.Stop)

	EmitHeartbeatEvent(HeartbeatEvent{Ts: 1000, Status: StatusSent, AgentID: "main"})
	EmitHeartbeatEvent(HeartbeatEvent{Ts: 2000, Status: StatusSkipped, AgentID: "main"})

	all := store.List(HistoryFilter{Limit: 10})
	if len(all) != 2 {
		t.Fatalf("len(List) = %d, want 2", len(all))
	}
	deleteID := all[0].ID

	deleted, err := store.Delete(deleteID)
	if err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if !deleted {
		t.Fatal("Delete() = false, want true")
	}

	if _, ok := store.Get(deleteID); ok {
		t.Fatal("deleted record still exists")
	}

	if err := store.Clear(); err != nil {
		t.Fatalf("Clear() error = %v", err)
	}
	if len(store.List(HistoryFilter{Limit: 10})) != 0 {
		t.Fatal("expected empty history after clear")
	}
}

func TestHistoryStore_StartFailsOnMalformedJSONL(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, historyFileName)
	if err := os.WriteFile(path, []byte("{bad json\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	store := NewHistoryStore(workspace)
	if err := store.Start(); err == nil {
		t.Fatal("Start() error = nil, want malformed jsonl error")
	}
}

func TestHistoryStore_ParseFilterHelpers(t *testing.T) {
	if _, err := ParseHeartbeatHistoryLimit("0"); err == nil {
		t.Fatal("expected ParseHeartbeatHistoryLimit(0) to fail")
	}
	if _, err := ParseHeartbeatHistoryTimestamp("bad", "before_ts"); err == nil {
		t.Fatal("expected ParseHeartbeatHistoryTimestamp to fail for non-int")
	}
	if _, err := ParseHeartbeatStatus("invalid"); err == nil {
		t.Fatal("expected ParseHeartbeatStatus to fail for invalid status")
	}
}
