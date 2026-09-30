package orchestrator

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// standingBenchCaveats can never be stripped from a rendering. A bench number
// without its budget and normalization method is not a reportable result
// (architecture rule 7).
var standingBenchCaveats = []string{
	"Score = (1−w)·entity agreement + w·category match, less a penalty per decoy named, clamped to [0,1]; it is zero if the scenario required cited evidence and none was given. Deterministic and recomputable by hand from the runs table of a run report.",
	"Evidence is checked for presence and well-formedness, NOT for truth: verifying an observation would mean re-running the agent's queries. A fabricated citation passes this check — cited telemetry is a floor on effort, not proof of correctness.",
	"An agent's prose is recorded but never scored.",
	"Runs that produced no diagnosis (agent error or exhausted budget) are counted separately and excluded from the means — they are not scored as zero. Read the means together with the answered rate.",
	"Budgets bound what an agent may spend; a low score under a tight budget is a budget result, not only a capability result.",
}

// RenderReportJSON renders the report as indented JSON (CI-friendly).
func RenderReportJSON(r Report) ([]byte, error) {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// RenderReportMarkdown renders the report for humans. Deterministic:
// byte-identical for the same Report.
func RenderReportMarkdown(r Report) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Argus Bench\n\n")
	fmt.Fprintf(&b, "- Scenario: `%s`\n", r.Scenario)
	fmt.Fprintf(&b, "- Agent: `%s`\n", r.Agent)
	fmt.Fprintf(&b, "- Scenario hash: `%s`\n", r.ScenarioHash)
	if r.EnvDigest != "" {
		fmt.Fprintf(&b, "- Environment: `%s`\n", r.EnvDigest)
	}
	if r.Condition != "" {
		fmt.Fprintf(&b, "- Telemetry condition: `%s`\n", r.Condition)
	}
	if n := len(r.CategoriesOffered); n > 0 {
		fmt.Fprintf(&b, "- Fault categories offered: %d (`%s`)\n", n, strings.Join(r.CategoriesOffered, "`, `"))
	} else {
		fmt.Fprintf(&b, "- Fault categories offered: **none** — the agent was not shown the category list"+
			" and could match the ground-truth category only by guessing its exact name\n")
	}
	fmt.Fprintf(&b, "- Seed: %d\n", r.Seed)
	fmt.Fprintf(&b, "- Budget: %s\n", budgetString(r))
	if m := r.Model; m != nil {
		if m.Endpoint != "" {
			fmt.Fprintf(&b, "- Model: `%s` served at `%s`\n", m.Model, m.Endpoint)
		} else {
			fmt.Fprintf(&b, "- Model: `%s`\n", m.Model)
		}
	}
	fmt.Fprintf(&b, "\n")

	s := r.Summary
	fmt.Fprintf(&b, "## Summary\n\n")
	fmt.Fprintf(&b, "| Score (mean ± sd) | Answered | Entity score | Category match | Uncited | Budget exhausted | Mean tool calls | Mean tokens |\n")
	fmt.Fprintf(&b, "|---:|---:|---:|---:|---:|---:|---:|---:|\n")
	fmt.Fprintf(&b, "| **%.2f ± %.2f** | %d/%d (%.0f%%) | %.2f ± %.2f | %.0f%% | %.0f%% | %d | %.1f | %.0f |\n\n",
		s.MeanScore, s.StdDevScore,
		s.Diagnoses, s.Attempts, s.AnswerRate*100,
		s.MeanEntityScore, s.StdDevEntityScore, s.CategoryMatchRate*100,
		s.EvidenceMissingRate*100,
		s.BudgetExhausted, s.MeanToolCalls, s.MeanTokens)

	fmt.Fprintf(&b, "## Runs\n\n")
	fmt.Fprintf(&b, "| # | Score | Entity | Category | Decoys | Evidence | Normalization | Tool calls | Tool errors | Tokens | Outcome |\n")
	fmt.Fprintf(&b, "|---:|---:|---:|---|---:|---|---|---:|---:|---:|---|\n")
	for _, run := range r.Runs {
		score, ent, cat, decoys, ev := "—", "—", "—", "—", "—"
		if run.Score != nil {
			score = fmt.Sprintf("%.2f", run.Score.Score)
			ent = fmt.Sprintf("%.2f", run.Score.EntityScore)
			cat = categoryCell(run.Score.CategoryMatch, run.Score.Category)
			decoys = fmt.Sprintf("%d", len(run.Score.DecoysNamed))
			ev = evidenceCell(run.Score.EvidenceCount, run.Score.CitedSignals, run.Score.EvidenceMissing,
				run.Score.MalformedEvidence)
		}
		fmt.Fprintf(&b, "| %d | %s | %s | %s | %s | %s | %s | %d | %d | %d | %s |\n",
			run.Repeat, score, ent, cat, decoys, ev, dash(run.Normalization),
			run.Usage.ToolCalls, run.Usage.ToolErrors, run.Usage.Tokens, outcome(run))
	}
	fmt.Fprintf(&b, "\n")

	if d := decoyTally(r); len(d) > 0 {
		fmt.Fprintf(&b, "Decoys named (plausible-but-wrong entities asserted): %s\n\n", strings.Join(d, ", "))
	}

	if miss := missedEntities(r); len(miss) > 0 {
		fmt.Fprintf(&b, "Most-missed ground-truth entities: %s\n\n", strings.Join(miss, ", "))
	}

	if lines := toolUse(r); len(lines) > 0 {
		fmt.Fprintf(&b, "## Tool use\n\n")
		for _, l := range lines {
			fmt.Fprintf(&b, "- %s\n", l)
		}
		fmt.Fprintf(&b, "\n")
	}

	fmt.Fprintf(&b, "## Method and caveats\n\n")
	for _, m := range normalizationMethods(r) {
		fmt.Fprintf(&b, "- Normalization used: **%s**%s\n", m, methodNote(m))
	}
	if calls, errs := toolErrorTotals(r); errs > 0 {
		fmt.Fprintf(&b, "- %d of %d tool calls returned an error to the agent (a rejected query, or a backend that did not answer). "+
			"Read `tool_log` in the JSON report before attributing a low score to the agent alone.\n", errs, calls)
	}
	for _, c := range standingBenchCaveats {
		fmt.Fprintf(&b, "- %s\n", c)
	}
	return b.String()
}

