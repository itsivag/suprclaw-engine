package browserrelay

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/itsivag/suprclaw/cmd/suprclaw/internal"
)

const defaultAPIURL = "http://127.0.0.1:18800"

func NewBrowserRelayCommand() *cobra.Command {
	var apiURL string

	cmd := &cobra.Command{
		Use:     "browser-relay",
		Aliases: []string{"br"},
		Short:   "Manage browser relay setup and runtime status",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}

	cmd.PersistentFlags().StringVar(&apiURL, "api-url", defaultAPIURL, "Launcher API base URL")
	cmd.AddCommand(
		newStatusCommand(func() string { return apiURL }),
		newSetupCommand(func() string { return apiURL }),
		newTokenCommand(func() string { return apiURL }),
		newTargetsCommand(func() string { return apiURL }),
	)

	return cmd
}

func doRequest(method, baseURL, path, token string, body any) ([]byte, int, error) {
	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return nil, 0, err
		}
		reader = bytes.NewReader(payload)
	}

	url := strings.TrimRight(baseURL, "/") + path
	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		return nil, 0, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if strings.TrimSpace(token) != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	client := &http.Client{Timeout: 12 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, err
	}
	if resp.StatusCode >= 400 {
		return data, resp.StatusCode, fmt.Errorf("request failed: %s", resp.Status)
	}
	return data, resp.StatusCode, nil
}

func currentRelayToken() string {
	cfg, err := internal.LoadConfig()
	if err != nil || cfg == nil {
		return ""
	}
	return strings.TrimSpace(cfg.Tools.BrowserRelay.Token)
}

func printJSON(data []byte) error {
	var out bytes.Buffer
	if err := json.Indent(&out, data, "", "  "); err != nil {
		return err
	}
	fmt.Println(out.String())
	return nil
}
