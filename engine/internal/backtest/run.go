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
	if in.Grace < 0 {
		return Report{}, fmt.Errorf("grace must not be negative, got %s", in.Grace)
	}
	// Checked here as well as in Segments and Replay, so a bad step fails
	// before the first backend call rather than after the presence sweep.
	if err := checkStepping("step", in.From, in.To, in.Step); err != nil {
		return Report{}, err
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
		Coverage:    totalCoverage(segs),
		Segments:    len(segs),
	}
	if window := in.To.Sub(in.From); rep.Coverage < window {
		rep.Caveats = append(rep.Caveats, fmt.Sprintf("telemetry covers %s of the %s window — verdicts apply to covered segments only", rep.Coverage, window))
	}

	for _, g := range in.Rules.Groups {
		for _, rule := range g.Rules {
			if !rule.Alert {
				continue
			}
			if in.Synthesize {
				expr, caveats, err := Synthesize(in.Rules, rule.Expr)
				if err != nil {
					// One rule that cannot be synthesized is reported, not fatal.
					rep.Caveats = append(rep.Caveats, fmt.Sprintf("%s not replayed: %v", rule.Name, err))
					continue
				}
				rule.Expr = expr // rule is a copy: the caller's rule set is untouched
				rep.Caveats = append(rep.Caveats, caveats...)
			}
			firings, err := replaySegments(ctx, eval, rule, segs, in.Step)
			if err != nil {
				return Report{}, err
			}
			rep.Rules = append(rep.Rules, Score(rule.Name, firings, in.Incidents, segs, ScoreOptions{Grace: in.Grace}))
		}
	}
	rep.Caveats = dedupeCaveats(rep.Caveats)
	return rep, nil
}

// replaySegments replays one rule through every covered segment in turn.
func replaySegments(ctx context.Context, eval EvalQuerier, rule Rule, segs []Segment, step time.Duration) ([]Firing, error) {
	var firings []Firing
	for _, seg := range segs {
		fs, err := Replay(ctx, eval, rule, seg, step)
		if err != nil {
			return nil, fmt.Errorf("replaying %s: %w", rule.Name, err)
		}
		firings = append(firings, fs...)
	}
	return firings, nil
}

// dedupeCaveats drops repeated caveats, keeping first-seen order: every rule
// that inlines the same recording rule adds the same note. It reuses in's
// backing array.
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
