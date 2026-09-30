package leaderboard

import (
	"flag"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tamen25/Argus/engine/internal/bench/agent"
	"github.com/tamen25/Argus/engine/internal/bench/local"
	"github.com/tamen25/Argus/engine/internal/bench/orchestrator"
	"github.com/tamen25/Argus/engine/internal/bench/scoring"
)

var update = flag.Bool("update", false, "rewrite golden files")

// noAnswer marks a run that produced no diagnosis (budget exhausted).
const noAnswer = -1

var t0 = time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)

// report builds one run report. Each score is one run; noAnswer is a run that
// ran out of budget. startMinute keeps reports distinct, as real ones are.
func report(agentName, scenario, condition string, startMinute int, scores ...float64) orchestrator.Report {
	r := orchestrator.Report{
		Scenario:     scenario,
		ScenarioHash: "aaaaaaaaaaaa" + scenario,
		Agent:        agentName,
		Condition:    condition,
		Budget:       agent.Budget{MaxToolCalls: 20, MaxTokens: 100000},
	}
	for i, s := range scores {
		run := orchestrator.RunRecord{
			Repeat:        i,
			Started:       t0.Add(time.Duration(startMinute+i) * time.Minute),
			Normalization: "json",
		}
		if s == noAnswer {
			run.Normalization = ""
			run.Error = "agent: budget exhausted before diagnosis"
			run.BudgetExhausted = true
		} else {
			run.Score = &scoring.Result{Scenario: scenario, Score: s}
		}
		r.Runs = append(r.Runs, run)
	}
	r.Summary = orchestrator.Summarize(r.Runs)
	return r
}

