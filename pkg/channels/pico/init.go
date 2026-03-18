package pico

import (
	"github.com/itsivag/suprclaw/pkg/bus"
	"github.com/itsivag/suprclaw/pkg/channels"
	"github.com/itsivag/suprclaw/pkg/config"
)

func init() {
	channels.RegisterFactory("pico", func(cfg *config.Config, b *bus.MessageBus) (channels.Channel, error) {
		agents := buildAgentSummaries(cfg)
		defaultAgent := findDefaultAgent(cfg)
		return NewPicoChannel(cfg.Channels.Pico, b, agents, defaultAgent)
	})
}

func buildAgentSummaries(cfg *config.Config) []agentSummary {
	if len(cfg.Agents.List) == 0 {
		return []agentSummary{{ID: "main", Name: "Main Agent"}}
	}
	summaries := make([]agentSummary, 0, len(cfg.Agents.List))
	for _, a := range cfg.Agents.List {
		name := a.Name
		if name == "" {
			name = a.ID
		}
		summaries = append(summaries, agentSummary{ID: a.ID, Name: name})
	}
	return summaries
}

func findDefaultAgent(cfg *config.Config) string {
	for _, a := range cfg.Agents.List {
		if a.Default {
			return a.ID
		}
	}
	if len(cfg.Agents.List) > 0 {
		return cfg.Agents.List[0].ID
	}
	return "main"
}
