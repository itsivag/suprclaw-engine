package feishu

import (
	"github.com/sipeed/suprclaw/pkg/bus"
	"github.com/sipeed/suprclaw/pkg/channels"
	"github.com/sipeed/suprclaw/pkg/config"
)

func init() {
	channels.RegisterFactory("feishu", func(cfg *config.Config, b *bus.MessageBus) (channels.Channel, error) {
		return NewFeishuChannel(cfg.Channels.Feishu, b)
	})
}
