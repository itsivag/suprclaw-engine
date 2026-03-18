// SuprClaw - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 SuprClaw contributors

package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/sipeed/suprclaw/cmd/suprclaw/internal"
	"github.com/sipeed/suprclaw/cmd/suprclaw/internal/agent"
	"github.com/sipeed/suprclaw/cmd/suprclaw/internal/auth"
	"github.com/sipeed/suprclaw/cmd/suprclaw/internal/cron"
	"github.com/sipeed/suprclaw/cmd/suprclaw/internal/gateway"
	"github.com/sipeed/suprclaw/cmd/suprclaw/internal/migrate"
	"github.com/sipeed/suprclaw/cmd/suprclaw/internal/model"
	"github.com/sipeed/suprclaw/cmd/suprclaw/internal/onboard"
	"github.com/sipeed/suprclaw/cmd/suprclaw/internal/skills"
	"github.com/sipeed/suprclaw/cmd/suprclaw/internal/status"
	"github.com/sipeed/suprclaw/cmd/suprclaw/internal/version"
	"github.com/sipeed/suprclaw/pkg/config"
)

func NewSuprclawCommand() *cobra.Command {
	short := fmt.Sprintf("%s suprclaw - Personal AI Assistant v%s\n\n", internal.Logo, config.GetVersion())

	cmd := &cobra.Command{
		Use:     "suprclaw",
		Short:   short,
		Example: "suprclaw version",
	}

	cmd.AddCommand(
		onboard.NewOnboardCommand(),
		agent.NewAgentCommand(),
		auth.NewAuthCommand(),
		gateway.NewGatewayCommand(),
		status.NewStatusCommand(),
		cron.NewCronCommand(),
		migrate.NewMigrateCommand(),
		skills.NewSkillsCommand(),
		model.NewModelCommand(),
		version.NewVersionCommand(),
	)

	return cmd
}

const (
	colorBlue = "\033[1;38;2;62;93;185m"
	colorRed  = "\033[1;38;2;213;70;70m"
	banner    = "\r\n" +
		colorBlue + "███████╗██╗   ██╗██████╗ ██████╗ " + colorRed + " ██████╗██╗      █████╗ ██╗    ██╗\n" +
		colorBlue + "██╔════╝██║   ██║██╔══██╗██╔══██╗" + colorRed + "██╔════╝██║     ██╔══██╗██║    ██║\n" +
		colorBlue + "███████╗██║   ██║██████╔╝██████╔╝" + colorRed + "██║     ██║     ███████║██║ █╗ ██║\n" +
		colorBlue + "╚════██║██║   ██║██╔═══╝ ██╔══██╗" + colorRed + "██║     ██║     ██╔══██║██║███╗██║\n" +
		colorBlue + "███████║╚██████╔╝██║     ██║  ██║" + colorRed + "╚██████╗███████╗██║  ██║╚███╔███╔╝\n" +
		colorBlue + "╚══════╝ ╚═════╝ ╚═╝     ╚═╝  ╚═╝" + colorRed + " ╚═════╝╚══════╝╚═╝  ╚═╝ ╚══╝╚══╝\n" +
		"\033[0m\r\n"
)

func main() {
	fmt.Printf("%s", banner)
	cmd := NewSuprclawCommand()
	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}
