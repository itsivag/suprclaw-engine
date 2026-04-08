package agent

import (
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
