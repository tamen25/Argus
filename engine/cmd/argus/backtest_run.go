package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/tamen25/Argus/engine/internal/backtest"
	"github.com/tamen25/Argus/engine/internal/ingest/mimir"
)

type backtestRunOptions struct {
	rules       []string
	incidents   string
	mimirURL    string
	mimirTenant string
	from, to    string
	step        time.Duration
	grace       time.Duration
	probeExpr   string
	synthesize  bool
	output      string
	outPath     string
}

func newBacktestRunCmd() *cobra.Command {
	opts := &backtestRunOptions{}
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Replay alert rules over history and score them against the incident registry",
		Long: `Loads rule files and incidents.yaml, maps telemetry presence over
[--from, --to], replays every alerting rule through the covered segments, and
scores the firings against the registry: detections with time-to-detection,
missed incidents, unverifiable incidents (no telemetry coverage — never
counted as misses), false positives outside incident windows ± grace,
pages/week over covered time, and flappiness.

Every report carries the fidelity caveats that applied (docs/backtest-fidelity.md).`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			rep, err := runBacktestRun(cmd.Context(), opts)
			if err != nil {
				return err
			}
			var out string
			switch opts.output {
			case "json":
				b, err := backtest.RenderReportJSON(rep)
				if err != nil {
					return err
				}
				out = string(b)
			case "md", "":
				out = backtest.RenderReportMarkdown(rep)
			default:
				return fmt.Errorf("unknown output format %q (want md or json)", opts.output)
			}
			if opts.outPath != "" {
				return os.WriteFile(opts.outPath, []byte(out), 0o644)
			}
			cmd.Print(out)
			return nil
		},
	}
	f := cmd.Flags()
	f.StringSliceVar(&opts.rules, "rules", nil, "rule file(s), Prometheus/Mimir ruler format (repeatable)")
	f.StringVar(&opts.incidents, "incidents", "incidents.yaml", "incident registry (ground truth)")
	f.StringVar(&opts.mimirURL, "mimir-url", "", "Mimir base URL (instant-query API)")
	f.StringVar(&opts.mimirTenant, "mimir-tenant", "", "Mimir X-Scope-OrgID")
	f.StringVar(&opts.from, "from", "", "window start, RFC3339")
	f.StringVar(&opts.to, "to", "", "window end, RFC3339")
	f.DurationVar(&opts.step, "step", time.Minute, "evaluation step (live ruler interval for closest fidelity)")
	f.DurationVar(&opts.grace, "grace", 5*time.Minute, "incident attribution margin: fires within ± grace are late, not false")
	f.StringVar(&opts.probeExpr, "probe-expr", "count(target_info)", "presence-probe expression for coverage mapping")
	f.BoolVar(&opts.synthesize, "synthesize", false, "inline defined recording rules (replay history where they never ran)")
	f.StringVar(&opts.output, "output", "md", "output format: md | json")
	f.StringVar(&opts.outPath, "out", "", "write the report to this file instead of stdout")
	_ = cmd.MarkFlagRequired("rules")
	_ = cmd.MarkFlagRequired("mimir-url")
	_ = cmd.MarkFlagRequired("from")
	_ = cmd.MarkFlagRequired("to")
	return cmd
}

func runBacktestRun(ctx context.Context, opts *backtestRunOptions) (backtest.Report, error) {
	from, err := time.Parse(time.RFC3339, opts.from)
	if err != nil {
		return backtest.Report{}, fmt.Errorf("--from: %w", err)
	}
	to, err := time.Parse(time.RFC3339, opts.to)
	if err != nil {
		return backtest.Report{}, fmt.Errorf("--to: %w", err)
	}
	if !to.After(from) {
		return backtest.Report{}, fmt.Errorf("--to must be after --from")
	}

	rs, err := backtest.LoadRuleFiles(opts.rules...)
	if err != nil {
		return backtest.Report{}, err
	}
	reg, err := backtest.LoadIncidents(opts.incidents)
	if err != nil {
		return backtest.Report{}, err
	}

	client := mimir.New(opts.mimirURL, opts.mimirTenant)
	eval := mimir.EvalSource{Client: client}
	probe := mimir.PresenceSource{Client: client, Expr: opts.probeExpr}

	segs, err := backtest.Segments(ctx, probe, from, to, opts.step)
	if err != nil {
		return backtest.Report{}, fmt.Errorf("presence mapping: %w", err)
	}

	rep := backtest.Report{
		GeneratedAt: time.Now().UTC(),
		From:        from,
		To:          to,
		Step:        opts.step,
		Segments:    len(segs),
	}
	for _, s := range segs {
		rep.Coverage += s.End.Sub(s.Start)
	}
	if rep.Coverage < to.Sub(from) {
		rep.Caveats = append(rep.Caveats, fmt.Sprintf("telemetry covers %s of the %s window — verdicts apply to covered segments only", rep.Coverage, to.Sub(from)))
	}

	for _, g := range rs.Groups {
		for _, r := range g.Rules {
			if !r.Alert {
				continue
			}
			rule := r
			if opts.synthesize {
				synth, synthCaveats, err := backtest.Synthesize(rs, r.Expr)
				if err != nil {
					rep.Caveats = append(rep.Caveats, fmt.Sprintf("%s not replayed: %v", r.Name, err))
					continue
				}
				rule.Expr = synth
				rep.Caveats = append(rep.Caveats, synthCaveats...)
			}
			var firings []backtest.Firing
			for _, seg := range segs {
				fs, err := backtest.Replay(ctx, eval, rule, seg, opts.step)
				if err != nil {
					return backtest.Report{}, fmt.Errorf("replaying %s: %w", r.Name, err)
				}
				firings = append(firings, fs...)
			}
			rep.Rules = append(rep.Rules, backtest.Score(r.Name, firings, reg.Incidents, segs, backtest.ScoreOptions{Grace: opts.grace}))
		}
	}
	return rep, nil
}
