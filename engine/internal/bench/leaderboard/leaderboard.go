// Package leaderboard turns a set of bench reports into the two tables a reader
// wants from a run matrix: an agent × scenario leaderboard, and the comparison
// of one telemetry condition against another (master plan §3.2, the flagship
// experiment).
//
// It is part of the deterministic core: pure functions over reports, no I/O and
// no LLM. Everything it prints can be recomputed by hand from the run reports
// it was given.
package leaderboard

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/tamen25/Argus/engine/internal/bench/agent"
	"github.com/tamen25/Argus/engine/internal/bench/orchestrator"
)

// Cell is one agent × scenario × condition result, pooled over every run of
// every report that falls in it.
type Cell struct {
	Scenario        string  `json:"scenario"`
	Attempts        int     `json:"attempts"`
	Diagnoses       int     `json:"diagnoses"`
	BudgetExhausted int     `json:"budget_exhausted"`
	AnswerRate      float64 `json:"answer_rate"`
	// MeanScore and StdDevScore are over answered runs only, exactly as in a run
	// report. They are meaningless when Diagnoses is zero.
	MeanScore   float64 `json:"mean_score"`
	StdDevScore float64 `json:"stddev_score"`
}

// Answered reports whether the cell has at least one scored diagnosis, and so
// a mean worth printing.
func (c Cell) Answered() bool { return c.Diagnoses > 0 }

// Row is one agent's line on a board.
type Row struct {
	Agent string `json:"agent"`
	// Cells is aligned with Board.Scenarios. A cell with zero Attempts means the
	// agent was not run on that scenario under this condition.
	Cells []Cell `json:"cells"`
	// ScenariosRun and ScenariosAnswered say how much of the suite the mean
	// covers: an agent that answered one scenario perfectly is not comparable
	// with one that answered eight.
	ScenariosRun      int `json:"scenarios_run"`
	ScenariosAnswered int `json:"scenarios_answered"`
	// MeanScore is the mean of the per-scenario means over answered scenarios,
	// so every scenario weighs the same whatever its number of repeats.
	MeanScore float64 `json:"mean_score"`
	// Attempts, Diagnoses and AnswerRate pool every run in the row.
	Attempts   int     `json:"attempts"`
	Diagnoses  int     `json:"diagnoses"`
	AnswerRate float64 `json:"answer_rate"`
}

// Board is the agent × scenario matrix for one telemetry condition.
type Board struct {
	// Condition is the label the runs carried; empty for unlabeled runs.
	Condition string   `json:"condition"`
	Scenarios []string `json:"scenarios"`
	// Rows are ranked: mean score, then answer rate, then name.
	Rows []Row `json:"rows"`
}

// Leaderboard is one board per condition found in the reports.
type Leaderboard struct {
	Boards  []Board  `json:"boards"`
	Caveats []string `json:"caveats"`
}

// ScenarioComparison is one scenario under both conditions for one agent.
type ScenarioComparison struct {
	Scenario string `json:"scenario"`
	// Baseline and Treatment are nil when the agent was not run on the scenario
	// under that condition.
	Baseline  *Cell `json:"baseline,omitempty"`
	Treatment *Cell `json:"treatment,omitempty"`
	// Delta is treatment − baseline mean score. It is nil unless the scenario was
	// answered under both conditions: a missing side is not a zero.
	Delta *float64 `json:"delta,omitempty"`
}

// AgentComparison is one agent's result under both conditions.
type AgentComparison struct {
	Agent     string               `json:"agent"`
	Scenarios []ScenarioComparison `json:"scenarios"`
	// Paired is the number of scenarios answered under both conditions. The
	// three means below are over exactly those scenarios.
	Paired        int     `json:"paired"`
	BaselineMean  float64 `json:"baseline_mean"`
	TreatmentMean float64 `json:"treatment_mean"`
	Delta         float64 `json:"delta"`
	// Answer rates pool every run the agent made under the condition, paired or
	// not: a condition under which an agent stops answering must stay visible.
	BaselineAttempts    int     `json:"baseline_attempts"`
	BaselineDiagnoses   int     `json:"baseline_diagnoses"`
	BaselineAnswerRate  float64 `json:"baseline_answer_rate"`
	TreatmentAttempts   int     `json:"treatment_attempts"`
	TreatmentDiagnoses  int     `json:"treatment_diagnoses"`
	TreatmentAnswerRate float64 `json:"treatment_answer_rate"`
	// Excluded names each scenario left out of the means, and why.
	Excluded []string `json:"excluded,omitempty"`
}

// Comparison is the flagship report: every agent under a baseline and a
// treatment condition.
type Comparison struct {
	Baseline  string            `json:"baseline"`
	Treatment string            `json:"treatment"`
	Agents    []AgentComparison `json:"agents"`
	Caveats   []string          `json:"caveats"`
}

