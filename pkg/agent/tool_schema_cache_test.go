package agent

import (
	"sync"
	"testing"

	"github.com/itsivag/suprclaw/pkg/providers"
)

func testToolDef(name, desc string) providers.ToolDefinition {
	return providers.ToolDefinition{
		Type: "function",
		Function: providers.ToolFunctionDefinition{
			Name:        name,
			Description: desc,
			Parameters: map[string]any{
				"type": "object",
			},
		},
	}
}

func TestToolSchemaCache_GetOrSet(t *testing.T) {
	cache := NewToolSchemaCache()

	defsA := []providers.ToolDefinition{
		testToolDef("zeta", "z"),
		testToolDef("alpha", "a"),
	}
	defsB := []providers.ToolDefinition{
		testToolDef("alpha", "a"),
		testToolDef("zeta", "z"),
	}

	cachedA, fpA, hitA := cache.GetOrSet("session", "agent", "openai", "gpt-5.4", defsA)
	if hitA {
		t.Fatalf("first cache lookup must be miss")
	}
	if len(cachedA) != 2 {
		t.Fatalf("expected 2 tool defs, got %d", len(cachedA))
	}
	if cachedA[0].Function.Name != "alpha" {
		t.Fatalf("tool defs should be normalized by name")
	}

	cachedB, fpB, hitB := cache.GetOrSet("session", "agent", "openai", "gpt-5.4", defsB)
	if !hitB {
		t.Fatalf("second cache lookup must be hit")
	}
	if fpA != fpB {
		t.Fatalf("fingerprint must be stable for logically identical toolsets")
	}
	if len(cachedB) != 2 || cachedB[0].Function.Name != "alpha" {
		t.Fatalf("cached value should be normalized and complete")
	}
}

func TestToolSchemaCache_InvalidationScopes(t *testing.T) {
	cache := NewToolSchemaCache()

	defsA := []providers.ToolDefinition{testToolDef("alpha", "a")}
	defsB := []providers.ToolDefinition{testToolDef("beta", "b")}

	cache.GetOrSet("s1", "a1", "openai", "gpt-5.4", defsA)
	cache.GetOrSet("s1", "a1", "openai", "gpt-5.4", defsB)
	cache.GetOrSet("s2", "a1", "openai", "gpt-5.4", defsA)
	cache.GetOrSet("s2", "a2", "anthropic", "claude-sonnet-4.6", defsA)

	if got := cache.Len(); got != 4 {
		t.Fatalf("expected 4 cache entries, got %d", got)
	}

	if removed := cache.InvalidateSession("s1"); removed != 2 {
		t.Fatalf("expected 2 session entries removed, got %d", removed)
	}
	if got := cache.Len(); got != 2 {
		t.Fatalf("expected 2 entries after session invalidation, got %d", got)
	}

	if removed := cache.InvalidateAgentProviderModel("a1", "openai", "gpt-5.4"); removed != 1 {
		t.Fatalf("expected 1 entry removed for agent/provider/model scope, got %d", removed)
	}
	if got := cache.Len(); got != 1 {
		t.Fatalf("expected 1 entry after agent/provider/model invalidation, got %d", got)
	}

	if removed := cache.InvalidateAll(); removed != 1 {
		t.Fatalf("expected final global invalidation to remove 1 entry, got %d", removed)
	}
	if got := cache.Len(); got != 0 {
		t.Fatalf("expected empty cache after global invalidation, got %d", got)
	}
}

func TestToolSchemaCache_ConcurrentAccess(t *testing.T) {
	cache := NewToolSchemaCache()
	defs := []providers.ToolDefinition{
		testToolDef("alpha", "a"),
		testToolDef("beta", "b"),
	}

	const goroutines = 32
	const iterations = 80

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				session := "s1"
				if i%2 == 0 {
					session = "s2"
				}
				cache.GetOrSet(session, "agent", "openai", "gpt-5.4", defs)
				if i%10 == 0 {
					cache.InvalidateSession(session)
				}
			}
		}(g)
	}
	wg.Wait()
}
