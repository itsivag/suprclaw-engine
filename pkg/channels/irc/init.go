package irc

import (
	"github.com/sipeed/suprclaw/pkg/bus"
	"github.com/sipeed/suprclaw/pkg/channels"
	"github.com/sipeed/suprclaw/pkg/config"
)

func init() {
	channels.RegisterFactory("irc", func(cfg *config.Config, b *bus.MessageBus) (channels.Channel, error) {
		if !cfg.Channels.IRC.Enabled {
			return nil, nil
		}
		return NewIRCChannel(cfg.Channels.IRC, b)
	})
}
