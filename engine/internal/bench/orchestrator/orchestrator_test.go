package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/tamen25/Argus/engine/internal/bench"
	"github.com/tamen25/Argus/engine/internal/bench/agent"
	"github.com/tamen25/Argus/engine/internal/mcp"
)

func testScenario() bench.Scenario {
	return bench.Scenario{
		APIVersion: bench.APIVersion,
		Kind:       bench.Kind,
		Metadata:   bench.Metadata{Name: "cardinality-explosion-checkout"},
		Spec: bench.ScenarioSpec{
			Environment: bench.Environment{App: "otel-demo"},
			Inject:      []bench.InjectStep{{Type: bench.InjectScript, Script: "s.sh", Duration: "10m"}},
			GroundTruth: bench.GroundTruth{
				RootCauseEntities: []bench.Entity{{Kind: "Deployment", Namespace: "otel-demo", Name: "checkout"}},
				Category:          "cardinality-explosion",
			},
			Scoring: bench.ScoringSpec{EntityMatch: bench.MatchJaccard, PartialCredit: true},
		},
	}
}

// fakeInjector records calls and can fail on demand.
type fakeInjector struct {
	resets, injects, cleanups int
	resetErr                  error
}

func (f *fakeInjector) Reset(context.Context, bench.Scenario) error {
	f.resets++
	return f.resetErr
}
func (f *fakeInjector) Inject(context.Context, bench.Scenario, bench.InjectStep) error {
	f.injects++
	return nil
}
func (f *fakeInjector) Cleanup(context.Context, bench.Scenario) error {
	f.cleanups++
	return nil
}

type okProbe struct{}

func (okProbe) Reached(context.Context, bench.Scenario) (bool, error) { return true, nil }

type neverProbe struct{}

func (neverProbe) Reached(context.Context, bench.Scenario) (bool, error) { return false, nil }

// scriptAgent returns canned raw answers (or errors) per repeat.
type scriptAgent struct {
	answers  []string
	errs     []error
	call     int
	lastTask agent.Task
}

func (s *scriptAgent) Name() string { return "script-agent" }

func (s *scriptAgent) Diagnose(_ context.Context, task agent.Task) (agent.Result, error) {
	s.lastTask = task
	i := s.call
	s.call++
	usage := agent.Usage{ToolCalls: 2, Tokens: 100, Steps: 2}
	if i < len(s.errs) && s.errs[i] != nil {
		return agent.Result{Usage: usage}, s.errs[i]
	}
	return agent.Result{Raw: json.RawMessage(s.answers[i]), Usage: usage}, nil
}

const perfect = `{"root_cause_entities":[{"kind":"Deployment","namespace":"otel-demo","name":"checkout"}],"category":"cardinality-explosion"}`
const halfRight = `{"root_cause_entities":[{"kind":"Deployment","namespace":"otel-demo","name":"frontend"}],"category":"other"}`

func fastOpts(repeats int) Options {
	return Options{
		Repeats:      repeats,
		Budget:       agent.Budget{MaxToolCalls: 5, MaxTokens: 1000},
		PollInterval: time.Millisecond,
		Seed:         42,
		EnvDigest:    "kind-abc123",
	}
}

func TestRun_HappyPath(t *testing.T) {
	inj := &fakeInjector{}
	ag := &scriptAgent{answers: []string{perfect}}
	rep, err := Run(context.Background(), testScenario(), ag, nil, inj, okProbe{}, fastOpts(1))
	if err != nil {
		t.Fatal(err)
	}
	if inj.resets != 1 || inj.injects != 1 || inj.cleanups != 1 {
		t.Errorf("injector calls: resets=%d injects=%d cleanups=%d", inj.resets, inj.injects, inj.cleanups)
	}
	if len(rep.Runs) != 1 || rep.Runs[0].Score == nil {
		t.Fatalf("runs = %+v", rep.Runs)
	}
	if rep.Runs[0].Score.EntityScore != 1 || !rep.Runs[0].Score.CategoryMatch {
		t.Errorf("score = %+v, want perfect", rep.Runs[0].Score)
	}
	if rep.Runs[0].Normalization != "json" {
		t.Errorf("normalization = %q, want json", rep.Runs[0].Normalization)
	}
	if rep.Summary.Diagnoses != 1 || rep.Summary.MeanEntityScore != 1 {
		t.Errorf("summary = %+v", rep.Summary)
	}
	if rep.ScenarioHash == "" || rep.Seed != 42 || rep.EnvDigest != "kind-abc123" {
		t.Errorf("reproducibility fields missing: %+v", rep)
	}
	if rep.Agent != "script-agent" {
		t.Errorf("agent = %q", rep.Agent)
	}
}

