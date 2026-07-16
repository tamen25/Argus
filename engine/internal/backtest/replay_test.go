package backtest_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/tamen25/Argus/engine/internal/backtest"
)

// scriptedEvaluator returns pre-scripted series values per step time — the
// fake for the EvalQuerier port (a Mimir adapter implements it with instant
// queries later).
type scriptedEvaluator struct {
	// values[expr][unix] = series → value
	values map[string]map[int64]map[string]float64
}

func (s *scriptedEvaluator) Eval(_ context.Context, expr string, t time.Time) (map[string]float64, error) {
	byTime, ok := s.values[expr]
	if !ok {
		return nil, fmt.Errorf("unscripted expr %q", expr)
	}
	return byTime[t.Unix()], nil
}

// Replay steps an alert rule through a segment tracking for:-state per
// series: pending once the condition holds, firing once it has held for the
// rule's `for:` duration, resolved when it stops holding.
func TestReplayTracksForState(t *testing.T) {
	t0 := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	step := time.Minute
	at := func(m int) int64 { return t0.Add(time.Duration(m) * step).Unix() }

	expr := `errors_ratio > 0.05`
	vals := map[int64]map[string]float64{}
	// condition true from minute 2 through minute 9, on one series
	for m := 2; m <= 9; m++ {
		vals[at(m)] = map[string]float64{`{service="ad"}`: 0.08}
	}
	q := &scriptedEvaluator{values: map[string]map[int64]map[string]float64{expr: vals}}

	rule := backtest.Rule{Name: "HighErr", Alert: true, Expr: expr, For: 5 * time.Minute}
	seg := backtest.Segment{Start: t0, End: t0.Add(12 * step)}

	firings, err := backtest.Replay(context.Background(), q, rule, seg, step)
	if err != nil {
		t.Fatal(err)
	}
	if len(firings) != 1 {
		t.Fatalf("firings = %+v, want exactly 1", firings)
	}
	f := firings[0]
	// pending at minute 2, for: 5m satisfied at minute 7
	if !f.ActiveAt.Equal(t0.Add(2 * step)) {
		t.Errorf("ActiveAt = %v, want minute 2", f.ActiveAt)
	}
	if !f.FiredAt.Equal(t0.Add(7 * step)) {
		t.Errorf("FiredAt = %v, want minute 7", f.FiredAt)
	}
	// condition last true at minute 9 → resolved at the minute-10 step
	if !f.ResolvedAt.Equal(t0.Add(10 * step)) {
		t.Errorf("ResolvedAt = %v, want minute 10", f.ResolvedAt)
	}
}

// A blip shorter than for: must never fire — the whole point of the clause.
func TestReplayShortBlipDoesNotFire(t *testing.T) {
	t0 := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	step := time.Minute
	expr := `errors_ratio > 0.05`
	vals := map[int64]map[string]float64{
		t0.Add(2 * step).Unix(): {`{service="ad"}`: 0.9},
		t0.Add(3 * step).Unix(): {`{service="ad"}`: 0.9},
	}
	q := &scriptedEvaluator{values: map[string]map[int64]map[string]float64{expr: vals}}
	rule := backtest.Rule{Name: "HighErr", Alert: true, Expr: expr, For: 5 * time.Minute}

	firings, err := backtest.Replay(context.Background(), q, rule,
		backtest.Segment{Start: t0, End: t0.Add(10 * step)}, step)
	if err != nil {
		t.Fatal(err)
	}
	if len(firings) != 0 {
		t.Errorf("firings = %+v, want none for a 2m blip under for:5m", firings)
	}
}

// for: 0 fires on the first true evaluation — the twin used to measure
// divergence on the live cluster.
func TestReplayForZeroFiresImmediately(t *testing.T) {
	t0 := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	step := time.Minute
	expr := `errors_ratio > 0.05`
	vals := map[int64]map[string]float64{t0.Add(2 * step).Unix(): {`{service="ad"}`: 0.9}}
	q := &scriptedEvaluator{values: map[string]map[int64]map[string]float64{expr: vals}}
	rule := backtest.Rule{Name: "Instant", Alert: true, Expr: expr}

	firings, err := backtest.Replay(context.Background(), q, rule,
		backtest.Segment{Start: t0, End: t0.Add(5 * step)}, step)
	if err != nil {
		t.Fatal(err)
	}
	if len(firings) != 1 || !firings[0].FiredAt.Equal(t0.Add(2*step)) {
		t.Errorf("firings = %+v, want one firing at minute 2", firings)
	}
}

// Two series under one rule fire independently.
func TestReplayPerSeriesState(t *testing.T) {
	t0 := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	step := time.Minute
	expr := `errors_ratio > 0.05`
	vals := map[int64]map[string]float64{}
	for m := 1; m <= 8; m++ {
		v := map[string]float64{`{service="ad"}`: 0.9}
		if m >= 4 {
			v[`{service="cart"}`] = 0.9
		}
		vals[t0.Add(time.Duration(m)*step).Unix()] = v
	}
	q := &scriptedEvaluator{values: map[string]map[int64]map[string]float64{expr: vals}}
	rule := backtest.Rule{Name: "HighErr", Alert: true, Expr: expr, For: 2 * time.Minute}

	firings, err := backtest.Replay(context.Background(), q, rule,
		backtest.Segment{Start: t0, End: t0.Add(10 * step)}, step)
	if err != nil {
		t.Fatal(err)
	}
	if len(firings) != 2 {
		t.Fatalf("firings = %+v, want 2 (one per series)", firings)
	}
	if !firings[0].FiredAt.Equal(t0.Add(3*step)) || !firings[1].FiredAt.Equal(t0.Add(6*step)) {
		t.Errorf("fired at %v and %v, want minutes 3 and 6", firings[0].FiredAt, firings[1].FiredAt)
	}
}
