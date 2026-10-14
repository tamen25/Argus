package backtest

import (
	"fmt"
	"slices"
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
	// CaveatsA and CaveatsB are each set's fidelity caveats (synthesized or
	// skipped rules, coverage), carried into the diff so a gate result is
	// never read without them.
	CaveatsA, CaveatsB []string
}

// DiffReports compares two backtest reports produced over the same history.
// Reports over different windows, steps or covered segments are not
// comparable, and are refused, not fudged (architecture rule 7). Comparing
// only the total covered time, as before, accepted two different windows
// that happened to have the same amount of coverage. A report with no
// coverage at all judged nothing, and is refused too: a CI gate must not
// pass on no evidence.
func DiffReports(a, b Report, opts DiffOptions) (Diff, error) {
	if err := comparable(a, b); err != nil {
		return Diff{}, err
	}
	d := Diff{
		Coverage: a.Coverage, PagesA: totalPages(a), PagesB: totalPages(b),
		CaveatsA: a.Caveats, CaveatsB: b.Caveats,
	}

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

// comparable says why two reports cannot be compared, or nil.
func comparable(a, b Report) error {
	const hint = " — run both sets in one `backtest diff`, which replays them over the same history"
	switch {
	case !a.From.Equal(b.From) || !a.To.Equal(b.To):
		return fmt.Errorf("reports are not comparable: windows %s–%s and %s–%s differ%s",
			a.From.Format(time.RFC3339), a.To.Format(time.RFC3339), b.From.Format(time.RFC3339), b.To.Format(time.RFC3339), hint)
	case a.Step != b.Step:
		return fmt.Errorf("reports are not comparable: steps %s and %s differ%s", a.Step, b.Step, hint)
	case !slices.Equal(a.Spans, b.Spans) || a.Coverage != b.Coverage:
		return fmt.Errorf("reports are not comparable: they cover different segments of the window (%s and %s)%s", a.Coverage, b.Coverage, hint)
	case a.Coverage == 0:
		return fmt.Errorf("no telemetry coverage in the window: there is no history to compare the rule sets on")
	}
	return nil
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

	fmt.Fprintf(&b, "## Fidelity caveats\n\n- %s\n- %s\n", standingReplayCaveat, standingCoverageCaveat)
	for _, set := range []struct {
		name    string
		caveats []string
	}{{"A", d.CaveatsA}, {"B", d.CaveatsB}} {
		for _, c := range set.caveats {
			fmt.Fprintf(&b, "- Set %s: %s\n", set.name, c)
		}
	}
	return b.String()
}