func TestRun_VarianceAcrossRepeats(t *testing.T) {
	ag := &scriptAgent{answers: []string{perfect, halfRight, perfect}}
	rep, err := Run(context.Background(), testScenario(), ag, nil, &fakeInjector{}, okProbe{}, fastOpts(3))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Summary.Attempts != 3 || rep.Summary.Diagnoses != 3 {
		t.Fatalf("summary = %+v", rep.Summary)
	}
	// Two perfect (1.0) + one all-wrong (0.0) -> mean 2/3, non-zero stddev.
	if got := rep.Summary.MeanEntityScore; got < 0.66 || got > 0.67 {
		t.Errorf("mean = %v, want ~0.667", got)
	}
	if rep.Summary.StdDevEntityScore == 0 {
		t.Error("stddev = 0, want non-zero variance")
	}
	if got := rep.Summary.CategoryMatchRate; got < 0.66 || got > 0.67 {
		t.Errorf("category match rate = %v, want ~0.667", got)
	}
	if rep.Summary.MeanToolCalls != 2 || rep.Summary.MeanTokens != 100 {
		t.Errorf("usage means = %+v", rep.Summary)
	}
}

func TestRun_AgentFailureRecordedNotFatal(t *testing.T) {
	ag := &scriptAgent{
		answers: []string{"", perfect},
		errs:    []error{agent.ErrBudgetExhausted, nil},
	}
	rep, err := Run(context.Background(), testScenario(), ag, nil, &fakeInjector{}, okProbe{}, fastOpts(2))
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Runs) != 2 {
		t.Fatalf("runs = %d, want 2 (one failure must not abort the matrix)", len(rep.Runs))
	}
	if !rep.Runs[0].BudgetExhausted {
		t.Error("first run should be flagged budget-exhausted")
	}
	if rep.Runs[0].Score != nil {
		t.Error("failed run must not carry a score (no silent zero)")
	}
	if rep.Summary.Failures != 1 || rep.Summary.BudgetExhausted != 1 || rep.Summary.Diagnoses != 1 {
		t.Errorf("summary = %+v", rep.Summary)
	}
	// Mean is over runs that produced a diagnosis, not over attempts.
	if rep.Summary.MeanEntityScore != 1 {
		t.Errorf("mean = %v, want 1 (averaged over diagnoses only)", rep.Summary.MeanEntityScore)
	}
}

func TestRun_CleanupRunsOnFailure(t *testing.T) {
	inj := &fakeInjector{resetErr: errors.New("cluster unreachable")}
	ag := &scriptAgent{answers: []string{perfect}}
	rep, err := Run(context.Background(), testScenario(), ag, nil, inj, okProbe{}, fastOpts(1))
	if err != nil {
		t.Fatal(err)
	}
	if inj.cleanups != 1 {
		t.Errorf("cleanups = %d, want 1 even when reset failed (a leaked fault poisons later repeats)", inj.cleanups)
	}
	if !strings.Contains(rep.Runs[0].Error, "cluster unreachable") {
		t.Errorf("error = %q", rep.Runs[0].Error)
	}
	if ag.call != 0 {
		t.Error("agent must not run when injection failed")
	}
}