// StandingCaveats returns the caveats every rendering of bench results must
// carry, for reports built from run reports (the leaderboard and comparison).
func StandingCaveats() []string {
	return append([]string{}, standingBenchCaveats...)
}

// categoryCell shows what the agent said when it did not match: "no" alone
// hides whether the answer was a near miss or a different fault entirely.
func categoryCell(match bool, given string) string {
	if match || given == "" {
		return boolMark(match)
	}
	return fmt.Sprintf("no (`%s`)", given)
}

// toolUse summarizes each run's tool log: which tools, how often, how many
// errors. Runs without a log (no tool use, or an agent Argus cannot observe)
// are omitted.
func toolUse(r Report) []string {
	var out []string
	for _, run := range r.Runs {
		if len(run.ToolLog) == 0 {
			continue
		}
		count, errs := map[string]int{}, map[string]int{}
		for _, c := range run.ToolLog {
			count[c.Tool]++
			if c.Error != "" {
				errs[c.Tool]++
			}
		}
		names := make([]string, 0, len(count))
		for n := range count {
			names = append(names, n)
		}
		sort.Strings(names)
		parts := make([]string, 0, len(names))
		for _, n := range names {
			p := fmt.Sprintf("`%s` ×%d", n, count[n])
			if errs[n] > 0 {
				p += fmt.Sprintf(" (%d failed)", errs[n])
			}
			parts = append(parts, p)
		}
		out = append(out, fmt.Sprintf("Run %d: %s", run.Repeat, strings.Join(parts, ", ")))
	}
	return out
}

func toolErrorTotals(r Report) (calls, errs int) {
	for _, run := range r.Runs {
		calls += run.Usage.ToolCalls
		errs += run.Usage.ToolErrors
	}
	return calls, errs
}

