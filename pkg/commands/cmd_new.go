package commands

import "context"

func newCommand() Definition {
	return Definition{
		Name:        "new",
		Description: "Start a new conversation",
		Usage:       "/new",
		Handler: func(_ context.Context, req Request, rt *Runtime) error {
			if rt == nil || rt.NewSession == nil {
				return req.Reply(unavailableMsg)
			}
			if err := rt.NewSession(); err != nil {
				return req.Reply("Failed to start a new conversation: " + err.Error())
			}
			return req.Reply("Started a new conversation.")
		},
	}
}