func TestRun_SteadyStateTimeout(t *testing.T) {
	sc := testScenario()
	sc.Spec.Inject[0].Duration = "1ms" // tiny window so the probe times out fast
	ag := &scriptAgent{answers: []string{perfect}}
	rep, err := Run(context.Background(), sc, ag, nil, &fakeInjector{}, neverProbe{}, fastOpts(1))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rep.Runs[0].Error, "steady state") {
		t.Errorf("error = %q, want steady-state timeout", rep.Runs[0].Error)
	}
	if ag.call != 0 {
		t.Error("agent must not run before steady state")
	}
}

// The brief must never hand the agent its answer.
func TestDefaultBrief_LeaksNoGroundTruth(t *testing.T) {
	sc := testScenario()
	brief := DefaultBrief(sc)
	for _, leak := range []string{"checkout", "cardinality-explosion", "Deployment"} {
		if strings.Contains(brief, leak) {
			t.Errorf("brief leaks ground truth %q: %s", leak, brief)
		}
	}
	if !strings.Contains(brief, "otel-demo") {
		t.Errorf("brief should name the environment: %s", brief)
	}
}

func TestRun_AgentReceivesScenarioAndBudget(t *testing.T) {
	ag := &scriptAgent{answers: []string{perfect}}
	opts := fastOpts(1)
	if _, err := Run(context.Background(), testScenario(), ag, nil, &fakeInjector{}, okProbe{}, opts); err != nil {
		t.Fatal(err)
	}
	if ag.lastTask.Scenario != "cardinality-explosion-checkout" {
		t.Errorf("task scenario = %q", ag.lastTask.Scenario)
	}
	if ag.lastTask.Budget != opts.Budget {
		t.Errorf("task budget = %+v, want %+v", ag.lastTask.Budget, opts.Budget)
	}
}

// A second normalizer is only reached when the first fails, and the method
// actually used is what gets recorded.
type fallbackNormalizer struct{ used *bool }

func (f fallbackNormalizer) Method() string { return "llm-judge" }
func (f fallbackNormalizer) Normalize(_ context.Context, _ []byte, scenario string) (bench.Diagnosis, error) {
	*f.used = true
	return bench.Diagnosis{
		Scenario:          scenario,
		Category:          "cardinality-explosion",
		RootCauseEntities: []bench.Entity{{Kind: "Deployment", Namespace: "otel-demo", Name: "checkout"}},
	}, nil
}

func TestRun_NormalizerFallbackRecordsMethod(t *testing.T) {
	used := false
	opts := fastOpts(1)
	opts.Normalizers = []bench.Normalizer{bench.JSONNormalizer{}, fallbackNormalizer{used: &used}}

	// Prose, not JSON: the deterministic normalizer must fail and the judge win.
	ag := &scriptAgent{answers: []string{`the checkout deployment exploded cardinality`}}
	rep, err := Run(context.Background(), testScenario(), ag, nil, &fakeInjector{}, okProbe{}, opts)
	if err != nil {
		t.Fatal(err)
	}
	if !used {
		t.Fatal("fallback normalizer was not reached")
	}
	if rep.Runs[0].Normalization != "llm-judge" {
		t.Errorf("normalization = %q, want the method actually used", rep.Runs[0].Normalization)
	}
	if rep.Runs[0].Score == nil || rep.Runs[0].Score.EntityScore != 1 {
		t.Errorf("score = %+v", rep.Runs[0].Score)
	}
}

func TestHashScenario_StableAndSensitive(t *testing.T) {
	a, err := HashScenario(testScenario())
	if err != nil {
		t.Fatal(err)
	}
	b, _ := HashScenario(testScenario())
	if a != b {
		t.Error("hash not stable across identical scenarios")
	}
	changed := testScenario()
	changed.Spec.GroundTruth.Category = "something-else"
	c, _ := HashScenario(changed)
	if a == c {
		t.Error("hash did not change when the scenario changed")
	}
}

// Tools is satisfied by *mcp.Registry — compile-time proof the orchestrator
// hands agents the same read-only surface the MCP server exposes.
var _ agent.Tools = (*mcp.Registry)(nil)
