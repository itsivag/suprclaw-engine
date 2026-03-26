package browserrelay

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newTokenCommand(apiURL func() string) *cobra.Command {
	var regenerate bool

	cmd := &cobra.Command{
		Use:   "token",
		Short: "Show or regenerate browser relay token",
		RunE: func(_ *cobra.Command, _ []string) error {
			method := "GET"
			var payload any
			if regenerate {
				method = "POST"
				payload = map[string]any{}
			}
			data, _, err := doRequest(method, apiURL(), "/api/browser-relay/token", currentRelayToken(), payload)
			if err != nil {
				return fmt.Errorf("browser relay token request failed: %w", err)
			}
			return printJSON(data)
		},
	}
	cmd.Flags().BoolVar(&regenerate, "regenerate", false, "Regenerate relay token")
	return cmd
}