// standingCaveats apply to every rendering; they can never be stripped
// (architecture rule 7).
var standingCaveats = []string{
	"A cell pools every run of that agent on that scenario under that condition; its mean and ± spread are over answered runs only. Runs that produced no diagnosis are counted in the answered figures and are not scored as zero.",
	"An agent's mean is the mean of its per-scenario means over the scenarios it answered, so each scenario weighs the same whatever its number of repeats. Read it together with how many scenarios that covers.",
	"The telemetry condition is a label the operator attached to each run. Argus records it; it does not verify what state the environment was in.",
}

var comparisonCaveats = []string{
	"Δ is treatment − baseline, over scenarios the agent answered under both conditions. A scenario missing or unanswered on either side is listed as excluded, not counted as zero.",
	"No significance test is applied. Compare Δ with the ± spread and the number of runs before reading it as an effect.",
}

// Build groups the reports into one board per condition.
func Build(reports []orchestrator.Report) (Leaderboard, error) {
	if err := validate(reports); err != nil {
		return Leaderboard{}, err
	}
	byCondition := map[string][]orchestrator.Report{}
	for _, r := range reports {
		byCondition[r.Condition] = append(byCondition[r.Condition], r)
	}
	conditions := make([]string, 0, len(byCondition))
	for c := range byCondition {
		conditions = append(conditions, c)
	}
	sort.Strings(conditions)

	lb := Leaderboard{}
	for _, c := range conditions {
		lb.Boards = append(lb.Boards, board(c, byCondition[c]))
	}
	lb.Caveats = append(dataCaveats(reports), withScoring(standingCaveats)...)
	return lb, nil
}

// Compare builds the baseline-vs-treatment comparison. Reports carrying any
// other condition label (or none) are ignored, and said to be.
func Compare(reports []orchestrator.Report, baseline, treatment string) (Comparison, error) {
	if baseline == "" || treatment == "" {
		return Comparison{}, errors.New("compare needs two condition labels")
	}
	if baseline == treatment {
		return Comparison{}, fmt.Errorf("compare needs two different conditions, got %q twice", baseline)
	}
	if err := validate(reports); err != nil {
		return Comparison{}, err
	}

	var used []orchestrator.Report
	ignored := 0
	seen := map[string]bool{}
	for _, r := range reports {
		if r.Condition != baseline && r.Condition != treatment {
			ignored++
			continue
		}
		seen[r.Condition] = true
		used = append(used, r)
	}
	for _, c := range []string{baseline, treatment} {
		if !seen[c] {
			return Comparison{}, fmt.Errorf("no report carries condition %q (run `argus bench run --condition %s`)", c, c)
		}
	}

	base := cells(filter(used, baseline))
	treat := cells(filter(used, treatment))

	agents := map[string]bool{}
	for a := range base {
		agents[a] = true
	}
	for a := range treat {
		agents[a] = true
	}

	cmp := Comparison{Baseline: baseline, Treatment: treatment}
	for _, a := range sortedKeys(agents) {
		cmp.Agents = append(cmp.Agents, compareAgent(a, base[a], treat[a], baseline, treatment))
	}
	// Largest improvement first; name breaks ties so the order is stable.
	sort.SliceStable(cmp.Agents, func(i, j int) bool {
		x, y := cmp.Agents[i], cmp.Agents[j]
		if (x.Paired > 0) != (y.Paired > 0) {
			return x.Paired > 0
		}
		if x.Delta != y.Delta {
			return x.Delta > y.Delta
		}
		return x.Agent < y.Agent
	})

	cmp.Caveats = dataCaveats(used)
	if ignored > 0 {
		cmp.Caveats = append(cmp.Caveats, fmt.Sprintf(
			"%d report(s) carried a condition other than `%s` or `%s` and were ignored.", ignored, baseline, treatment))
	}
	cmp.Caveats = append(cmp.Caveats, comparisonCaveats...)
	cmp.Caveats = append(cmp.Caveats, withScoring(standingCaveats)...)
	return cmp, nil
}

