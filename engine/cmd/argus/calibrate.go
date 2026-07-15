package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tamen25/Argus/engine/internal/calibrate"
	"github.com/tamen25/Argus/engine/internal/report"
	"github.com/tamen25/Argus/engine/internal/rules"
	"github.com/tamen25/Argus/engine/internal/rules/builtin"
	"github.com/tamen25/Argus/engine/internal/soak"
	"github.com/tamen25/Argus/engine/internal/store"
)

func newRulesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rules",
		Short: "Rule set utilities",
	}
	cmd.AddCommand(newCalibrateCmd())
	return cmd
}

func newCalibrateCmd() *cobra.Command {
	opts := &calibrateOptions{}
	cmd := &cobra.Command{
		Use:   "calibrate",
		Short: "Propose evidence-based threshold overrides from soak data (and optionally Postgres history)",
		Long: "Reads accumulated aggregate snapshots and reports, computes robust statistics\n" +
			"(median, MAD, P90/P99 — telemetry is heavy-tailed, no mean/σ) per calibratable\n" +
			"rule, and emits override YAML for human review. Spec-rule criteria are never\n" +
			"modified — only params the spec leaves open and argus-extension params.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			md, err := runCalibrate(cmd.Context(), opts)
			if err != nil {
				return err
			}
			cmd.Print(md)
			// path notice stays out of md: the proposal document must be
			// byte-identical for a given snapshot set
			cmd.PrintErrf("\nOverride files written to %s — review and commit the ones you accept.\n", opts.outDir)
			return nil
		},
	}
	cmd.Flags().StringVar(&opts.soakDir, "soak-dir", "", "soak output directory (scripts/soak.sh) with aggregates-*.json / report-*.json")
	cmd.Flags().StringVar(&opts.rulesDir, "rules", "", "extra rule YAML dir overriding/extending built-ins")
	cmd.Flags().StringVar(&opts.outDir, "out", "calibrated-rules", "directory for proposed override YAML files")
	cmd.Flags().StringVar(&opts.storeDSN, "store-dsn", "", "Postgres DSN: include persisted finding ratios in the evidence")
	_ = cmd.MarkFlagRequired("soak-dir")
	return cmd
}

type calibrateOptions struct {
	soakDir, rulesDir, outDir, storeDSN string
}

func runCalibrate(ctx context.Context, opts *calibrateOptions) (string, error) {
	rs, err := builtin.Load()
	if err != nil {
		return "", err
	}
	if opts.rulesDir != "" {
		custom, err := rules.LoadDir(opts.rulesDir)
		if err != nil {
			return "", err
		}
		rs = rules.Merge(rs, custom)
	}

	in := calibrate.Input{Rules: rs, Ratios: map[string][]float64{}}

	aggFiles, err := filepath.Glob(filepath.Join(opts.soakDir, "aggregates-*.json"))
	if err != nil || len(aggFiles) == 0 {
		return "", fmt.Errorf("no aggregates-*.json in %q — point --soak-dir at a scripts/soak.sh output directory", opts.soakDir)
	}
	sort.Strings(aggFiles)
	for _, f := range aggFiles {
		data, err := os.ReadFile(f)
		if err != nil {
			return "", err
		}
		var rows []rules.AggregateRow
		if err := json.Unmarshal(data, &rows); err != nil {
			return "", fmt.Errorf("%s: %w", f, err)
		}
		in.Aggregates = append(in.Aggregates, rows...)
	}

	repFiles, _ := filepath.Glob(filepath.Join(opts.soakDir, "report-*.json"))
	sort.Strings(repFiles)
	for _, f := range repFiles {
		data, err := os.ReadFile(f)
		if err != nil {
			return "", err
		}
		var rep report.Report
		if err := json.Unmarshal(data, &rep); err != nil || rep.Snapshot == nil {
			continue
		}
		for _, s := range rep.Snapshot.Services {
			for _, fd := range s.Findings {
				in.Ratios[fd.RuleID] = append(in.Ratios[fd.RuleID], fd.Stats.Ratio)
			}
		}
	}

	if opts.storeDSN != "" {
		pg, err := store.Open(ctx, opts.storeDSN)
		if err != nil {
			return "", fmt.Errorf("store: %w", err)
		}
		defer pg.Close()
		hist, err := pg.RuleRatios(ctx)
		if err != nil {
			return "", fmt.Errorf("store ratios: %w", err)
		}
		for id, vals := range hist {
			in.Ratios[id] = append(in.Ratios[id], vals...)
		}
	}

	// Evidence quality travels with the evidence: a segmented soak (daemon
	// outages, engine restarts) under-represents steady state.
	var disclosures []string
	if samples, err := soak.ReadMetrics(filepath.Join(opts.soakDir, "metrics.csv")); err != nil {
		disclosures = append(disclosures, "run continuity not verifiable — no readable metrics.csv in the soak dir")
	} else if c := soak.CheckContinuity(samples); c.Segmented() {
		disclosures = append(disclosures, "evidence from a "+c.String()+" soak run — distributions may under-represent steady state")
	}

	props := calibrate.Propose(in)
	if err := os.MkdirAll(opts.outDir, 0o755); err != nil {
		return "", err
	}
	byID := map[string]*rules.Rule{}
	for _, r := range rs {
		byID[r.ID] = r
	}
	for _, p := range props {
		data, err := calibrate.OverrideYAML(byID[p.RuleID], p)
		if err != nil {
			return "", err
		}
		name := strings.ToLower(p.RuleID) + ".yaml"
		if err := os.WriteFile(filepath.Join(opts.outDir, name), data, 0o644); err != nil {
			return "", err
		}
	}

	return calibrate.Render(props, disclosures...), nil
}
