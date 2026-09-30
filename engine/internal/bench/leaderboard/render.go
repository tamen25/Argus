package leaderboard

import (
	"encoding/json"
	"fmt"
	"strings"
)

// unlabeled is how a board of runs with no condition label is titled.
const unlabeled = "(unlabeled)"

// RenderJSON renders a Leaderboard or a Comparison as indented JSON.
func RenderJSON(v any) ([]byte, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// RenderLeaderboardMarkdown renders one table per condition. Deterministic:
// byte-identical for the same input.
func RenderLeaderboardMarkdown(lb Leaderboard) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Argus Bench — leaderboard\n\n")
	for _, board := range lb.Boards {
		name := board.Condition
		if name == "" {
			name = unlabeled
		} else {
			name = "`" + name + "`"
		}
		fmt.Fprintf(&b, "## Telemetry condition: %s\n\n", name)

		fmt.Fprintf(&b, "| # | Agent | Mean score | Scenarios answered | Runs answered |")
		for _, s := range board.Scenarios {
			fmt.Fprintf(&b, " `%s` |", s)
		}
		fmt.Fprintf(&b, "\n|---:|---|---:|---:|---:|%s\n", strings.Repeat("---:|", len(board.Scenarios)))

		for i, row := range board.Rows {
			meanScore := "—"
			if row.ScenariosAnswered > 0 {
				meanScore = fmt.Sprintf("**%.2f**", row.MeanScore)
			}
			fmt.Fprintf(&b, "| %d | `%s` | %s | %d/%d | %d/%d (%.0f%%) |",
				i+1, row.Agent, meanScore, row.ScenariosAnswered, row.ScenariosRun,
				row.Diagnoses, row.Attempts, row.AnswerRate*100)
			for _, c := range row.Cells {
				fmt.Fprintf(&b, " %s |", cellText(&c))
			}
			fmt.Fprintf(&b, "\n")
		}
		fmt.Fprintf(&b, "\n")
	}
	fmt.Fprintf(&b, "A cell is mean score ± spread (answered runs/attempts). "+
		"`no diagnosis` means every attempt failed or ran out of budget; `·` means not run.\n\n")
	writeCaveats(&b, lb.Caveats)
	return b.String()
}

// RenderComparisonMarkdown renders the baseline-vs-treatment report.
func RenderComparisonMarkdown(c Comparison) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Argus Bench — `%s` vs `%s`\n\n", c.Baseline, c.Treatment)
	fmt.Fprintf(&b, "Baseline: `%s`. Treatment: `%s`. Δ is treatment − baseline.\n\n", c.Baseline, c.Treatment)

	fmt.Fprintf(&b, "## Summary\n\n")
	fmt.Fprintf(&b, "| Agent | Scenarios compared | `%s` | `%s` | Δ | Runs answered (`%s`) | Runs answered (`%s`) |\n",
		c.Baseline, c.Treatment, c.Baseline, c.Treatment)
	fmt.Fprintf(&b, "|---|---:|---:|---:|---:|---:|---:|\n")
	for _, a := range c.Agents {
		base, treat, delta := "—", "—", "—"
		if a.Paired > 0 {
			base = fmt.Sprintf("%.2f", a.BaselineMean)
			treat = fmt.Sprintf("%.2f", a.TreatmentMean)
			delta = fmt.Sprintf("**%+.2f**", a.Delta)
		}
		fmt.Fprintf(&b, "| `%s` | %d of %d | %s | %s | %s | %s | %s |\n",
			a.Agent, a.Paired, len(a.Scenarios), base, treat, delta,
			answered(a.BaselineDiagnoses, a.BaselineAttempts, a.BaselineAnswerRate),
			answered(a.TreatmentDiagnoses, a.TreatmentAttempts, a.TreatmentAnswerRate))
	}
	fmt.Fprintf(&b, "\n")

	for _, a := range c.Agents {
		fmt.Fprintf(&b, "## `%s`\n\n", a.Agent)
		fmt.Fprintf(&b, "| Scenario | `%s` | `%s` | Δ |\n", c.Baseline, c.Treatment)
		fmt.Fprintf(&b, "|---|---:|---:|---:|\n")
		for _, s := range a.Scenarios {
			delta := "—"
			if s.Delta != nil {
				delta = fmt.Sprintf("%+.2f", *s.Delta)
			}
			fmt.Fprintf(&b, "| `%s` | %s | %s | %s |\n", s.Scenario, cellText(s.Baseline), cellText(s.Treatment), delta)
		}
		fmt.Fprintf(&b, "\n")
		if len(a.Excluded) > 0 {
			fmt.Fprintf(&b, "Excluded from this agent's means:\n\n")
			for _, e := range a.Excluded {
				fmt.Fprintf(&b, "- %s\n", e)
			}
			fmt.Fprintf(&b, "\n")
		}
	}

	writeCaveats(&b, c.Caveats)
	return b.String()
}

// cellText renders one cell. A nil cell, or one with no attempts, was not run.
func cellText(c *Cell) string {
	switch {
	case c == nil || c.Attempts == 0:
		return "·"
	case !c.Answered():
		return fmt.Sprintf("no diagnosis (0/%d)", c.Attempts)
	default:
		return fmt.Sprintf("%.2f ± %.2f (%d/%d)", c.MeanScore, c.StdDevScore, c.Diagnoses, c.Attempts)
	}
}

func answered(diagnoses, attempts int, rate float64) string {
	if attempts == 0 {
		return "·"
	}
	return fmt.Sprintf("%d/%d (%.0f%%)", diagnoses, attempts, rate*100)
}

func writeCaveats(b *strings.Builder, caveats []string) {
	fmt.Fprintf(b, "## Method and caveats\n\n")
	for _, c := range caveats {
		fmt.Fprintf(b, "- %s\n", c)
	}
}
