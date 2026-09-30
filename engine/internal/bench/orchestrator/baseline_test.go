package orchestrator

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/tamen25/Argus/engine/internal/bench"
)

// clockedProbe answers Reached and Clean from scripts, advancing a fake clock
// by one poll interval on every call so settle windows and deadlines can be
// exercised without real waiting. The last scripted answer repeats.
type clockedProbe struct {
	reached []bool
	clean   []bool
	nr, nc  int
	now     time.Time
	step    time.Duration
}

func (p *clockedProbe) Reached(context.Context, bench.Scenario) (bool, error) {
	p.now = p.now.Add(p.step)
	v := p.reached[min(p.nr, len(p.reached)-1)]
	p.nr++
	return v, nil
}

func (p *clockedProbe) Clean(context.Context, bench.Scenario) (bool, error) {
	p.now = p.now.Add(p.step)
	v := p.clean[min(p.nc, len(p.clean)-1)]
	p.nc++
	return v, nil
}

func steadyScenario(settle string) bench.Scenario {
	sc := testScenario()
	sc.Spec.SteadyState = &bench.SteadyState{Query: "count(x)", Min: f64(300), Settle: settle}
	return sc
}

func clockedOpts(p *clockedProbe, repeats int) Options {
	o := fastOpts(repeats)
	o.Now = func() time.Time { return p.now }
	return o
}

// TestSettleWindowDoesNotLeakAcrossRepeats is the regression guard for B-02.
// With the settle timer stored in the shared probe, repeat 2 found it already
// running from repeat 1 and skipped its settle window. Each repeat must hold
// the signature for the full window on its own.
func TestSettleWindowDoesNotLeakAcrossRepeats(t *testing.T) {
	p := &clockedProbe{reached: []bool{true}, clean: []bool{true}, now: time.Unix(0, 0), step: 10 * time.Second}
	sc := steadyScenario("30s")
	if _, err := Run(context.Background(), sc, &scriptAgent{answers: []string{perfect, perfect}},
		nil, &fakeInjector{}, p, clockedOpts(p, 2)); err != nil {
		t.Fatal(err)
	}
	// A 30s window at 10s per poll needs 4 Reached calls: the one that starts
	// the window, then 10s, 20s, 30s. Two repeats that each honor it: 8.
	if p.nr != 8 {
		t.Fatalf("Reached called %d times across 2 repeats, want 8 (4 each) — "+
			"fewer means a repeat skipped its settle window", p.nr)
	}
}

// TestSettleWindowRestartsOnLapse: only an uninterrupted hold counts. A signature
// that flickers must not accumulate settle time across its gaps.
func TestSettleWindowRestartsOnLapse(t *testing.T) {
	// held, held, LAPSE, then held for the full window.
	p := &clockedProbe{reached: []bool{true, true, false, true, true, true, true}, clean: []bool{true},
		now: time.Unix(0, 0), step: 10 * time.Second}
	sc := steadyScenario("30s")
	if _, err := Run(context.Background(), sc, &scriptAgent{answers: []string{perfect}},
		nil, &fakeInjector{}, p, clockedOpts(p, 1)); err != nil {
		t.Fatal(err)
	}
	// 3 calls before and at the lapse, then 4 for a fresh uninterrupted window.
	if p.nr != 7 {
		t.Fatalf("Reached called %d times, want 7 — the lapse must restart the window", p.nr)
	}
}

// TestBaselineWaitsForCleanEnvironment: a repeat must not inject while the
// previous fault's signature is still present.
func TestBaselineWaitsForCleanEnvironment(t *testing.T) {
	p := &clockedProbe{reached: []bool{true}, clean: []bool{false, false, true},
		now: time.Unix(0, 0), step: 10 * time.Second}
	inj := &fakeInjector{}
	rep, err := Run(context.Background(), testScenarioWithSteady(), &scriptAgent{answers: []string{perfect}},
		nil, inj, p, clockedOpts(p, 1))
	if err != nil {
		t.Fatal(err)
	}
	if p.nc != 3 {
		t.Fatalf("Clean polled %d times, want 3 (dirty, dirty, clean)", p.nc)
	}
	if rep.Runs[0].Score == nil {
		t.Fatalf("run should have been scored once the baseline cleared; error: %s", rep.Runs[0].Error)
	}
}

