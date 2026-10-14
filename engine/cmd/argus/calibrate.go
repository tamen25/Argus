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

	"github.com/tamen25/Argus/engine/internal/atomicfile"
	"github.com/tamen25/Argus/engine/internal/calibrate"
	"github.com/tamen25/Argus/engine/internal/report"
	"github.com/tamen25/Argus/engine/internal/rules"
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
			if err := writeOutput(cmd, "", []byte(md)); err != nil {
				return err
			}
			// The path notice goes to stderr, out of the proposal: the
			// document must be byte-identical for a given snapshot set.
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

// runCalibrate gathers the evidence (soak aggregates, finding ratios from
// soak reports and, optionally, Postgres history), proposes threshold
// overrides, writes one override file per proposal, and returns the proposal
// document. Disclosures about the evidence's quality travel with it.
func runCalibrate(ctx context.Context, opts *calibrateOptions) (string, error) {
	rs, err := loadRules(opts.rulesDir)
	if err != nil {
		return "", err
	}
	in := calibrate.Input{Rules: rs, Ratios: map[string][]float64{}}
	if in.Aggregates, err = readSoakAggregates(opts.soakDir); err != nil {
		return "", err
	}

	var disclosures []string
	skipped, err := addSoakRatios(opts.soakDir, in.Ratios)
	if err != nil {
		return "", err
	}
	if skipped > 0 {
		disclosures = append(disclosures, fmt.Sprintf("%d report-*.json file(s) in the soak dir are not score reports and were ignored", skipped))
	}
	if opts.storeDSN != "" {
		if err := addStoredRatios(ctx, opts.storeDSN, in.Ratios); err != nil {
			return "", err
		}
	}
	disclosures = append(disclosures, soakContinuity(opts.soakDir)...)

	props := calibrate.Propose(in)
	if err := writeOverrides(opts.outDir, rs, props); err != nil {
		return "", err
	}
	return calibrate.Render(props, disclosures...), nil
}

// readSoakAggregates reads every aggregates-*.json snapshot in the soak
// directory, in name order. There must be at least one.
func readSoakAggregates(soakDir string) ([]rules.AggregateRow, error) {
	files, err := filepath.Glob(filepath.Join(soakDir, "aggregates-*.json"))
	if err != nil || len(files) == 0 {
		return nil, fmt.Errorf("no aggregates-*.json in %q — point --soak-dir at a scripts/soak.sh output directory", soakDir)
	}
	sort.Strings(files)
	var all []rules.AggregateRow
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		var rows []rules.AggregateRow
		if err := json.Unmarshal(data, &rows); err != nil {
			return nil, fmt.Errorf("%s: %w", f, err)
		}
		all = append(all, rows...)
	}
	return all, nil
}

// addSoakRatios adds every finding's ratio from the soak's report-*.json
// files to ratios, by rule ID. A file that is not a score report is skipped
// and counted, so the proposal can say so.
func addSoakRatios(soakDir string, ratios map[string][]float64) (skipped int, err error) {
	files, _ := filepath.Glob(filepath.Join(soakDir, "report-*.json"))
	sort.Strings(files)
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			return skipped, err
		}
		var rep report.Report
		if err := json.Unmarshal(data, &rep); err != nil || rep.Snapshot == nil {
			skipped++
			continue
		}
		for _, s := range rep.Snapshot.Services {
			for _, fd := range s.Findings {
				ratios[fd.RuleID] = append(ratios[fd.RuleID], fd.Stats.Ratio)
			}
		}
	}
	return skipped, nil
}

// addStoredRatios adds the finding ratios persisted in Postgres to ratios.
func addStoredRatios(ctx context.Context, dsn string, ratios map[string][]float64) error {
	pg, err := store.Open(ctx, dsn)
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}
	defer pg.Close()
	hist, err := pg.RuleRatios(ctx)
	if err != nil {
		return fmt.Errorf("store ratios: %w", err)
	}
	for id, vals := range hist {
		ratios[id] = append(ratios[id], vals...)
	}
	return nil
}

// soakContinuity discloses a soak run that was not continuous: a segmented
// soak (daemon outages, engine restarts) under-represents steady state.
func soakContinuity(soakDir string) []string {
	samples, err := soak.ReadMetrics(filepath.Join(soakDir, "metrics.csv"))
	if err != nil {
		return []string{"run continuity not verifiable — no readable metrics.csv in the soak dir"}
	}
	if c := soak.CheckContinuity(samples); c.Segmented() {
		return []string{"evidence from a " + c.String() + " soak run — distributions may under-represent steady state"}
	}
	return nil
}

// writeOverrides writes one override YAML file per proposal, named after the
// rule ID.
func writeOverrides(outDir string, rs []*rules.Rule, props []calibrate.Proposal) error {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	byID := map[string]*rules.Rule{}
	for _, r := range rs {
		byID[r.ID] = r
	}
	for _, p := range props {
		data, err := calibrate.OverrideYAML(byID[p.RuleID], p)
		if err != nil {
			return err
		}
		name := strings.ToLower(p.RuleID) + ".yaml"
		if err := atomicfile.Write(filepath.Join(outDir, name), data, 0o644); err != nil {
			return err
		}
	}
	return nil
}
