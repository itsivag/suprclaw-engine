package matrix

import (
	"github.com/sipeed/suprclaw/pkg/bus"
	"github.com/sipeed/suprclaw/pkg/channels"
	"github.com/sipeed/suprclaw/pkg/config"
)

func init() {
	channels.RegisterFactory("matrix", func(cfg *config.Config, b *bus.MessageBus) (channels.Channel, error) {
		return NewMatrixChannel(cfg.Channels.Matrix, b)
	})
}
