package backtest

import (
	"context"
	"fmt"
	"time"
)

// RunInput is everything the backtest pipeline needs that the caller has
// already loaded: the (possibly SLO-expanded) rule set, the incident registry,
// the window, and the replay knobs. Backends stay behind the EvalQuerier and
// InstantQuerier ports (architecture rule 1) — concrete Mimir clients are the
// caller's to build.
type RunInput struct {
	Rules       RuleSet
	Incidents   []Incident
	From, To    time.Time
	Step, Grace time.Duration
	Synthesize  bool
}

// Run is the backtest pipeline end to end: map telemetry presence over the
// window, replay every alerting rule through the covered segments, and score
// the firings against the registry. Deterministic given deterministic ports.
// Shared by `argus backtest run`, `backtest diff`, and the /api/backtest
// endpoint so there is exactly one replay-and-score path.
func Run(ctx context.Context, eval EvalQuerier, probe InstantQuerier, in RunInput) (Report, error) {
	if !in.To.After(in.From) {
		return Report{}, fmt.Errorf("to must be after from")
	}
	segs, err := Segments(ctx, probe, in.From, in.To, in.Step)
	if err != nil {
		return Report{}, fmt.Errorf("presence mapping: %w", err)
	}

	rep := Report{
		GeneratedAt: time.Now().UTC(),
		From:        in.From,
		To:          in.To,
		Step:        in.Step,
		Segments:    len(segs),
	}
	for _, s := range segs {
		rep.Coverage += s.End.Sub(s.Start)
	}
	if rep.Coverage < in.To.Sub(in.From) {
		rep.Caveats = append(rep.Caveats, fmt.Sprintf("telemetry covers %s of the %s window — verdicts apply to covered segments only", rep.Coverage, in.To.Sub(in.From)))
	}

	for _, g := range in.Rules.Groups {
		for _, r := range g.Rules {
			if !r.Alert {
				continue
			}
			rule := r
			if in.Synthesize {
				synth, synthCaveats, err := Synthesize(in.Rules, r.Expr)
				if err != nil {
					rep.Caveats = append(rep.Caveats, fmt.Sprintf("%s not replayed: %v", r.Name, err))
					continue
				}
				rule.Expr = synth
				rep.Caveats = append(rep.Caveats, synthCaveats...)
			}
			var firings []Firing
			for _, seg := range segs {
				fs, err := Replay(ctx, eval, rule, seg, in.Step)
				if err != nil {
					return Report{}, fmt.Errorf("replaying %s: %w", r.Name, err)
				}
				firings = append(firings, fs...)
			}
			rep.Rules = append(rep.Rules, Score(r.Name, firings, in.Incidents, segs, ScoreOptions{Grace: in.Grace}))
		}
	}
	rep.Caveats = dedupeCaveats(rep.Caveats)
	return rep, nil
}

func dedupeCaveats(in []string) []string {
	if len(in) == 0 {
		return in
	}
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
