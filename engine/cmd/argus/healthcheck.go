package main

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/spf13/cobra"
)

// newHealthcheckCmd is `argus healthcheck`: GET a health URL, exit 0 on 200.
// The engine image is distroless (no shell, no curl), so a container health
// check runs the binary itself. Checking the binary runs, as the demo's
// health check did with `argus version`, says nothing about the server.
func newHealthcheckCmd() *cobra.Command {
	var url string
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:   "healthcheck",
		Short: "Check that a running engine answers its health endpoint (for container health checks)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return healthcheck(cmd.Context(), url, timeout)
		},
	}
	cmd.Flags().StringVar(&url, "url", "http://127.0.0.1:8080/healthz", "health endpoint to GET")
	cmd.Flags().DurationVar(&timeout, "timeout", 3*time.Second, "how long to wait for the answer")
	return cmd
}

func healthcheck(ctx context.Context, url string, timeout time.Duration) error {
	if err := positive("timeout", timeout); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s answered HTTP %d", url, resp.StatusCode)
	}
	return nil
}
