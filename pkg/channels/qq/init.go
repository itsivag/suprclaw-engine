package qq

import (
	"github.com/itsivag/suprclaw/pkg/bus"
	"github.com/itsivag/suprclaw/pkg/channels"
	"github.com/itsivag/suprclaw/pkg/config"
)

func init() {
	channels.RegisterFactory("qq", func(cfg *config.Config, b *bus.MessageBus) (channels.Channel, error) {
		return NewQQChannel(cfg.Channels.QQ, b)
	})
}
