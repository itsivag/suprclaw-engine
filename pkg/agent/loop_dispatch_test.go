package agent

import (
	"testing"

	"github.com/itsivag/suprclaw/pkg/providers"
)

func TestSanitizeDispatchMessages_DropsEmptyAssistantWithoutToolCalls(t *testing.T) {
	messages := []providers.Message{
		{Role: "system", Content: "sys"},
		{Role: "assistant", Content: ""},
		{Role: "user", Content: "hello"},
	}

	got := sanitizeDispatchMessages(messages)
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2", len(got))
	}
	if got[0].Role != "system" || got[1].Role != "user" {
		t.Fatalf("roles = %q, %q; want system, user", got[0].Role, got[1].Role)
	}
}

func TestEnsureNonSystemDispatchMessage_AppendsFallbackWhenSystemOnly(t *testing.T) {
	messages := []providers.Message{
		{Role: "system", Content: "sys"},
	}

	got, injected := ensureNonSystemDispatchMessage(messages, "")
	if !injected {
		t.Fatal("expected injected=true")
	}
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2", len(got))
	}
	if got[1].Role != "user" {
		t.Fatalf("got[1].Role = %q, want user", got[1].Role)
	}
	if got[1].Content == "" {
		t.Fatal("fallback user content must not be empty")
	}
}

func TestEnsureNonSystemDispatchMessage_NoOpWhenNonSystemExists(t *testing.T) {
	messages := []providers.Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "hi"},
	}

	got, injected := ensureNonSystemDispatchMessage(messages, "fallback")
	if injected {
		t.Fatal("expected injected=false")
	}
	if len(got) != len(messages) {
		t.Fatalf("len(got) = %d, want %d", len(got), len(messages))
	}
}

