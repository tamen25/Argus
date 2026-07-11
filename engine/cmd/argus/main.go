// Command argus is the Argus CLI and engine entrypoint.
//
// Phase 0 scope: hello-world skeleton — root command, version, and a minimal
// serve command exposing /healthz. Real subsystems (score, cost, backtest,
// bench, mcp, devtools) land in later phases behind this same entrypoint.
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"
)

// version is injected at build time via -ldflags "-X main.version=v0.x.y".
var version = "dev"

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "argus:", err)
		os.Exit(1)
	}
}

// newRootCmd builds the command tree. Constructed per call (not package-level
// state) so tests can execute commands in isolation.
func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "argus",
		Short:         "Argus — CI for reliability: score, price, backtest, and prove your observability",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(newVersionCmd(), newServeCmd())
	return root
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the argus version",
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprintln(cmd.OutOrStdout(), "argus", version)
			return err
		},
	}
}

func newServeCmd() *cobra.Command {
	var addr string
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the argus engine (Phase 0: health endpoint only)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return serve(cmd.Context(), addr)
		},
	}
	cmd.Flags().StringVar(&addr, "addr", ":8080", "listen address")
	return cmd
}

// serve runs a minimal HTTP server until ctx is cancelled or SIGINT/SIGTERM.
func serve(ctx context.Context, addr string) error {
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})

	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return err
		}
		// ListenAndServe returns ErrServerClosed after a clean Shutdown; that
		// is the expected path, not an error.
		if err := <-errCh; !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}
}