// matrix is a small run matrix with every shape the comparison has to handle:
// a clear improvement, no change, a scenario unanswered on one side, and a
// scenario run under only one condition.
func matrix() []orchestrator.Report {
	return []orchestrator.Report{
		report("model-a", "s1-cardinality", "degraded", 0, 0, 0.5),
		report("model-a", "s1-cardinality", "remediated", 10, 1, 1),
		report("model-a", "s2-latency", "degraded", 20, noAnswer, noAnswer),
		report("model-a", "s2-latency", "remediated", 30, 0.5, 0.5),
		report("model-a", "s3-oomkill", "degraded", 40, 0.5),
		report("model-a", "s4-partition", "degraded", 50, 0.5, 0.5),
		report("model-a", "s4-partition", "remediated", 60, 0.5, 1),

		report("model-b", "s1-cardinality", "degraded", 70, 0.5, 0.5),
		report("model-b", "s1-cardinality", "remediated", 80, 0.5, 0.5),
		report("model-b", "s4-partition", "degraded", 90, 1, noAnswer),
		report("model-b", "s4-partition", "remediated", 100, 1, 1),
	}
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func agentNamed(t *testing.T, c Comparison, name string) AgentComparison {
	t.Helper()
	for _, a := range c.Agents {
		if a.Agent == name {
			return a
		}
	}
	t.Fatalf("agent %q not in comparison", name)
	return AgentComparison{}
}

func TestCompare_MeansCoverOnlyScenariosAnsweredOnBothSides(t *testing.T) {
	c, err := Compare(matrix(), "degraded", "remediated")
	if err != nil {
		t.Fatal(err)
	}
	a := agentNamed(t, c, "model-a")

	// s1 (0.25 → 1.00) and s4 (0.50 → 0.75) are paired. s2 has no diagnosis
	// under degraded and s3 was never run under remediated: neither may count.
	if a.Paired != 2 {
		t.Fatalf("paired = %d, want 2", a.Paired)
	}
	if !near(a.BaselineMean, 0.375) || !near(a.TreatmentMean, 0.875) || !near(a.Delta, 0.5) {
		t.Errorf("means = %.3f → %.3f (Δ %.3f), want 0.375 → 0.875 (Δ 0.500)",
			a.BaselineMean, a.TreatmentMean, a.Delta)
	}
	if len(a.Scenarios) != 4 {
		t.Errorf("scenarios listed = %d, want all 4 the agent was run on", len(a.Scenarios))
	}

	want := []string{
		"s2-latency: no diagnosis under `degraded` (2 attempts)",
		"s3-oomkill: not run under `remediated`",
	}
	if strings.Join(a.Excluded, "|") != strings.Join(want, "|") {
		t.Errorf("excluded = %q, want %q", a.Excluded, want)
	}
	for _, s := range a.Scenarios {
		if (s.Scenario == "s2-latency" || s.Scenario == "s3-oomkill") && s.Delta != nil {
			t.Errorf("%s has Δ %v: a missing side is not a zero", s.Scenario, *s.Delta)
		}
	}
}

// A condition under which an agent stops answering is the most important thing
// a comparison can show, so answer rates pool every run, not only paired ones.
func TestCompare_AnswerRatesCoverEveryRun(t *testing.T) {
	c, err := Compare(matrix(), "degraded", "remediated")
	if err != nil {
		t.Fatal(err)
	}
	a := agentNamed(t, c, "model-a")
	// degraded: s1 2/2, s2 0/2, s3 1/1, s4 2/2 → 5 of 7.
	if a.BaselineAttempts != 7 || a.BaselineDiagnoses != 5 {
		t.Errorf("degraded runs answered = %d/%d, want 5/7", a.BaselineDiagnoses, a.BaselineAttempts)
	}
	if a.TreatmentAttempts != 6 || a.TreatmentDiagnoses != 6 {
		t.Errorf("remediated runs answered = %d/%d, want 6/6", a.TreatmentDiagnoses, a.TreatmentAttempts)
	}
}

func TestCompare_PoolsRunsRatherThanAveragingMeans(t *testing.T) {
	// One report with a single 1.00 and another with four 0.00: the cell mean is
	// 0.20 over five runs. Averaging the two report means would say 0.50.
	reports := []orchestrator.Report{
		report("m", "s", "degraded", 0, 1),
		report("m", "s", "degraded", 10, 0, 0, 0, 0),
		report("m", "s", "remediated", 20, 1),
	}
	c, err := Compare(reports, "degraded", "remediated")
	if err != nil {
		t.Fatal(err)
	}
	cell := c.Agents[0].Scenarios[0].Baseline
	if cell.Attempts != 5 || !near(cell.MeanScore, 0.2) {
		t.Errorf("pooled cell = %.2f over %d runs, want 0.20 over 5", cell.MeanScore, cell.Attempts)
	}
	if d := c.Agents[0].Scenarios[0].Delta; d == nil || !near(*d, 0.8) {
		t.Errorf("Δ = %v, want 0.80", d)
	}
}

func TestCompare_OrdersByImprovement(t *testing.T) {
	c, err := Compare(matrix(), "degraded", "remediated")
	if err != nil {
		t.Fatal(err)
	}
	// model-a improves by 0.50; model-b by 0.00 (s1 flat, s4 1.00 → 1.00).
	if c.Agents[0].Agent != "model-a" || c.Agents[1].Agent != "model-b" {
		t.Errorf("order = %s, %s; want the larger Δ first", c.Agents[0].Agent, c.Agents[1].Agent)
	}
	if !near(c.Agents[1].Delta, 0) {
		t.Errorf("model-b Δ = %v, want 0", c.Agents[1].Delta)
	}
}

func TestCompare_IgnoresOtherConditionsAndSaysSo(t *testing.T) {
	reports := append(matrix(), report("model-a", "s1-cardinality", "", 200, 1), report("model-a", "s1-cardinality", "canary", 210, 1))
	c, err := Compare(reports, "degraded", "remediated")
	if err != nil {
		t.Fatal(err)
	}
	cell := agentNamed(t, c, "model-a").Scenarios[0].Treatment
	if cell.Attempts != 2 {
		t.Errorf("remediated s1 has %d attempts, want 2: another condition's runs leaked in", cell.Attempts)
	}
	if !containsCaveat(c.Caveats, "2 report(s) carried a condition other than") {
		t.Errorf("ignored reports are not disclosed: %q", c.Caveats)
	}
}

func TestCompare_RejectsInputItCannotCompare(t *testing.T) {
	cases := []struct {
		name    string
		reports []orchestrator.Report
		a, b    string
		wantErr string
	}{
		{"no reports", nil, "degraded", "remediated", "no bench reports"},
		{"same label twice", matrix(), "degraded", "degraded", "two different conditions"},
		{"empty label", matrix(), "degraded", "", "two condition labels"},
		{"label nobody carries", matrix(), "degraded", "fixed", `no report carries condition "fixed"`},
		{"same report twice", append(matrix(), matrix()[0]), "degraded", "remediated", "given more than once"},
		{"report without runs", append(matrix(), orchestrator.Report{Agent: "m", Scenario: "s", Condition: "degraded"}),
			"degraded", "remediated", "has no runs"},
		{"not a run report", []orchestrator.Report{{}}, "degraded", "remediated", "not a bench run report"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Compare(tc.reports, tc.a, tc.b)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("err = %v, want one mentioning %q", err, tc.wantErr)
			}
		})
	}
}

