package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/tamen25/Argus/engine/internal/synthhist"
)

func newDevtoolsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "devtools",
		Short: "Development and demo utilities (not product features)",
	}
	cmd.AddCommand(newSynthHistoryCmd())
	return cmd
}

func newSynthHistoryCmd() *cobra.Command {
	var specPath, outDir string
	cmd := &cobra.Command{
		Use:   "synth-history",
		Short: "Fabricate multi-week metric history with embedded incident signatures",
		Long: `Generates deterministic synthetic telemetry as real TSDB blocks plus a
matching incidents.yaml — ground truth included. Real dev history accumulates
in short gappy sessions; demos and CI need continuous windows with known
answers (docs/backtest-fidelity.md).

The blocks land in <out>/blocks; push them into a dev Mimir bucket with
mimirtool or mc, or open them directly with any TSDB reader. This is a dev
tool: it writes local files only.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			spec, err := synthhist.LoadSpec(specPath)
			if err != nil {
				return err
			}
			if err := synthhist.Generate(cmd.Context(), spec, outDir); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "synthetic history written: %s/blocks + %s/incidents.yaml (%d services, %d incidents, %s → %s)\n",
				outDir, outDir, len(spec.Services), len(spec.Incidents), spec.From.Format("2006-01-02"), spec.To.Format("2006-01-02"))
			return nil
		},
	}
	cmd.Flags().StringVar(&specPath, "spec", "", "synth spec YAML (argus.synth/v1)")
	cmd.Flags().StringVar(&outDir, "out", "synth-history", "output directory")
	_ = cmd.MarkFlagRequired("spec")
	return cmd
}
