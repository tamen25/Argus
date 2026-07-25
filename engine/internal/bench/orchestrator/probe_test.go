package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/tamen25/Argus/engine/internal/bench"
)

type fakeQuerier struct {
	raw  string
	err  error
	last string
}

func (f *fakeQuerier) QueryInstant(_ context.Context, q string, _ time.Time) (json.RawMessage, error) {
	f.last = q
	if f.err != nil {
		return nil, f.err
	}
	return json.RawMessage(f.raw), nil
}

func vectorJSON(value string) string {
	return `{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[1,"` + value + `"]}]}}`
}

func scenarioWithSteadyState(ss *bench.SteadyState) bench.Scenario {
	return bench.Scenario{
		Metadata: bench.Metadata{Name: "s"},
		Spec:     bench.ScenarioSpec{SteadyState: ss},
	}
}

func f64(v float64) *float64 { return &v }

func TestProbeNoSteadyStateIsImmediatelyReady(t *testing.T) {
	p := &PromQLProbe{}
	ok, err := p.Reached(context.Background(), scenarioWithSteadyState(nil))
	if err != nil || !ok {
		t.Fatalf("Reached = (%v, %v), want (true, nil)", ok, err)
	}
}

func TestProbeWaitsUntilThresholdCrossed(t *testing.T) {
	q := &fakeQuerier{raw: vectorJSON("120")}
	p := &PromQLProbe{Q: q}
	sc := scenarioWithSteadyState(&bench.SteadyState{Query: "count(x)", Min: f64(5000)})

	ok, err := p.Reached(context.Background(), sc)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("Reached = true at 120 with min 5000, want false")
	}
	if q.last != "count(x)" {
		t.Fatalf("probe queried %q, want the scenario's query", q.last)
	}

	q.raw = vectorJSON("7000")
	if ok, err = p.Reached(context.Background(), sc); err != nil || !ok {
		t.Fatalf("Reached = (%v, %v) at 7000, want (true, nil)", ok, err)
	}
}

func TestProbeRespectsMax(t *testing.T) {
	q := &fakeQuerier{raw: vectorJSON("0.5")}
	p := &PromQLProbe{Q: q}
	sc := scenarioWithSteadyState(&bench.SteadyState{Query: "rate(x)", Max: f64(0.1)})

	if ok, _ := p.Reached(context.Background(), sc); ok {
		t.Fatal("0.5 should not satisfy max 0.1")
	}
	q.raw = vectorJSON("0.05")
	if ok, _ := p.Reached(context.Background(), sc); !ok {
		t.Fatal("0.05 should satisfy max 0.1")
	}
}

// TestProbeEmptyResultIsNotReady: before the fault, the series does not exist.
// That is the normal pre-fault state, not an error.
func TestProbeEmptyResultIsNotReady(t *testing.T) {
	q := &fakeQuerier{raw: `{"status":"success","data":{"resultType":"vector","result":[]}}`}
	p := &PromQLProbe{Q: q}
	sc := scenarioWithSteadyState(&bench.SteadyState{Query: "count(x)", Min: f64(1)})
	ok, err := p.Reached(context.Background(), sc)
	if err != nil {
		t.Fatalf("empty result should not error, got %v", err)
	}
	if ok {
		t.Fatal("empty result should not be ready")
	}
}

// TestProbeBackendErrorIsNotReady: during bootstrap the backend legitimately
// fails before the first scrape. A not-yet must not abort the run.
func TestProbeBackendErrorIsNotReady(t *testing.T) {
	q := &fakeQuerier{err: errors.New("connection refused")}
	p := &PromQLProbe{Q: q}
	sc := scenarioWithSteadyState(&bench.SteadyState{Query: "count(x)", Min: f64(1)})
	ok, err := p.Reached(context.Background(), sc)
	if err != nil {
		t.Fatalf("backend error should be a not-yet, got %v", err)
	}
	if ok {
		t.Fatal("should not be ready when the backend is unreachable")
	}
}

func TestProbeRequiresBackendWhenSteadyStateDeclared(t *testing.T) {
	p := &PromQLProbe{}
	sc := scenarioWithSteadyState(&bench.SteadyState{Query: "count(x)", Min: f64(1)})
	if _, err := p.Reached(context.Background(), sc); err == nil {
		t.Fatal("want an error when steadyState is declared without a backend")
	}
}

// TestProbeSettleWindow: a value that has only just crossed the line is not an
// established state. The condition must hold across the settle window.
func TestProbeSettleWindow(t *testing.T) {
	q := &fakeQuerier{raw: vectorJSON("9000")}
	now := time.Unix(1000, 0)
	p := &PromQLProbe{Q: q, Now: func() time.Time { return now }}
	sc := scenarioWithSteadyState(&bench.SteadyState{Query: "count(x)", Min: f64(5000), Settle: "60s"})

	if ok, _ := p.Reached(context.Background(), sc); ok {
		t.Fatal("first crossing should start the settle window, not end it")
	}
	now = now.Add(30 * time.Second)
	if ok, _ := p.Reached(context.Background(), sc); ok {
		t.Fatal("30s into a 60s settle window should not be ready")
	}
	now = now.Add(31 * time.Second)
	if ok, _ := p.Reached(context.Background(), sc); !ok {
		t.Fatal("past the settle window should be ready")
	}
}

// TestProbeSettleResetsOnDip: a metric that falls back below the threshold has
// not held, and the window restarts.
func TestProbeSettleResetsOnDip(t *testing.T) {
	q := &fakeQuerier{raw: vectorJSON("9000")}
	now := time.Unix(1000, 0)
	p := &PromQLProbe{Q: q, Now: func() time.Time { return now }}
	sc := scenarioWithSteadyState(&bench.SteadyState{Query: "count(x)", Min: f64(5000), Settle: "60s"})

	_, _ = p.Reached(context.Background(), sc) // starts the window
	q.raw = vectorJSON("100")                  // dips back below
	now = now.Add(30 * time.Second)
	if ok, _ := p.Reached(context.Background(), sc); ok {
		t.Fatal("a dip below the threshold must not be ready")
	}
	q.raw = vectorJSON("9000")
	now = now.Add(1 * time.Second)
	if ok, _ := p.Reached(context.Background(), sc); ok {
		t.Fatal("the settle window must restart after a dip")
	}
}

func TestFirstSampleValueAcceptsScalar(t *testing.T) {
	raw := json.RawMessage(`{"status":"success","data":{"resultType":"scalar","result":[1,"42.5"]}}`)
	v, ok, err := firstSampleValue(raw)
	if err != nil || !ok || v != 42.5 {
		t.Fatalf("firstSampleValue = (%v, %v, %v), want (42.5, true, nil)", v, ok, err)
	}
}

func TestFirstSampleValueRejectsErrorStatus(t *testing.T) {
	raw := json.RawMessage(`{"status":"error","data":{"resultType":"vector","result":[]}}`)
	if _, _, err := firstSampleValue(raw); err == nil {
		t.Fatal("want an error for status=error")
	}
}
