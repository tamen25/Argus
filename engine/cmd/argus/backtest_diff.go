package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/tamen25/Argus/engine/internal/backtest"
)

// errBacktestRegression marks the CI-gate failure: exit non-zero when the
// proposed rule set loses detections or blows a budget.
var errBacktestRegression = errors.New("backtest regression")

type backtestDiffOptions struct {
	rulesA, rulesB []string
	sloA, sloB     string
	incidents      string
	mimirURL       string
	mimirTenant    string
	from, to       string
	step           time.Duration
	grace          time.Duration
	probeExpr      string
	synthesize     bool
	maxTTD         time.Duration
	maxPages       float64
}

func newBacktestDiffCmd() *cobra.Command {
	opts := &backtestDiffOptions{}
	cmd := &cobra.Command{
		Use:   "diff",
		Short: "Compare two rule sets over the same history — the CI regression gate",
		Long: `Replays rule set A (current) and rule set B (proposed) over the same
covered window and diffs the verdicts: an incident is detected by a set if
any of its rules fired for it. Losing a detection always fails; TTD
regressions beyond --max-ttd-regression and a pages/week total above
--max-pages-week fail too. Exit code is non-zero on regression — wire it
straight into CI.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			d, err := runBacktestDiff(cmd.Context(), opts)
			if err != nil {
				return err
			}
			cmd.Print(backtest.RenderDiffMarkdown(d))
			if d.Regression {
				return fmt.Errorf("%w: %d reason(s)", errBacktestRegression, len(d.Reasons))
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringSliceVar(&opts.rulesA, "rules-a", nil, "current rule file(s)")
	f.StringSliceVar(&opts.rulesB, "rules-b", nil, "proposed rule file(s)")
	f.StringVar(&opts.sloA, "slo-a", "", "current SLO policy file (optional)")
	f.StringVar(&opts.sloB, "slo-b", "", "proposed SLO policy file (optional)")
	f.StringVar(&opts.incidents, "incidents", "incidents.yaml", "incident registry (ground truth)")
	f.StringVar(&opts.mimirURL, "mimir-url", "", "Mimir base URL (instant-query API)")
	f.StringVar(&opts.mimirTenant, "mimir-tenant", "", "Mimir X-Scope-OrgID")
	f.StringVar(&opts.from, "from", "", "window start, RFC3339")
	f.StringVar(&opts.to, "to", "", "window end, RFC3339")
	f.DurationVar(&opts.step, "step", time.Minute, "evaluation step")
	f.DurationVar(&opts.grace, "grace", 5*time.Minute, "incident attribution margin")
	f.StringVar(&opts.probeExpr, "probe-expr", "count(target_info)", "presence-probe expression")
	f.BoolVar(&opts.synthesize, "synthesize", false, "inline defined recording rules")
	f.DurationVar(&opts.maxTTD, "max-ttd-regression", 0, "fail when any incident's TTD worsens by more than this (0 disables)")
	f.Float64Var(&opts.maxPages, "max-pages-week", 0, "fail when set B's total pages/week exceeds this (0 disables)")
	_ = cmd.MarkFlagRequired("rules-a")
	_ = cmd.MarkFlagRequired("rules-b")
	_ = cmd.MarkFlagRequired("mimir-url")
	_ = cmd.MarkFlagRequired("from")
	_ = cmd.MarkFlagRequired("to")
	return cmd
}

func runBacktestDiff(ctx context.Context, opts *backtestDiffOptions) (backtest.Diff, error) {
	base := backtestRunOptions{
		incidents: opts.incidents, mimirURL: opts.mimirURL, mimirTenant: opts.mimirTenant,
		from: opts.from, to: opts.to, step: opts.step, grace: opts.grace,
		probeExpr: opts.probeExpr, synthesize: opts.synthesize,
	}

	a := base
	a.rules, a.slo = opts.rulesA, opts.sloA
	repA, err := runBacktestRun(ctx, &a)
	if err != nil {
		return backtest.Diff{}, fmt.Errorf("rule set A: %w", err)
	}

	b := base
	b.rules, b.slo = opts.rulesB, opts.sloB
	repB, err := runBacktestRun(ctx, &b)
	if err != nil {
		return backtest.Diff{}, fmt.Errorf("rule set B: %w", err)
	}

	return backtest.DiffReports(repA, repB, backtest.DiffOptions{
		MaxTTDRegression: opts.maxTTD,
		MaxPagesPerWeek:  opts.maxPages,
	})
}
