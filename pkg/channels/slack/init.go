package slack

import (
	"github.com/itsivag/suprclaw/pkg/bus"
	"github.com/itsivag/suprclaw/pkg/channels"
	"github.com/itsivag/suprclaw/pkg/config"
)

func init() {
	channels.RegisterFactory("slack", func(cfg *config.Config, b *bus.MessageBus) (channels.Channel, error) {
		return NewSlackChannel(cfg.Channels.Slack, b)
	})
}
