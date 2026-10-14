// Command argus is the Argus CLI and engine entrypoint: `serve` runs the
// engine; `score`, `cost`, `backtest`, `bench`, `remediate`, `rules`, `mcp`
// and `devtools` are the command-line tools.
package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// version is injected at build time via -ldflags "-X main.version=v0.x.y".
var version = "dev"

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "argus:", err)
		os.Exit(exitCode(err))
	}
}

// exitCode is 2 when a CI gate tripped (--fail-below-score,
// --fail-over-monthly, a backtest diff regression): the run worked and the
// answer is "no". Anything else is 1, an error. A pipeline can then tell a
// regression from a broken run, and retry only the second.
func exitCode(err error) int {
	for _, gate := range []error{errBelowThreshold, errOverBudget, errBacktestRegression} {
		if errors.Is(err, gate) {
			return 2
		}
	}
	return 1
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
	root.AddCommand(newVersionCmd(), newServeCmd(), newScoreCmd(), newRulesCmd(), newRemediateCmd(), newCostCmd(), newBacktestCmd(), newBenchCmd(), newMCPCmd(), newDevtoolsCmd())
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