func compareAgent(name string, base, treat map[string]Cell, baseline, treatment string) AgentComparison {
	ac := AgentComparison{Agent: name}
	scenarios := map[string]bool{}
	for s := range base {
		scenarios[s] = true
	}
	for s := range treat {
		scenarios[s] = true
	}

	var baseMeans, treatMeans []float64
	for _, s := range sortedKeys(scenarios) {
		sc := ScenarioComparison{Scenario: s}
		b, hasB := base[s]
		t, hasT := treat[s]
		if hasB {
			sc.Baseline = &b
			ac.BaselineAttempts += b.Attempts
			ac.BaselineDiagnoses += b.Diagnoses
		}
		if hasT {
			sc.Treatment = &t
			ac.TreatmentAttempts += t.Attempts
			ac.TreatmentDiagnoses += t.Diagnoses
		}
		switch {
		case !hasB:
			ac.Excluded = append(ac.Excluded, fmt.Sprintf("%s: not run under `%s`", s, baseline))
		case !hasT:
			ac.Excluded = append(ac.Excluded, fmt.Sprintf("%s: not run under `%s`", s, treatment))
		case !b.Answered():
			ac.Excluded = append(ac.Excluded, fmt.Sprintf("%s: no diagnosis under `%s` (%d attempts)", s, baseline, b.Attempts))
		case !t.Answered():
			ac.Excluded = append(ac.Excluded, fmt.Sprintf("%s: no diagnosis under `%s` (%d attempts)", s, treatment, t.Attempts))
		default:
			d := round(t.MeanScore - b.MeanScore)
			sc.Delta = &d
			baseMeans = append(baseMeans, b.MeanScore)
			treatMeans = append(treatMeans, t.MeanScore)
		}
		ac.Scenarios = append(ac.Scenarios, sc)
	}

	ac.Paired = len(baseMeans)
	if ac.Paired > 0 {
		ac.BaselineMean = mean(baseMeans)
		ac.TreatmentMean = mean(treatMeans)
		ac.Delta = round(ac.TreatmentMean - ac.BaselineMean)
	}
	ac.BaselineAnswerRate = rate(ac.BaselineDiagnoses, ac.BaselineAttempts)
	ac.TreatmentAnswerRate = rate(ac.TreatmentDiagnoses, ac.TreatmentAttempts)
	return ac
}

func board(condition string, reports []orchestrator.Report) Board {
	byAgent := cells(reports)
	scenarioSet := map[string]bool{}
	for _, m := range byAgent {
		for s := range m {
			scenarioSet[s] = true
		}
	}
	b := Board{Condition: condition, Scenarios: sortedKeys(scenarioSet)}

	for agentName, m := range byAgent {
		row := Row{Agent: agentName}
		var means []float64
		for _, s := range b.Scenarios {
			c, ok := m[s]
			if !ok {
				row.Cells = append(row.Cells, Cell{Scenario: s})
				continue
			}
			row.Cells = append(row.Cells, c)
			row.ScenariosRun++
			row.Attempts += c.Attempts
			row.Diagnoses += c.Diagnoses
			if c.Answered() {
				row.ScenariosAnswered++
				means = append(means, c.MeanScore)
			}
		}
		if len(means) > 0 {
			row.MeanScore = mean(means)
		}
		row.AnswerRate = rate(row.Diagnoses, row.Attempts)
		b.Rows = append(b.Rows, row)
	}
	sort.Slice(b.Rows, func(i, j int) bool {
		x, y := b.Rows[i], b.Rows[j]
		if x.MeanScore != y.MeanScore {
			return x.MeanScore > y.MeanScore
		}
		if x.AnswerRate != y.AnswerRate {
			return x.AnswerRate > y.AnswerRate
		}
		return x.Agent < y.Agent
	})
	return b
}

// cells pools the runs of every report into agent → scenario → Cell. Runs are
// pooled and re-summarized rather than averaging report means, which would
// weigh a 1-repeat report the same as a 5-repeat one.
func cells(reports []orchestrator.Report) map[string]map[string]Cell {
	pooled := map[string]map[string][]orchestrator.RunRecord{}
	for _, r := range reports {
		if pooled[r.Agent] == nil {
			pooled[r.Agent] = map[string][]orchestrator.RunRecord{}
		}
		pooled[r.Agent][r.Scenario] = append(pooled[r.Agent][r.Scenario], r.Runs...)
	}
	out := map[string]map[string]Cell{}
	for a, scenarios := range pooled {
		out[a] = map[string]Cell{}
		for s, runs := range scenarios {
			sum := orchestrator.Summarize(runs)
			out[a][s] = Cell{
				Scenario:        s,
				Attempts:        sum.Attempts,
				Diagnoses:       sum.Diagnoses,
				BudgetExhausted: sum.BudgetExhausted,
				AnswerRate:      sum.AnswerRate,
				MeanScore:       sum.MeanScore,
				StdDevScore:     sum.StdDevScore,
			}
		}
	}
	return out
}