func TestBuild_OneBoardPerConditionRankedAndAligned(t *testing.T) {
	lb, err := Build(matrix())
	if err != nil {
		t.Fatal(err)
	}
	if len(lb.Boards) != 2 || lb.Boards[0].Condition != "degraded" || lb.Boards[1].Condition != "remediated" {
		t.Fatalf("boards = %+v, want degraded then remediated", lb.Boards)
	}
	deg := lb.Boards[0]
	if strings.Join(deg.Scenarios, ",") != "s1-cardinality,s2-latency,s3-oomkill,s4-partition" {
		t.Errorf("scenarios = %v", deg.Scenarios)
	}
	// model-b: s1 0.50, s4 1.00 → 0.75. model-a: s1 0.25, s3 0.50, s4 0.50 → 0.4167.
	if deg.Rows[0].Agent != "model-b" || !near(deg.Rows[0].MeanScore, 0.75) {
		t.Errorf("top row = %s %.3f, want model-b 0.750", deg.Rows[0].Agent, deg.Rows[0].MeanScore)
	}
	a := deg.Rows[1]
	if !near(a.MeanScore, (0.25+0.5+0.5)/3) {
		t.Errorf("model-a mean = %.4f, want the mean of its three answered scenario means", a.MeanScore)
	}
	if a.ScenariosRun != 4 || a.ScenariosAnswered != 3 {
		t.Errorf("model-a coverage = %d/%d, want 3 answered of 4 run", a.ScenariosAnswered, a.ScenariosRun)
	}
	// Every row has a cell for every scenario, so columns line up; a scenario the
	// agent was not run on is an empty cell, not a missing one.
	for _, row := range deg.Rows {
		if len(row.Cells) != len(deg.Scenarios) {
			t.Fatalf("%s has %d cells for %d scenarios", row.Agent, len(row.Cells), len(deg.Scenarios))
		}
	}
	if b := deg.Rows[0]; b.Cells[1].Attempts != 0 || b.ScenariosRun != 2 {
		t.Errorf("model-b was not run on s2; got cell %+v, scenarios run %d", b.Cells[1], b.ScenariosRun)
	}
}

