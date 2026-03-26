package browserrelay

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newSetupCommand(apiURL func() string) *cobra.Command {
	return &cobra.Command{
		Use:   "setup",
		Short: "Enable and bootstrap browser relay token + URLs",
		RunE: func(_ *cobra.Command, _ []string) error {
			data, _, err := doRequest("POST", apiURL(), "/api/browser-relay/setup", currentRelayToken(), map[string]any{})
			if err != nil {
				return fmt.Errorf("browser relay setup failed: %w", err)
			}
			return printJSON(data)
		},
	}
}
