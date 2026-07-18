package backtest

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Report is the full backtest verdict: every rule's scorecard over the same
// covered window, plus the fidelity caveats that applied. Rendered to
// Markdown for humans and JSON for machines/CI (both golden-tested).
type Report struct {
	GeneratedAt time.Time     `json:"generated_at"`
	From        time.Time     `json:"from"`
	To          time.Time     `json:"to"`
	Step        time.Duration `json:"step_seconds"`
	Coverage    time.Duration `json:"coverage_seconds"`
	Segments    int           `json:"segments"`
	Rules       []Scorecard   `json:"rules"`
	// Caveats carry the fidelity notes (architecture rule 7); the standing
	// replay caveat is always rendered even when this is empty.
	Caveats []string `json:"caveats,omitempty"`
}

// standingReplayCaveat can never be stripped from a rendering.
const standingReplayCaveat = "Replay is not re-execution: stepped instant queries differ from live ruler evaluation in staleness, lookback, and alignment (docs/backtest-fidelity.md)."

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
	fmt.Fprintf(&b, "# Argus Backtest\n\n")
	fmt.Fprintf(&b, "- Generated: %s\n", r.GeneratedAt.Format(time.RFC3339))
	fmt.Fprintf(&b, "- Window: %s → %s · step %s\n", r.From.Format(time.RFC3339), r.To.Format(time.RFC3339), r.Step)
	fmt.Fprintf(&b, "- **Coverage: %s of %s (%d segment(s))**\n\n", r.Coverage, r.To.Sub(r.From), r.Segments)

	for _, sc := range r.Rules {
		fmt.Fprintf(&b, "## %s\n\n", sc.Rule)
		fmt.Fprintf(&b, "| Detected | Missed | Unverifiable | False positives | Pages/week* | Flappiness |\n")
		fmt.Fprintf(&b, "|---:|---:|---:|---:|---:|---:|\n")
		fmt.Fprintf(&b, "| %d | %d | %d | %d | %.1f | %.1f |\n\n",
			len(sc.Detections), len(sc.Missed), len(sc.Unverifiable), len(sc.FalsePositives), sc.PagesPerWeek, sc.Flappiness)

		if len(sc.Detections) > 0 {
			fmt.Fprintf(&b, "| Incident | TTD |\n|---|---:|\n")
			for _, d := range sc.Detections {
				fmt.Fprintf(&b, "| %s | %s |\n", d.IncidentID, d.TTD)
			}
			fmt.Fprintf(&b, "\n")
		}
		for _, id := range sc.Missed {
			fmt.Fprintf(&b, "- **missed**: %s\n", id)
		}
		for _, id := range sc.Unverifiable {
			fmt.Fprintf(&b, "- unverifiable (no telemetry coverage): %s\n", id)
		}
		for _, f := range sc.FalsePositives {
			fmt.Fprintf(&b, "- false positive: %s fired %s\n", f.Series, f.FiredAt.Format(time.RFC3339))
		}
		if len(sc.Missed)+len(sc.Unverifiable)+len(sc.FalsePositives) > 0 {
			fmt.Fprintf(&b, "\n")
		}
	}

	fmt.Fprintf(&b, "*Pages/week extrapolates firing intervals over covered time only.\n\n")
	fmt.Fprintf(&b, "## Fidelity caveats\n\n")
	fmt.Fprintf(&b, "- %s\n", standingReplayCaveat)
	for _, c := range r.Caveats {
		fmt.Fprintf(&b, "- %s\n", c)
	}
	return b.String()
}
