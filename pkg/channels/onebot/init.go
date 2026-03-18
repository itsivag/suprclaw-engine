package onebot

import (
	"github.com/sipeed/suprclaw/pkg/bus"
	"github.com/sipeed/suprclaw/pkg/channels"
	"github.com/sipeed/suprclaw/pkg/config"
)

func init() {
	channels.RegisterFactory("onebot", func(cfg *config.Config, b *bus.MessageBus) (channels.Channel, error) {
		return NewOneBotChannel(cfg.Channels.OneBot, b)
	})
}
