package commands

import "context"

func compactCommand() Definition {
	return Definition{
		Name:        "compact",
		Description: "Compact the current conversation",
		Usage:       "/compact",
		Handler: func(_ context.Context, req Request, rt *Runtime) error {
			if rt == nil || rt.CompactSession == nil {
				return req.Reply(unavailableMsg)
			}
			if err := rt.CompactSession(); err != nil {
				return req.Reply("Failed to compact conversation: " + err.Error())
			}
			return req.Reply("Conversation compacted!")
		},
	}
}
