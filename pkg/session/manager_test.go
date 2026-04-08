package session

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/itsivag/suprclaw/pkg/providers"
)

func TestSanitizeFilename(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"simple", "simple"},
		{"telegram:123456", "telegram_123456"},
		{"discord:987654321", "discord_987654321"},
		{"slack:C01234", "slack_C01234"},
		{"no-colons-here", "no-colons-here"},
		{"multiple:colons:here", "multiple_colons_here"},
		{"agent:main:telegram:group:-1003822706455/12", "agent_main_telegram_group_-1003822706455_12"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := sanitizeFilename(tt.input)
			if got != tt.expected {
				t.Errorf("sanitizeFilename(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestSave_WithColonInKey(t *testing.T) {
	tmpDir := t.TempDir()
	sm := NewSessionManager(tmpDir)

	// Create a session with a key containing colon (typical channel session key).
	key := "telegram:123456"
	sm.GetOrCreate(key)
	sm.AddMessage(key, "user", "hello")

	// Save should succeed even though the key contains ':'
	if err := sm.Save(key); err != nil {
		t.Fatalf("Save(%q) failed: %v", key, err)
	}

	// The file on disk should use sanitized name.
	expectedFile := filepath.Join(tmpDir, "telegram_123456.json")
	if _, err := os.Stat(expectedFile); os.IsNotExist(err) {
		t.Fatalf("expected session file %s to exist", expectedFile)
	}

	// Load into a fresh manager and verify the session round-trips.
	sm2 := NewSessionManager(tmpDir)
	history := sm2.GetHistory(key)
	if len(history) != 1 {
		t.Fatalf("expected 1 message after reload, got %d", len(history))
	}
	if history[0].Content != "hello" {
		t.Errorf("expected message content %q, got %q", "hello", history[0].Content)
	}
}

func TestSave_RoundTripsMessageUsage(t *testing.T) {
	tmpDir := t.TempDir()
	sm := NewSessionManager(tmpDir)

	key := "usage-roundtrip"
	sm.GetOrCreate(key)
	sm.AddFullMessage(key, providers.Message{
		Role:    "assistant",
		Content: "hello",
		Usage: &providers.UsageInfo{
			PromptTokens:     120,
			CompletionTokens: 24,
			TotalTokens:      144,
		},
	})

	if err := sm.Save(key); err != nil {
		t.Fatalf("Save(%q) failed: %v", key, err)
	}

	sm2 := NewSessionManager(tmpDir)
	history := sm2.GetHistory(key)
	if len(history) != 1 {
		t.Fatalf("expected 1 message after reload, got %d", len(history))
	}
	if history[0].Usage == nil {
		t.Fatal("expected usage to round-trip")
	}
	if history[0].Usage.TotalTokens != 144 {
		t.Fatalf("Usage.TotalTokens = %d, want 144", history[0].Usage.TotalTokens)
	}
}

func TestSave_RoundTripsDiscoveredTools(t *testing.T) {
	tmpDir := t.TempDir()
	sm := NewSessionManager(tmpDir)

	key := "discovered-roundtrip"
	sm.GetOrCreate(key)
	sm.SetDiscoveredTools(key, []string{"mcp_b", "mcp_a", "mcp_a", "  "})

	if err := sm.Save(key); err != nil {
		t.Fatalf("Save(%q) failed: %v", key, err)
	}

	sm2 := NewSessionManager(tmpDir)
	got := sm2.GetDiscoveredTools(key)
	if len(got) != 2 {
		t.Fatalf("expected 2 discovered tools after reload, got %d (%v)", len(got), got)
	}
	if got[0] != "mcp_a" || got[1] != "mcp_b" {
		t.Fatalf("expected sorted deduped discovered tools [mcp_a mcp_b], got %v", got)
	}
}

func TestSave_RejectsPathTraversal(t *testing.T) {
	tmpDir := t.TempDir()
	sm := NewSessionManager(tmpDir)

	// Invalid names that must still be rejected.
	badKeys := []string{"", ".", ".."}
	for _, key := range badKeys {
		sm.GetOrCreate(key)
		if err := sm.Save(key); err == nil {
			t.Errorf("Save(%q) should have failed but didn't", key)
		}
	}

	// Keys containing path separators are sanitized (no subdirs created).
	sm.GetOrCreate("foo/bar")
	if err := sm.Save("foo/bar"); err != nil {
		t.Fatalf("Save(\"foo/bar\") after sanitize should succeed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(tmpDir, "foo_bar.json")); os.IsNotExist(err) {
		t.Errorf("expected foo_bar.json in storage (sanitized from foo/bar)")
	}
}
