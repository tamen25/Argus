package orchestrator

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/tamen25/Argus/engine/internal/bench"
	"github.com/tamen25/Argus/engine/internal/bench/agent"
	"github.com/tamen25/Argus/engine/internal/bench/scoring"
)

func sampleReport() Report {
	perfectScore := scoring.Result{EntityScore: 1, CategoryMatch: true}
	partial := scoring.Result{
		EntityScore:   0.5,
		CategoryMatch: false,
		Missed:        []bench.Entity{{Kind: "Deployment", Namespace: "otel-demo", Name: "checkout"}},
	}
	r := Report{
		Scenario:     "cardinality-explosion-checkout",
		ScenarioHash: "abc123",
		Agent:        "test-model",
		EnvDigest:    "kind-xyz",
		Seed:         42,
		Budget:       agent.Budget{MaxToolCalls: 20, MaxTokens: 100000},
		Runs: []RunRecord{
			{Repeat: 0, Score: &perfectScore, Normalization: "json", Usage: agent.Usage{ToolCalls: 3, Tokens: 900}},
			{Repeat: 1, Score: &partial, Normalization: "llm-judge", Usage: agent.Usage{ToolCalls: 5, Tokens: 1200}},
			{Repeat: 2, Error: "agent: budget exhausted before diagnosis", BudgetExhausted: true, Usage: agent.Usage{ToolCalls: 20, Tokens: 5000}},
		},
	}
	r.Summary = summarize(r.Runs)
	return r
}

func TestRenderMarkdown_CoreFacts(t *testing.T) {
	md := RenderReportMarkdown(sampleReport())
	for _, want := range []string{
		"cardinality-explosion-checkout",
		"test-model",
		"abc123",        // scenario hash (reproducibility)
		"kind-xyz",      // env digest
		"20 tool calls", // budget disclosed
		"budget exhausted",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown missing %q:\n%s", want, md)
		}
	}
}

// A report that used the LLM judge must say so, unmissably.
func TestRenderMarkdown_DisclosesLLMJudge(t *testing.T) {
	md := RenderReportMarkdown(sampleReport())
	if !strings.Contains(md, "llm-judge") {
		t.Fatal("markdown does not name the llm-judge normalization method")
	}
	if !strings.Contains(md, "not deterministic") {
		t.Errorf("markdown does not flag llm-judge as non-deterministic:\n%s", md)
	}
}

// The standing caveats can never be dropped from a rendering.
func TestRenderMarkdown_StandingCaveatsAlwaysPresent(t *testing.T) {
	for _, r := range []Report{sampleReport(), {Scenario: "empty", Agent: "none"}} {
		md := RenderReportMarkdown(r)
		for _, c := range standingBenchCaveats {
			if !strings.Contains(md, c) {
				t.Errorf("missing standing caveat %q", c)
			}
		}
	}
}

// An uncapped run must say so rather than silently omitting the budget.
func TestRenderMarkdown_UncappedBudgetIsExplicit(t *testing.T) {
	r := sampleReport()
	r.Budget = agent.Budget{}
	md := RenderReportMarkdown(r)
	if !strings.Contains(md, "uncapped") {
		t.Errorf("uncapped budget not disclosed:\n%s", md)
	}
}

func TestRenderMarkdown_Deterministic(t *testing.T) {
	a := RenderReportMarkdown(sampleReport())
	b := RenderReportMarkdown(sampleReport())
	if a != b {
		t.Error("markdown rendering is not deterministic")
	}
}

func TestRenderMarkdown_ListsMissedEntities(t *testing.T) {
	md := RenderReportMarkdown(sampleReport())
	if !strings.Contains(md, "Deployment/otel-demo/checkout") {
		t.Errorf("missed entity not reported:\n%s", md)
	}
}

func TestRenderJSON_RoundTrips(t *testing.T) {
	b, err := RenderReportJSON(sampleReport())
	if err != nil {
		t.Fatal(err)
	}
	var back Report
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatalf("json does not round-trip: %v", err)
	}
	if back.ScenarioHash != "abc123" || len(back.Runs) != 3 {
		t.Errorf("round-tripped report = %+v", back)
	}
	if back.Summary.Diagnoses != 2 || back.Summary.BudgetExhausted != 1 {
		t.Errorf("summary lost in round-trip: %+v", back.Summary)
	}
}
