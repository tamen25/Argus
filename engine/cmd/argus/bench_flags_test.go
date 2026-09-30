package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/tamen25/Argus/engine/internal/bench"
	"github.com/tamen25/Argus/engine/internal/bench/judge"
	"github.com/tamen25/Argus/engine/internal/bench/orchestrator"
)

// runBenchExpectingError executes `bench run` with the given extra args and
// returns the error, which these tests require to be non-nil.
func runBenchExpectingError(t *testing.T, extra ...string) error {
	t.Helper()
	args := append([]string{"bench", "run", "--scenario", writeScenario(t)}, extra...)
	root := newRootCmd()
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.SetArgs(args)
	err := root.Execute()
	if err == nil {
		t.Fatalf("bench run %v: expected an error, got nil", extra)
	}
	return err
}

// A model grading its own output corrupts the score: it misreads its own
// answer the same way twice. Refused before anything is dialed.
func TestBenchRefusesJudgeSharingAgentModel(t *testing.T) {
	err := runBenchExpectingError(t,
		"--agent", "openai", "--endpoint", "https://api.example/v1/chat/completions",
		"--model", "model-x", "--mimir-url", "http://127.0.0.1:1", "--inject", "none",
		"--judge-endpoint", "https://api.example/v1/chat/completions",
		"--judge-model", "MODEL-X")
	if !errors.Is(err, ErrJudgeSameModel) {
		t.Fatalf("error = %v, want a judge/agent model refusal", err)
	}
}

func TestEnforceDistinctJudge(t *testing.T) {
	if err := enforceDistinctJudge("model-x", ""); err != nil {
		t.Errorf("no judge configured: %v", err)
	}
	if err := enforceDistinctJudge("model-x", "model-y"); err != nil {
		t.Errorf("different judge refused: %v", err)
	}
	if err := enforceDistinctJudge("model-x", " Model-X "); !errors.Is(err, ErrJudgeSameModel) {
		t.Errorf("same model (case and space differ): err = %v", err)
	}
}

// Local-inference flags were removed on 2026-10-01; using one is an error, not
// a silently ignored option.
func TestLocalInferenceFlagsAreGone(t *testing.T) {
	for _, flag := range []string{"--local-only", "--min-context=0"} {
		err := runBenchExpectingError(t, "--agent", "stub", "--inject", "none", flag)
		if !strings.Contains(err.Error(), "unknown flag") {
			t.Errorf("%s: err = %v, want unknown flag", flag, err)
		}
	}
}

func TestModelInfoRecordsWhatServedTheRun(t *testing.T) {
	got := modelInfo(benchFlags{agentKind: "openai", model: "m", endpoint: "https://api.example/v1"})
	if got == nil || got.Model != "m" || got.Endpoint != "https://api.example/v1" {
		t.Errorf("openai model info = %+v", got)
	}
	if got := modelInfo(benchFlags{agentKind: "stub"}); got != nil {
		t.Errorf("a stub has no model, got %+v", got)
	}
}

func TestJudgeHasItsOwnTimeout(t *testing.T) {
	fl := newBenchRunCmd().Flags()
	j := fl.Lookup("judge-timeout")
	if j == nil {
		t.Fatal("--judge-timeout flag missing")
	}
	if j.DefValue != judge.DefaultJudgeTimeout.String() {
		t.Fatalf("--judge-timeout default = %s, want judge.DefaultJudgeTimeout (%s)",
			j.DefValue, judge.DefaultJudgeTimeout)
	}
	if a := fl.Lookup("agent-timeout").DefValue; a == j.DefValue {
		t.Fatalf("judge and agent timeouts share a default (%s); they should be independent", a)
	}
}

// TestLegacyInjectModesRefuseScenarioHooks: --inject=script and =kubectl cannot
// run a scenario's reset/cleanup hooks. They must refuse, never skip: a skipped
// cleanup leaks a mutated workload into every later repeat and scenario.
func TestLegacyInjectModesRefuseScenarioHooks(t *testing.T) {
	sc := bench.Scenario{Metadata: bench.Metadata{Name: "s"},
		Spec: bench.ScenarioSpec{Cleanup: []bench.Hook{{Script: "restore.sh"}}}}
	for _, mode := range []string{"script", "kubectl"} {
		_, err := buildInjector(benchFlags{inject: mode, scenario: "scenarios/s.yaml"}, sc)
		if err == nil || !strings.Contains(err.Error(), "--inject=auto") {
			t.Errorf("--inject=%s with hooks: err = %v, want a refusal pointing at --inject=auto", mode, err)
		}
	}
	if _, err := buildInjector(benchFlags{inject: "auto", scenario: "scenarios/s.yaml"}, sc); err != nil {
		t.Errorf("--inject=auto must accept hooks, got %v", err)
	}
	if def := newBenchRunCmd().Flags().Lookup("inject").DefValue; def != "auto" {
		t.Errorf("--inject default = %s, want auto", def)
	}
}

// TestStubUsesRealProbeWhenGivenABackend: without --mimir-url the stub skips
// the steady-state gate (calibration must not need a cluster); with it, the
// stub gets the same observing probe a real agent does, so a scenario's
// lifecycle can be validated end to end at no model cost.
func TestStubUsesRealProbeWhenGivenABackend(t *testing.T) {
	sc := bench.Scenario{Spec: bench.ScenarioSpec{SteadyState: &bench.SteadyState{Query: "q"}}}
	if _, ok := buildProbe(benchFlags{agentKind: "stub"}, sc).(orchestrator.AlwaysReadyProbe); !ok {
		t.Error("stub without a backend should skip the gate")
	}
	if _, ok := buildProbe(benchFlags{agentKind: "stub", mimirURL: "http://127.0.0.1:1"}, sc).(*orchestrator.PromQLProbe); !ok {
		t.Error("stub with --mimir-url should use the observing probe")
	}
}
