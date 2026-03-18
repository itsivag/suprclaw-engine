package whatsapp

import (
	"github.com/itsivag/suprclaw/pkg/bus"
	"github.com/itsivag/suprclaw/pkg/channels"
	"github.com/itsivag/suprclaw/pkg/config"
)

func init() {
	channels.RegisterFactory("whatsapp", func(cfg *config.Config, b *bus.MessageBus) (channels.Channel, error) {
		return NewWhatsAppChannel(cfg.Channels.WhatsApp, b)
	})
}
