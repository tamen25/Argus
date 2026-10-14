package backtest

import (
	"context"
	"sort"
	"time"
)

// EvalQuerier is the replay port: evaluate a PromQL expression at an instant,
// returning matched series (label-set key) → value. The Mimir adapter
// implements it with instant queries; unit tests script it.
type EvalQuerier interface {
	Eval(ctx context.Context, expr string, t time.Time) (map[string]float64, error)
}

// Firing is one would-have-fired interval reconstructed by replay.
type Firing struct {
	Rule       string
	Series     string // label-set key of the series that fired
	ActiveAt   time.Time
	FiredAt    time.Time
	ResolvedAt time.Time // zero if still firing at segment end
}

// Replay steps one alerting rule through a presence segment, reconstructing
// per-series `for:` state the way a live ruler would have evolved it: pending
// on the first true evaluation, firing once continuously true for the rule's
// For duration, resolved on the first false evaluation, or, with
// keep_firing_for, once the condition has been false for that long.
//
// Fidelity caveats (spike, docs/backtest-fidelity.md): step-aligned instant
// queries approximate the live ruler's own evaluation schedule; staleness and
// lookback-delta behavior differ at the margins, and a segment boundary cuts
// pending state (a condition that began before the segment cannot be seen).
// The caller records those caveats on the report.
func Replay(ctx context.Context, q EvalQuerier, rule Rule, seg Segment, step time.Duration) ([]Firing, error) {
	if err := checkStepping("step", seg.Start, seg.End, step); err != nil {
		return nil, err
	}
	type state struct {
		activeAt time.Time
		firedAt  time.Time // zero until for: satisfied
		lastTrue time.Time // the last evaluation the condition held
	}
	active := map[string]*state{}
	var out []Firing

	resolve := func(key string, at time.Time) {
		st := active[key]
		if st.firedAt.IsZero() {
			delete(active, key) // pending that never fired is not a Firing
			return
		}
		out = append(out, Firing{Rule: rule.Name, Series: key, ActiveAt: st.activeAt, FiredAt: st.firedAt, ResolvedAt: at})
		delete(active, key)
	}

	for t := seg.Start; !t.After(seg.End); t = t.Add(step) {
		vals, err := q.Eval(ctx, rule.Expr, t)
		if err != nil {
			return nil, err
		}
		for key := range vals {
			st, ok := active[key]
			if !ok {
				st = &state{activeAt: t}
				active[key] = st
			}
			st.lastTrue = t
			if st.firedAt.IsZero() && t.Sub(st.activeAt) >= rule.For {
				st.firedAt = t
			}
		}
		for key, st := range active {
			if _, still := vals[key]; still {
				continue
			}
			// A firing alert with keep_firing_for stays firing until the
			// condition has been false for that long.
			if !st.firedAt.IsZero() && t.Sub(st.lastTrue) < rule.KeepFiringFor {
				continue
			}
			resolve(key, t)
		}
	}
	// still active at segment end: firing with no resolution in view
	for key, st := range active {
		if !st.firedAt.IsZero() {
			out = append(out, Firing{Rule: rule.Name, Series: key, ActiveAt: st.activeAt, FiredAt: st.firedAt})
		}
	}

	sort.Slice(out, func(i, j int) bool {
		if !out[i].FiredAt.Equal(out[j].FiredAt) {
			return out[i].FiredAt.Before(out[j].FiredAt)
		}
		return out[i].Series < out[j].Series
	})
	return out, nil
}
