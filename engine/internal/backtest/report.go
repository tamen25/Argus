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
//
// The in-memory struct keeps time.Duration for the CLI and diff logic;
// MarshalJSON below emits the wire shape (durations as seconds, snake_case
// throughout, slices never null) so the plugin and CI consume one consistent
// contract.
type Report struct {
	GeneratedAt time.Time
	From        time.Time
	To          time.Time
	Step        time.Duration
	Coverage    time.Duration
	Segments    int
	Rules       []Scorecard
	// Caveats carry the fidelity notes (architecture rule 7); the standing
	// replay caveat is always rendered even when this is empty.
	Caveats []string
}

// MarshalJSON emits the backtest wire contract: RFC3339 times, durations as
// seconds (the field names say so), snake_case keys at every level, and
// non-nil slices so strict consumers never hit `.length` of null (the bug
// that once broke the Spend page).
func (r Report) MarshalJSON() ([]byte, error) {
	type detectionJSON struct {
		IncidentID string  `json:"incident_id"`
		TTDSeconds float64 `json:"ttd_seconds"`
	}
	type firingJSON struct {
		Series          string `json:"series"`
		FiredAt         string `json:"fired_at"`
		ResolvedAt      string `json:"resolved_at,omitempty"`
		UnresolvedAtEnd bool   `json:"unresolved_at_end"`
	}
	type scorecardJSON struct {
		Rule           string          `json:"rule"`
		Detections     []detectionJSON `json:"detections"`
		Missed         []string        `json:"missed"`
		Unverifiable   []string        `json:"unverifiable"`
		FalsePositives []firingJSON    `json:"false_positives"`
		CoverageSecs   float64         `json:"coverage_seconds"`
		PagesPerWeek   float64         `json:"pages_per_week"`
		Flappiness     float64         `json:"flappiness"`
	}
	type reportJSON struct {
		GeneratedAt     string          `json:"generated_at"`
		From            string          `json:"from"`
		To              string          `json:"to"`
		StepSeconds     float64         `json:"step_seconds"`
		CoverageSeconds float64         `json:"coverage_seconds"`
		WindowSeconds   float64         `json:"window_seconds"`
		Segments        int             `json:"segments"`
		Rules           []scorecardJSON `json:"rules"`
		Caveats         []string        `json:"caveats"`
	}

	rules := make([]scorecardJSON, 0, len(r.Rules))
	for _, sc := range r.Rules {
		dets := make([]detectionJSON, 0, len(sc.Detections))
		for _, d := range sc.Detections {
			dets = append(dets, detectionJSON{IncidentID: d.IncidentID, TTDSeconds: d.TTD.Seconds()})
		}
		fps := make([]firingJSON, 0, len(sc.FalsePositives))
		for _, f := range sc.FalsePositives {
			fj := firingJSON{Series: f.Series, FiredAt: f.FiredAt.UTC().Format(time.RFC3339)}
			if f.ResolvedAt.IsZero() {
				fj.UnresolvedAtEnd = true
			} else {
				fj.ResolvedAt = f.ResolvedAt.UTC().Format(time.RFC3339)
			}
			fps = append(fps, fj)
		}
		rules = append(rules, scorecardJSON{
			Rule:           sc.Rule,
			Detections:     dets,
			Missed:         nonNil(sc.Missed),
			Unverifiable:   nonNil(sc.Unverifiable),
			FalsePositives: fps,
			CoverageSecs:   sc.Coverage.Seconds(),
			PagesPerWeek:   sc.PagesPerWeek,
			Flappiness:     sc.Flappiness,
		})
	}
	return json.Marshal(reportJSON{
		GeneratedAt:     r.GeneratedAt.UTC().Format(time.RFC3339),
		From:            r.From.UTC().Format(time.RFC3339),
		To:              r.To.UTC().Format(time.RFC3339),
		StepSeconds:     r.Step.Seconds(),
		CoverageSeconds: r.Coverage.Seconds(),
		WindowSeconds:   r.To.Sub(r.From).Seconds(),
		Segments:        r.Segments,
		Rules:           rules,
		Caveats:         nonNil(r.Caveats),
	})
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
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