// validate rejects input that would make the tables quietly wrong.
func validate(reports []orchestrator.Report) error {
	if len(reports) == 0 {
		return errors.New("no bench reports given")
	}
	seen := map[string]bool{}
	for _, r := range reports {
		if r.Scenario == "" || r.Agent == "" {
			return errors.New("a report has no scenario or agent: not a bench run report")
		}
		if len(r.Runs) == 0 {
			return fmt.Errorf("report for %s × %s has no runs", r.Agent, r.Scenario)
		}
		// The same report given twice would double its runs and shrink the
		// spread without adding any evidence.
		key := strings.Join([]string{
			r.Agent, r.Scenario, r.Condition,
			r.Runs[0].Started.UTC().Format(time.RFC3339Nano), fmt.Sprint(len(r.Runs)),
		}, "|")
		if seen[key] {
			return fmt.Errorf("the report for %s × %s started %s was given more than once",
				r.Agent, r.Scenario, r.Runs[0].Started.UTC().Format(time.RFC3339))
		}
		seen[key] = true
	}
	return nil
}

// dataCaveats are the disclosures that depend on what the reports contain:
// anything that makes two cells less than like-for-like.
func dataCaveats(reports []orchestrator.Report) []string {
	hashes := map[string]map[string]bool{}  // scenario → hashes
	budgets := map[string]map[string]bool{} // agent → budgets
	models := map[string]map[string]bool{}  // agent → model provenance
	judged := 0
	for _, r := range reports {
		add(hashes, r.Scenario, short(r.ScenarioHash))
		add(budgets, r.Agent, budget(r.Budget))
		if r.Model != nil {
			add(models, r.Agent, fmt.Sprintf("%s %s ctx %d weights %s",
				r.Model.Model, r.Model.Quantization, r.Model.EffectiveNumCtx, short(r.Model.WeightsDigest)))
		}
		for _, run := range r.Runs {
			if run.Normalization != "" && run.Normalization != "json" {
				judged++
			}
		}
	}

	var out []string
	for _, s := range sortedKeys(hashes) {
		if len(hashes[s]) > 1 {
			out = append(out, fmt.Sprintf(
				"Scenario `%s` was run with %d different definitions (hashes %s). Those runs are pooled here but are not like-for-like.",
				s, len(hashes[s]), strings.Join(sortedKeys(hashes[s]), ", ")))
		}
	}
	for _, a := range sortedKeys(budgets) {
		if len(budgets[a]) > 1 {
			out = append(out, fmt.Sprintf(
				"Agent `%s` ran under %d different budgets (%s). A difference between its cells may be a budget effect.",
				a, len(budgets[a]), strings.Join(sortedKeys(budgets[a]), "; ")))
		}
	}
	for _, a := range sortedKeys(models) {
		if len(models[a]) > 1 {
			out = append(out, fmt.Sprintf(
				"Agent `%s` was served by %d different model builds (%s). Those are different subjects under one name.",
				a, len(models[a]), strings.Join(sortedKeys(models[a]), "; ")))
		}
	}
	if judged > 0 {
		out = append(out, fmt.Sprintf(
			"%d run(s) were normalized by a non-deterministic method (an LLM judge), not parsed as JSON. See the run reports.", judged))
	}
	return out
}

// withScoring appends the run report's own standing caveats, so the scoring
// rule and its limits travel with every table built from run reports.
func withScoring(own []string) []string {
	out := append([]string{}, own...)
	return append(out, orchestrator.StandingCaveats()...)
}

func filter(reports []orchestrator.Report, condition string) []orchestrator.Report {
	var out []orchestrator.Report
	for _, r := range reports {
		if r.Condition == condition {
			out = append(out, r)
		}
	}
	return out
}

func add(m map[string]map[string]bool, k, v string) {
	if m[k] == nil {
		m[k] = map[string]bool{}
	}
	m[k][v] = true
}

// sortedKeys returns a map's keys in order. Go randomizes map iteration, and a
// report must be byte-identical for the same input.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func budget(b agent.Budget) string {
	if b.MaxToolCalls == 0 && b.MaxTokens == 0 {
		return "uncapped"
	}
	return fmt.Sprintf("%d tool calls / %d tokens", b.MaxToolCalls, b.MaxTokens)
}

func short(digest string) string {
	if digest == "" {
		return "unknown"
	}
	digest = strings.TrimPrefix(digest, "sha256:")
	if len(digest) > 12 {
		return digest[:12]
	}
	return digest
}

func mean(xs []float64) float64 {
	var t float64
	for _, x := range xs {
		t += x
	}
	return t / float64(len(xs))
}

func rate(n, of int) float64 {
	if of == 0 {
		return 0
	}
	return float64(n) / float64(of)
}

// round trims floating-point residue (0.30000000000000004) from a difference,
// which would otherwise print as false precision and make JSON output
// unstable across platforms.
func round(x float64) float64 {
	const scale = 1e9
	if x < 0 {
		return -float64(int64(-x*scale+0.5)) / scale
	}
	return float64(int64(x*scale+0.5)) / scale
}
