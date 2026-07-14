// Command argus is the Argus CLI and engine entrypoint.
//
// Phase 1 scope: `argus score` (stream + poller evaluation, reports, CI gate)
// and `argus serve` (OTLP receiver + Prometheus score export). Later phases
// add cost, backtest, bench, mcp, and devtools behind this same entrypoint.
package main

import (
	"fmt"
	"os"

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
	root.AddCommand(newVersionCmd(), newServeCmd(), newScoreCmd(), newRulesCmd(), newRemediateCmd())
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