func TestDataCaveats_DiscloseWhatIsNotLikeForLike(t *testing.T) {
	changed := report("model-a", "s1-cardinality", "remediated", 300, 1)
	changed.ScenarioHash = "bbbbbbbbbbbbs1-cardinality"
	otherBudget := report("model-a", "s4-partition", "remediated", 310, 1)
	otherBudget.Budget = agent.Budget{MaxToolCalls: 40, MaxTokens: 100000}
	judged := report("model-b", "s4-partition", "remediated", 320, 1)
	judged.Runs[0].Normalization = "llm-judge"
	q4 := report("model-b", "s1-cardinality", "degraded", 330, 1)
	q4.Model = &local.ModelInfo{Model: "model-b", Quantization: "Q4_K_M", EffectiveNumCtx: 32768, WeightsDigest: "sha256:1111111111111111"}
	q8 := report("model-b", "s1-cardinality", "remediated", 340, 1)
	q8.Model = &local.ModelInfo{Model: "model-b", Quantization: "Q8_0", EffectiveNumCtx: 32768, WeightsDigest: "sha256:2222222222222222"}

	c, err := Compare(append(matrix(), changed, otherBudget, judged, q4, q8), "degraded", "remediated")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Scenario `s1-cardinality` was run with 2 different definitions",
		"Agent `model-a` ran under 2 different budgets",
		"Agent `model-b` was served by 2 different model builds",
		"1 run(s) were normalized by a non-deterministic method",
	} {
		if !containsCaveat(c.Caveats, want) {
			t.Errorf("missing caveat %q in:\n%s", want, strings.Join(c.Caveats, "\n"))
		}
	}

	// A clean matrix carries none of them.
	clean, err := Compare(matrix(), "degraded", "remediated")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range clean.Caveats {
		if strings.Contains(c, "different") || strings.Contains(c, "non-deterministic") {
			t.Errorf("a clean matrix got the data caveat %q", c)
		}
	}
}

// The standing caveats travel with every rendering (architecture rule 7).
func TestRenderings_AlwaysCarryTheStandingCaveats(t *testing.T) {
	c, err := Compare(matrix(), "degraded", "remediated")
	if err != nil {
		t.Fatal(err)
	}
	lb, err := Build(matrix())
	if err != nil {
		t.Fatal(err)
	}
	for name, md := range map[string]string{
		"comparison":  RenderComparisonMarkdown(c),
		"leaderboard": RenderLeaderboardMarkdown(lb),
	} {
		for _, want := range []string{
			"The telemetry condition is a label the operator attached",
			"are not scored as zero",
			"Evidence is checked for presence and well-formedness, NOT for truth",
		} {
			if !strings.Contains(md, want) {
				t.Errorf("%s rendering lacks the caveat %q", name, want)
			}
		}
	}
	if md := RenderComparisonMarkdown(c); !strings.Contains(md, "No significance test is applied") {
		t.Error("comparison rendering does not say that Δ is not a significance test")
	}
}

func TestGoldens(t *testing.T) {
	c, err := Compare(matrix(), "degraded", "remediated")
	if err != nil {
		t.Fatal(err)
	}
	lb, err := Build(matrix())
	if err != nil {
		t.Fatal(err)
	}
	cj, err := RenderJSON(c)
	if err != nil {
		t.Fatal(err)
	}
	lj, err := RenderJSON(lb)
	if err != nil {
		t.Fatal(err)
	}
	for name, got := range map[string]string{
		"comparison.golden.md":    RenderComparisonMarkdown(c),
		"comparison.golden.json":  string(cj),
		"leaderboard.golden.md":   RenderLeaderboardMarkdown(lb),
		"leaderboard.golden.json": string(lj),
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

	// Go randomizes map iteration; a report must not depend on it.
	for i := 0; i < 20; i++ {
		again, err := Compare(matrix(), "degraded", "remediated")
		if err != nil {
			t.Fatal(err)
		}
		if RenderComparisonMarkdown(again) != RenderComparisonMarkdown(c) {
			t.Fatal("comparison rendering differs between identical inputs")
		}
	}
}

func containsCaveat(caveats []string, want string) bool {
	for _, c := range caveats {
		if strings.Contains(c, want) {
			return true
		}
	}
	return false
}
