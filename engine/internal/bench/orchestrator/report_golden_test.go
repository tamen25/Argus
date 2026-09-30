package orchestrator

import (
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/tamen25/Argus/engine/internal/bench"
	"github.com/tamen25/Argus/engine/internal/bench/agent"
	"github.com/tamen25/Argus/engine/internal/bench/local"
	"github.com/tamen25/Argus/engine/internal/bench/scoring"
)

var update = flag.Bool("update", false, "rewrite golden files")

// goldenReport exercises every branch the bench report renders: a cited
// diagnosis, a named decoy, a malformed citation, an uncited answer, a
// budget-exhausted run, an LLM-judge normalization, and model provenance whose
// served context differs from the architectural maximum.
func goldenReport() Report {
	e := func(name string) bench.Entity {
		return bench.Entity{Kind: "Deployment", Namespace: "otel-demo", Name: name}
	}
	cited := scoring.Result{
		Scenario: "cardinality-explosion-frontend", Score: 1, EntityScore: 1, CategoryMatch: true,
		EvidenceCount: 2, CitedSignals: []string{"logs", "metrics"},
		Matched: []bench.Entity{e("frontend")},
	}
	decoy := scoring.Result{
		Scenario: "cardinality-explosion-frontend", Score: 0, EntityScore: 0.5, CategoryMatch: false,
		EvidenceCount: 1, CitedSignals: []string{"metrics"}, MalformedEvidence: 1,
		DecoysNamed: []bench.Entity{e("product-reviews")},
		Matched:     []bench.Entity{e("frontend")}, Extra: []bench.Entity{e("product-reviews")},
	}
	uncited := scoring.Result{
		Scenario: "cardinality-explosion-frontend", Score: 0, EntityScore: 1, CategoryMatch: true,
		EvidenceMissing: true, Matched: []bench.Entity{e("frontend")},
	}
	r := Report{
		Scenario:     "cardinality-explosion-frontend",
		ScenarioHash: "8fb638d665e5",
		Agent:        "qwen3.6-bench",
		EnvDigest:    "kind-argus-golden",
		Seed:         7,
		Budget:       agent.Budget{MaxToolCalls: 35, MaxTokens: 400000},
		Model: &local.ModelInfo{
			Endpoint: "http://127.0.0.1:11434/v1/chat/completions", Model: "qwen3.6-bench",
			Quantization: "Q4_K_M", ParameterSize: "36.0B",
			EffectiveNumCtx: 32768, ArchContextLength: 262144,
		},
		Runs: []RunRecord{
			{Repeat: 0, Score: &cited, Normalization: "json", Usage: agent.Usage{ToolCalls: 9, Tokens: 14000}},
			{Repeat: 1, Score: &decoy, Normalization: "llm-judge", Usage: agent.Usage{ToolCalls: 12, Tokens: 18000}},
			{Repeat: 2, Score: &uncited, Normalization: "json", Usage: agent.Usage{ToolCalls: 4, Tokens: 6000}},
			{Repeat: 3, Error: "agent: budget exhausted before diagnosis", BudgetExhausted: true,
				Usage: agent.Usage{ToolCalls: 35, Tokens: 52000}},
		},
	}
	r.Summary = summarize(r.Runs)
	return r
}

// TestBenchReportGoldens locks both report renderings. The bench report was the
// only report format without one (master plan §5.2: golden-file tests for every report
// format), so a change to its table layout went unnoticed by every test.
// Regenerate deliberately with: go test ./internal/bench/orchestrator/ -run Golden -update
func TestBenchReportGoldens(t *testing.T) {
	r := goldenReport()
	js, err := RenderReportJSON(r)
	if err != nil {
		t.Fatal(err)
	}
	for name, got := range map[string]string{
		"bench-report.golden.md":   RenderReportMarkdown(r),
		"bench-report.golden.json": string(js),
	} {
		path := filepath.Join("testdata", name)
		if *update {
			if err := os.MkdirAll("testdata", 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		want, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s: %v (run with -update to create)", name, err)
		}
		if got != string(want) {
			t.Errorf("%s drift:\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
		}
	}
}
