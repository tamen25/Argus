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

// TestProbeCleanRequiresObservedAbsence: a baseline is only clean when the
// query answered and the condition does not hold. An unreachable backend proves
// nothing, so it must not count as clean.
func TestProbeCleanRequiresObservedAbsence(t *testing.T) {
	sc := scenarioWithSteadyState(&bench.SteadyState{Query: "count(x)", Min: f64(300)})
	cases := []struct {
		name string
		q    *fakeQuerier
		want bool
	}{
		{"below threshold", &fakeQuerier{raw: vectorJSON("40")}, true},
		{"series absent", &fakeQuerier{raw: `{"status":"success","data":{"resultType":"vector","result":[]}}`}, true},
		{"fault present", &fakeQuerier{raw: vectorJSON("2000")}, false},
		{"backend down", &fakeQuerier{err: errors.New("connection refused")}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := (&PromQLProbe{Q: tc.q}).Clean(context.Background(), sc)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("Clean = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestProbeIsStateless: the probe answers "does it hold now" and nothing else.
// Settle state kept inside the probe leaked from one repeat into the next.
func TestProbeIsStateless(t *testing.T) {
	q := &fakeQuerier{raw: vectorJSON("9000")}
	p := &PromQLProbe{Q: q}
	sc := scenarioWithSteadyState(&bench.SteadyState{Query: "count(x)", Min: f64(5000), Settle: "60s"})
	for i := 0; i < 3; i++ {
		ok, err := p.Reached(context.Background(), sc)
		if err != nil || !ok {
			t.Fatalf("call %d: Reached = (%v, %v), want (true, nil) on every call", i, ok, err)
		}
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
