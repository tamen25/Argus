package backtest_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/tamen25/Argus/engine/internal/backtest"
)

// fakeEval returns a fixed error ratio for a service across a firing window.
type fakeEval struct {
	series           string
	trueFrom, trueTo time.Time
}

func (f fakeEval) Eval(_ context.Context, _ string, t time.Time) (map[string]float64, error) {
	if !t.Before(f.trueFrom) && t.Before(f.trueTo) {
		return map[string]float64{f.series: 0.9}, nil
	}
	return map[string]float64{}, nil
}

// alwaysPresent is a probe with data across the whole window.
type alwaysPresent struct{}

func (alwaysPresent) HasData(context.Context, time.Time) (bool, error) { return true, nil }

// Run maps coverage, replays, and scores in one pass — the shared pipeline
// behind the CLI and the endpoint.
func TestRunEndToEnd(t *testing.T) {
	from := time.Date(2026, 7, 18, 5, 0, 0, 0, time.UTC)
	to := from.Add(time.Hour)
	rs := backtest.RuleSet{Groups: []backtest.Group{{
		Name: "g", Rules: []backtest.Rule{
			{Name: "HighErr", Alert: true, Expr: `errs > 0.05`, For: 5 * time.Minute},
		},
	}}}
	incidents := []backtest.Incident{
		{ID: "inc-1", Start: from.Add(10 * time.Minute), End: from.Add(40 * time.Minute), Services: []string{"ad"}},
	}
	eval := fakeEval{series: `{service="ad"}`, trueFrom: from.Add(12 * time.Minute), trueTo: from.Add(40 * time.Minute)}

	rep, err := backtest.Run(context.Background(), eval, alwaysPresent{}, backtest.RunInput{
		Rules: rs, Incidents: incidents, From: from, To: to,
		Step: time.Minute, Grace: 5 * time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Coverage != time.Hour || rep.Segments != 1 {
		t.Errorf("coverage=%v segments=%d, want 1h/1", rep.Coverage, rep.Segments)
	}
	if len(rep.Rules) != 1 {
		t.Fatalf("scorecards = %d, want 1", len(rep.Rules))
	}
	sc := rep.Rules[0]
	if len(sc.Detections) != 1 || sc.Detections[0].IncidentID != "inc-1" {
		t.Errorf("detections = %+v, want inc-1", sc.Detections)
	}
	// condition true at 12m, for:5m → fires 17m, incident started 10m → TTD 7m
	if sc.Detections[0].TTD != 7*time.Minute {
		t.Errorf("TTD = %v, want 7m", sc.Detections[0].TTD)
	}
}

func TestRunRejectsInvertedWindow(t *testing.T) {
	now := time.Now()
	_, err := backtest.Run(context.Background(), fakeEval{}, alwaysPresent{}, backtest.RunInput{
		From: now, To: now.Add(-time.Hour), Step: time.Minute,
	})
	if err == nil {
		t.Error("inverted window accepted")
	}
}

// exprLog records every expression it is asked to evaluate, and never fires.
type exprLog struct{ exprs map[string]bool }

func (e *exprLog) Eval(_ context.Context, expr string, _ time.Time) (map[string]float64, error) {
	e.exprs[expr] = true
	return map[string]float64{}, nil
}

// firstHalf has data only in the first half hour of the window.
type firstHalf struct{ from time.Time }

func (f firstHalf) HasData(_ context.Context, t time.Time) (bool, error) {
	return t.Before(f.from.Add(30 * time.Minute)), nil
}

// With synthesis on, alerts are replayed with their recording rules inlined;
// an alert that cannot be synthesized is skipped with a caveat, not fatal; a
// caveat repeated by several rules is reported once; and partial coverage is
// disclosed.
func TestRunSynthesizesAndDisclosesCoverage(t *testing.T) {
	from := time.Date(2026, 7, 18, 5, 0, 0, 0, time.UTC)
	rs := backtest.RuleSet{Groups: []backtest.Group{{
		Name: "g", Rules: []backtest.Rule{
			{Name: "job:errs:rate5m", Expr: `sum by (job) (rate(errs_total[5m]))`},
			{Name: "ErrsHigh", Alert: true, Expr: `job:errs:rate5m > 1`},
			{Name: "ErrsVeryHigh", Alert: true, Expr: `job:errs:rate5m > 10`},
			{Name: "External", Alert: true, Expr: `slo:ratio:rate5m > 0.1`},
		},
	}}}
	eval := &exprLog{exprs: map[string]bool{}}

	rep, err := backtest.Run(context.Background(), eval, firstHalf{from: from}, backtest.RunInput{
		Rules: rs, From: from, To: from.Add(time.Hour), Step: time.Minute, Synthesize: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(rep.Rules) != 2 {
		t.Errorf("scorecards = %d, want the two synthesizable alerts", len(rep.Rules))
	}
	for expr := range eval.exprs {
		if strings.Contains(expr, "job:errs") {
			t.Errorf("replayed %q, want the recording rule inlined", expr)
		}
	}
	var notReplayed, inlined, coverage int
	for _, c := range rep.Caveats {
		switch {
		case strings.HasPrefix(c, "External not replayed"):
			notReplayed++
		case strings.Contains(c, `"job:errs:rate5m" synthesized inline`):
			inlined++
		case strings.HasPrefix(c, "telemetry covers 29m0s of the 1h0m0s window"):
			coverage++
		}
	}
	if notReplayed != 1 || inlined != 1 || coverage != 1 {
		t.Errorf("caveats = %q, want one each: not replayed, inlined, partial coverage", rep.Caveats)
	}
}
