package api

import (
	"testing"

	"github.com/itsivag/suprclaw/pkg/config"
)

func TestNormalizeBrowserRelayConfigDefaultsEngineMode(t *testing.T) {
	got := normalizeBrowserRelayConfig(config.BrowserRelayConfig{})
	if got.EngineMode != "hybrid" {
		t.Fatalf("EngineMode = %q, want %q", got.EngineMode, "hybrid")
	}
	if got.AgentBrowserBinary == "" {
		t.Fatal("AgentBrowserBinary is empty, want non-empty default")
	}
	if got.AgentBrowserMaxSessions <= 0 {
		t.Fatalf("AgentBrowserMaxSessions = %d, want > 0", got.AgentBrowserMaxSessions)
	}
	if got.AgentBrowserIdleTimeoutSec <= 0 {
		t.Fatalf("AgentBrowserIdleTimeoutSec = %d, want > 0", got.AgentBrowserIdleTimeoutSec)
	}

	got2 := normalizeBrowserRelayConfig(config.BrowserRelayConfig{
		EngineMode:          "hybrid",
		AgentBrowserEnabled: true,
	})
	if !got2.AgentBrowserEnabled {
		t.Fatal("AgentBrowserEnabled should remain true when explicitly configured")
	}
}
