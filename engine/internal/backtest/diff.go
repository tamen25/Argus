package backtest

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// DiffOptions are the CI-gate budgets. Zero values disable a gate; losing a
// detection always regresses.
type DiffOptions struct {
	// MaxTTDRegression trips when any incident's set-level TTD worsens by
	// more than this.
	MaxTTDRegression time.Duration
	// MaxPagesPerWeek trips when set B's total pages/week exceeds it.
	MaxPagesPerWeek float64
}

// TTDChange is one incident's set-level TTD under each rule set.
type TTDChange struct {
	IncidentID string
	A, B       time.Duration
}

// Diff is the A-vs-B verdict. Set-level: an incident counts as detected if
// ANY rule in the set detected it; pages/week totals sum over rules.
type Diff struct {
	LostDetections   []string
	GainedDetections []string
	TTDChanges       []TTDChange
	PagesA, PagesB   float64
	Regression       bool
	Reasons          []string
	Coverage         time.Duration
}

// DiffReports compares two backtest reports produced over the same window.
// Reports with different coverage are not comparable — refused, not fudged
// (architecture rule 7).
func DiffReports(a, b Report, opts DiffOptions) (Diff, error) {
	if a.Coverage != b.Coverage {
		return Diff{}, fmt.Errorf("reports are not comparable: coverage %s vs %s — run both sets over the same window in one invocation", a.Coverage, b.Coverage)
	}
	d := Diff{Coverage: a.Coverage, PagesA: totalPages(a), PagesB: totalPages(b)}

	detA, detB := setDetections(a), setDetections(b)
	for _, id := range sortedKeys(detA) {
		if tb, ok := detB[id]; ok {
			d.TTDChanges = append(d.TTDChanges, TTDChange{IncidentID: id, A: detA[id], B: tb})
		} else {
			d.LostDetections = append(d.LostDetections, id)
		}
	}
	for _, id := range sortedKeys(detB) {
		if _, ok := detA[id]; !ok {
			d.GainedDetections = append(d.GainedDetections, id)
		}
	}

	d.Reasons = gateReasons(d, opts)
	d.Regression = len(d.Reasons) > 0
	return d, nil
}

// gateReasons lists every budget the diff breaks. Losing a detection always
// counts; the TTD and pages budgets apply only when set.
func gateReasons(d Diff, opts DiffOptions) []string {
	var reasons []string
	if len(d.LostDetections) > 0 {
		reasons = append(reasons, fmt.Sprintf("detections lost: %s", strings.Join(d.LostDetections, ", ")))
	}
	if opts.MaxTTDRegression > 0 {
		for _, c := range d.TTDChanges {
			if c.B-c.A > opts.MaxTTDRegression {
				reasons = append(reasons, fmt.Sprintf("TTD for %s regressed %s → %s (budget %s)", c.IncidentID, c.A, c.B, opts.MaxTTDRegression))
			}
		}
	}
	if opts.MaxPagesPerWeek > 0 && d.PagesB > opts.MaxPagesPerWeek {
		reasons = append(reasons, fmt.Sprintf("pages/week %.1f exceeds budget %.1f", d.PagesB, opts.MaxPagesPerWeek))
	}
	return reasons
}

// totalPages sums pages/week over a report's rules.
func totalPages(r Report) float64 {
	var total float64
	for _, sc := range r.Rules {
		total += sc.PagesPerWeek
	}
	return total
}

// sortedKeys returns a map's keys in order, so the diff is the same on every
// run (Go randomizes map iteration).
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// setDetections is incident → best (earliest) TTD across the set's rules.
func setDetections(r Report) map[string]time.Duration {
	out := map[string]time.Duration{}
	for _, sc := range r.Rules {
		for _, det := range sc.Detections {
			if cur, ok := out[det.IncidentID]; !ok || det.TTD < cur {
				out[det.IncidentID] = det.TTD
			}
		}
	}
	return out
}

// RenderDiffMarkdown renders the side-by-side verdict. Deterministic.
func RenderDiffMarkdown(d Diff) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Argus Backtest Diff\n\n")
	verdict := "no regression"
	if d.Regression {
		verdict = "**REGRESSION**"
	}
	fmt.Fprintf(&b, "- Verdict: %s\n", verdict)
	fmt.Fprintf(&b, "- Coverage (both sets): %s\n", d.Coverage)
	fmt.Fprintf(&b, "- Pages/week: %.1f → %.1f\n\n", d.PagesA, d.PagesB)

	for _, r := range d.Reasons {
		fmt.Fprintf(&b, "- ❌ %s\n", r)
	}
	for _, id := range d.GainedDetections {
		fmt.Fprintf(&b, "- ✅ new detection: %s\n", id)
	}
	if len(d.Reasons)+len(d.GainedDetections) > 0 {
		fmt.Fprintf(&b, "\n")
	}

	if len(d.TTDChanges) > 0 {
		fmt.Fprintf(&b, "| Incident | TTD A | TTD B |\n|---|---:|---:|\n")
		for _, c := range d.TTDChanges {
			fmt.Fprintf(&b, "| %s | %s | %s |\n", c.IncidentID, c.A, c.B)
		}
		fmt.Fprintf(&b, "\n")
	}

	fmt.Fprintf(&b, "## Fidelity caveats\n\n- %s\n", standingReplayCaveat)
	return b.String()
}