func budgetString(r Report) string {
	parts := []string{}
	if r.Budget.MaxToolCalls > 0 {
		parts = append(parts, fmt.Sprintf("%d tool calls", r.Budget.MaxToolCalls))
	}
	if r.Budget.MaxTokens > 0 {
		parts = append(parts, fmt.Sprintf("%d tokens", r.Budget.MaxTokens))
	}
	if len(parts) == 0 {
		return "**uncapped** (no tool-call or token limit was enforced)"
	}
	return strings.Join(parts, " / ") + " per run"
}

func outcome(r RunRecord) string {
	switch {
	case r.BudgetExhausted && r.BudgetCap != "":
		return "budget exhausted (" + r.BudgetCap + ")"
	case r.BudgetExhausted:
		return "budget exhausted"
	case r.Error != "":
		return "error: " + firstLine(r.Error)
	default:
		return "diagnosed"
	}
}

// normalizationMethods lists the distinct methods actually used, sorted for
// deterministic output. A report must show when a non-deterministic step ran.
func normalizationMethods(r Report) []string {
	seen := map[string]bool{}
	for _, run := range r.Runs {
		if run.Normalization != "" {
			seen[run.Normalization] = true
		}
	}
	out := make([]string, 0, len(seen))
	for m := range seen {
		out = append(out, m)
	}
	sort.Strings(out)
	return out
}

func methodNote(method string) string {
	if method == "llm-judge" {
		return " — a model mapped free-form agent output into the scored schema; this step is not deterministic."
	}
	return " — deterministic."
}

// missedEntities lists ground-truth entities the agent failed to name, most
// frequent first, so a report shows what was consistently missed.
func missedEntities(r Report) []string {
	count := map[string]int{}
	for _, run := range r.Runs {
		if run.Score == nil {
			continue
		}
		for _, e := range run.Score.Missed {
			count[entityLabel(e.Kind, e.Namespace, e.Name)]++
		}
	}
	out := make([]string, 0, len(count))
	for k := range count {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool {
		if count[out[i]] != count[out[j]] {
			return count[out[i]] > count[out[j]]
		}
		return out[i] < out[j]
	})
	for i, k := range out {
		out[i] = fmt.Sprintf("%s (%d/%d)", k, count[k], len(r.Runs))
	}
	return out
}

// evidenceCell renders what the agent cited: the count plus which signals, or an
// explicit marker when the scenario required evidence and got none.
// evidenceCell renders what the agent validly cited, flagging citations that
// did not count so an agent that tried and got the format wrong is visible
// rather than indistinguishable from one that never tried.
func evidenceCell(count int, signals []string, missing bool, malformed int) string {
	cell := fmt.Sprintf("%d", count)
	switch {
	case missing:
		cell = "**none**"
	case len(signals) > 0:
		cell = fmt.Sprintf("%d (%s)", count, strings.Join(signals, ", "))
	}
	if malformed > 0 {
		cell += fmt.Sprintf(" +%d malformed", malformed)
	}
	return cell
}

// decoyTally lists decoys the agent asserted, most frequent first, so a report
// shows which wrong attribution an agent keeps reaching for.
func decoyTally(r Report) []string {
	count := map[string]int{}
	for _, run := range r.Runs {
		if run.Score == nil {
			continue
		}
		for _, e := range run.Score.DecoysNamed {
			count[entityLabel(e.Kind, e.Namespace, e.Name)]++
		}
	}
	out := make([]string, 0, len(count))
	for k := range count {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool {
		if count[out[i]] != count[out[j]] {
			return count[out[i]] > count[out[j]]
		}
		return out[i] < out[j]
	})
	for i, k := range out {
		out[i] = fmt.Sprintf("%s (%d/%d)", k, count[k], len(r.Runs))
	}
	return out
}

func entityLabel(kind, ns, name string) string {
	if ns == "" {
		return kind + "/" + name
	}
	return kind + "/" + ns + "/" + name
}

func boolMark(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func dash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}
