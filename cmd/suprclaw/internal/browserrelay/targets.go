package browserrelay

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newTargetsCommand(apiURL func() string) *cobra.Command {
	return &cobra.Command{
		Use:   "targets",
		Short: "List currently visible relay targets",
		RunE: func(_ *cobra.Command, _ []string) error {
			data, _, err := doRequest("GET", apiURL(), "/api/browser-relay/targets", currentRelayToken(), nil)
			if err != nil {
				return fmt.Errorf("browser relay targets failed: %w", err)
			}
			return printJSON(data)
		},
	}
}
