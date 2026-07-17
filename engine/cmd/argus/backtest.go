package main

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/tamen25/Argus/engine/internal/backtest"
	"github.com/tamen25/Argus/engine/internal/ingest/mimir"
)

type backtestReplayOptions struct {
	rules       []string
	mimirURL    string
	mimirTenant string
	from, to    string
	step        time.Duration
	probeExpr   string
	synthesize  bool
}

func newBacktestCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "backtest",
		Short: "Replay alert rules against historical Mimir data (Phase 3)",
	}
	cmd.AddCommand(newBacktestReplayCmd())
	return cmd
}

func newBacktestReplayCmd() *cobra.Command {
	opts := &backtestReplayOptions{}
	cmd := &cobra.Command{
		Use:   "replay",
		Short: "SPIKE: step alert rules through a historical window, reconstructing for:-state",
		Long: `Fidelity-spike harness (docs/backtest-fidelity.md): loads Prometheus/Mimir
rule files, maps telemetry presence over [--from, --to], and steps each alert
rule through the covered segments reconstructing per-series for:-state.

Replay is NOT re-execution — the report footer lists every fidelity caveat
that applied (coverage, synthesized recording rules, external dependencies).
Numbers without their caveats are not Argus output.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out, err := runBacktestReplay(cmd.Context(), opts)
			if err != nil {
				return err
			}
			cmd.Print(out)
			return nil
		},
	}
	f := cmd.Flags()
	f.StringSliceVar(&opts.rules, "rules", nil, "rule file(s), Prometheus/Mimir ruler format (repeatable)")
	f.StringVar(&opts.mimirURL, "mimir-url", "", "Mimir base URL (instant-query API)")
	f.StringVar(&opts.mimirTenant, "mimir-tenant", "", "Mimir X-Scope-OrgID")
	f.StringVar(&opts.from, "from", "", "window start, RFC3339")
	f.StringVar(&opts.to, "to", "", "window end, RFC3339")
	f.DurationVar(&opts.step, "step", time.Minute, "evaluation step (live ruler interval for full fidelity)")
	f.StringVar(&opts.probeExpr, "probe-expr", "count(target_info)", "presence-probe expression for coverage mapping")
	f.BoolVar(&opts.synthesize, "synthesize", false, "inline defined recording rules (replay history where they never ran)")
	_ = cmd.MarkFlagRequired("rules")
	_ = cmd.MarkFlagRequired("mimir-url")
	_ = cmd.MarkFlagRequired("from")
	_ = cmd.MarkFlagRequired("to")
	return cmd
}

func runBacktestReplay(ctx context.Context, opts *backtestReplayOptions) (string, error) {
	from, err := time.Parse(time.RFC3339, opts.from)
	if err != nil {
		return "", fmt.Errorf("--from: %w", err)
	}
	to, err := time.Parse(time.RFC3339, opts.to)
	if err != nil {
		return "", fmt.Errorf("--to: %w", err)
	}
	if !to.After(from) {
		return "", fmt.Errorf("--to must be after --from")
	}

	rs, err := backtest.LoadRuleFiles(opts.rules...)
	if err != nil {
		return "", err
	}

	client := mimir.New(opts.mimirURL, opts.mimirTenant)
	eval := mimir.EvalSource{Client: client}
	probe := mimir.PresenceSource{Client: client, Expr: opts.probeExpr}

	segs, err := backtest.Segments(ctx, probe, from, to, opts.step)
	if err != nil {
		return "", fmt.Errorf("presence mapping: %w", err)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# Backtest replay (fidelity spike)\n\n")
	fmt.Fprintf(&b, "- Window: %s → %s · step %s\n", from.Format(time.RFC3339), to.Format(time.RFC3339), opts.step)

	var caveats []string
	var covered time.Duration
	for _, s := range segs {
		covered += s.End.Sub(s.Start)
	}
	fmt.Fprintf(&b, "- Coverage: %s of %s calendar window across %d segment(s)\n\n", covered, to.Sub(from), len(segs))
	if covered < to.Sub(from) {
		caveats = append(caveats, fmt.Sprintf("telemetry covers %s of the %s window — verdicts apply to covered segments only", covered, to.Sub(from)))
	}
	if len(segs) == 0 {
		caveats = append(caveats, "no telemetry in the window: nothing was evaluated")
	}

	deps := backtest.Dependencies(rs)
	for _, g := range rs.Groups {
		for _, r := range g.Rules {
			if !r.Alert {
				continue
			}
			expr := r.Expr
			if opts.synthesize {
				synth, synthCaveats, err := backtest.Synthesize(rs, r.Expr)
				if err != nil {
					fmt.Fprintf(&b, "## %s — NOT REPLAYED\n\n%v\n\n", r.Name, err)
					caveats = append(caveats, fmt.Sprintf("%s skipped: %v", r.Name, err))
					continue
				}
				expr = synth
				caveats = append(caveats, synthCaveats...)
			} else {
				for _, d := range deps[r.Name] {
					if d.Kind == backtest.DepExternalRecording {
						caveats = append(caveats, fmt.Sprintf("%s reads external recording rule %q — results are empty unless it ran historically (try --synthesize for defined rules)", r.Name, d.Series))
					}
				}
			}

			rule := r
			rule.Expr = expr
			fmt.Fprintf(&b, "## %s (for: %s)\n\n", r.Name, r.For)
			total := 0
			for _, seg := range segs {
				firings, err := backtest.Replay(ctx, eval, rule, seg, opts.step)
				if err != nil {
					return "", fmt.Errorf("replaying %s: %w", r.Name, err)
				}
				for _, f := range firings {
					total++
					resolved := "unresolved at segment end"
					if !f.ResolvedAt.IsZero() {
						resolved = "resolved " + f.ResolvedAt.Format(time.RFC3339)
					}
					fmt.Fprintf(&b, "- %s · active %s · fired %s · %s\n",
						f.Series, f.ActiveAt.Format(time.RFC3339), f.FiredAt.Format(time.RFC3339), resolved)
				}
			}
			if total == 0 {
				fmt.Fprintf(&b, "- no firings in covered segments\n")
			}
			fmt.Fprintf(&b, "\n")
		}
	}

	fmt.Fprintf(&b, "## Fidelity caveats\n\n")
	caveats = append(caveats, "replay steps instant queries through time — staleness, lookback-delta, and ruler-alignment semantics differ from live evaluation (docs/backtest-fidelity.md)")
	sort.Strings(caveats)
	for _, c := range dedupe(caveats) {
		fmt.Fprintf(&b, "- %s\n", c)
	}
	return b.String(), nil
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	out := in[:0]
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