// TestBaselineTimeoutIsAnAttributableFailure is the regression guard for B-03.
// When the fault's effects outlive its cleanup, the run must fail with a reason
// that names the baseline — not quietly pass steady state on stale telemetry.
func TestBaselineTimeoutIsAnAttributableFailure(t *testing.T) {
	p := &clockedProbe{reached: []bool{true}, clean: []bool{false}, now: time.Unix(0, 0), step: time.Minute}
	inj := &fakeInjector{}
	ag := &scriptAgent{answers: []string{perfect}}
	opts := clockedOpts(p, 1)
	opts.BaselineTimeout = 5 * time.Minute
	rep, err := Run(context.Background(), testScenarioWithSteady(), ag, nil, inj, p, opts)
	if err != nil {
		t.Fatal(err)
	}
	if got := rep.Runs[0].Error; !strings.Contains(got, "baseline") {
		t.Fatalf("error = %q, want a baseline failure", got)
	}
	if inj.injects != 0 {
		t.Fatalf("injected %d times into a dirty environment, want 0", inj.injects)
	}
	if ag.call != 0 {
		t.Fatal("agent must not be asked when the repeat could not start clean")
	}
	if rep.Runs[0].Score != nil {
		t.Fatal("a repeat that never started clean must not be scored")
	}
}

// TestBaselineSkippedForProbesThatCannotObserve: AlwaysReadyProbe and fakes
// cannot see the environment, so they are not asked to vouch for a baseline.
func TestBaselineSkippedForProbesThatCannotObserve(t *testing.T) {
	inj := &fakeInjector{}
	rep, err := Run(context.Background(), testScenarioWithSteady(), &scriptAgent{answers: []string{perfect}},
		nil, inj, okProbe{}, fastOpts(1))
	if err != nil {
		t.Fatal(err)
	}
	if inj.injects != 1 || rep.Runs[0].Score == nil {
		t.Fatalf("injects=%d scored=%v: a non-observing probe must not block the run",
			inj.injects, rep.Runs[0].Score != nil)
	}
}

func testScenarioWithSteady() bench.Scenario { return steadyScenario("") }

// TestMalformedEvidenceIsScoredNotExcluded is the regression guard for B-04.
// One bad citation used to fail validation for the whole diagnosis; the run
// became a normalization failure, the summary excluded it from the mean, and
// the agent's average went UP. It must instead be scored — zero, since the only
// citation does not count — and stay in the mean.
func TestMalformedEvidenceIsScoredNotExcluded(t *testing.T) {
	sc := testScenario()
	sc.Spec.Scoring.RequireEvidence = true
	cited := `{"root_cause_entities":[{"kind":"Deployment","namespace":"otel-demo","name":"checkout"}],` +
		`"category":"cardinality-explosion","evidence":[{"signal":"metrics","observation":"series tripled"}]}`
	malformed := `{"root_cause_entities":[{"kind":"Deployment","namespace":"otel-demo","name":"checkout"}],` +
		`"category":"cardinality-explosion","evidence":[{"signal":"prometheus","observation":"series tripled"}]}`

	rep, err := Run(context.Background(), sc, &scriptAgent{answers: []string{cited, malformed}},
		nil, &fakeInjector{}, okProbe{}, fastOpts(2))
	if err != nil {
		t.Fatal(err)
	}
	s := rep.Summary
	if s.Diagnoses != 2 {
		t.Fatalf("answered %d/2: the malformed-evidence run was excluded (error: %q)", s.Diagnoses, rep.Runs[1].Error)
	}
	if s.MeanScore != 0.5 {
		t.Fatalf("MeanScore = %v, want 0.5 (1.0 and 0.0) — 1.0 means the bad run inflated the average", s.MeanScore)
	}
	if got := rep.Runs[1].Score.MalformedEvidence; got != 1 {
		t.Fatalf("MalformedEvidence = %d, want 1 so the report shows the agent tried and got it wrong", got)
	}
}
