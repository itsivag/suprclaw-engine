package commands

import (
	"context"
	"fmt"
)

func switchCommand() Definition {
	return Definition{
		Name:        "switch",
		Description: "Switch runtime settings",
		SubCommands: []SubCommand{
			{
				Name:        "model",
				Description: "Switch to a different model",
				ArgsUsage:   "to <name>",
				Handler: func(_ context.Context, req Request, rt *Runtime) error {
					if rt == nil || rt.SwitchModel == nil {
						return req.Reply(unavailableMsg)
					}
					// Parse: /switch model to <value>
					value := nthToken(req.Text, 3) // tokens: [/switch, model, to, <value>]
					if nthToken(req.Text, 2) != "to" || value == "" {
						return req.Reply("Usage: /switch model to <name>")
					}
					oldModel, err := rt.SwitchModel(value)
					if err != nil {
						return req.Reply(err.Error())
					}
					return req.Reply(fmt.Sprintf("Switched model from %s to %s", oldModel, value))
				},
			},
			{
				Name:        "reasoning",
				Description: "Switch reasoning level",
				ArgsUsage:   "to <off|low|medium|high|xhigh|adaptive>",
				Handler: func(_ context.Context, req Request, rt *Runtime) error {
					if rt == nil || rt.SwitchReasoning == nil {
						return req.Reply(unavailableMsg)
					}
					value := nthToken(req.Text, 3) // tokens: [/switch, reasoning, to, <value>]
					if nthToken(req.Text, 2) != "to" || value == "" {
						return req.Reply("Usage: /switch reasoning to <off|low|medium|high|xhigh|adaptive>")
					}
					oldReasoning, err := rt.SwitchReasoning(value)
					if err != nil {
						return req.Reply(err.Error())
					}
					return req.Reply(fmt.Sprintf("Switched reasoning from %s to %s", oldReasoning, value))
				},
			},
			{
				Name:        "channel",
				Description: "Moved to /check channel",
				Handler: func(_ context.Context, req Request, _ *Runtime) error {
					return req.Reply("This command has moved. Please use: /check channel <name>")
				},
			},
		},
	}
}
