package discord

import (
	"github.com/sipeed/suprclaw/pkg/bus"
	"github.com/sipeed/suprclaw/pkg/channels"
	"github.com/sipeed/suprclaw/pkg/config"
)

func init() {
	channels.RegisterFactory("discord", func(cfg *config.Config, b *bus.MessageBus) (channels.Channel, error) {
		return NewDiscordChannel(cfg.Channels.Discord, b)
	})
}
