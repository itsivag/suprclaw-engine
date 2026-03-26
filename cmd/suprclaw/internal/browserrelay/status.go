package browserrelay

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newStatusCommand(apiURL func() string) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show browser relay status",
		RunE: func(_ *cobra.Command, _ []string) error {
			data, _, err := doRequest("GET", apiURL(), "/api/browser-relay/status", currentRelayToken(), nil)
			if err != nil {
				return fmt.Errorf("browser relay status failed: %w", err)
			}
			return printJSON(data)
		},
	}
}
