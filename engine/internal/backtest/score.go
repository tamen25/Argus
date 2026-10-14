package backtest

import (
	"slices"
	"sort"
	"time"
)

// ScoreOptions tunes incident attribution.
type ScoreOptions struct {
	// Grace widens every incident window on both sides when attributing
	// firings: a fire shortly after the labeled end is late, not false.
	Grace time.Duration
}

// Detection is one incident a rule caught.
type Detection struct {
	IncidentID string
	// TTD is first-firing time minus incident start — how long users paged
	// AFTER impact began. Negative TTD (fired before the labeled start,
	// within grace) is reported as zero.
	TTD time.Duration
}

// Scorecard is one rule judged against the registry over covered history.
// Field semantics are the report contract:
//   - Missed: incidents with telemetry coverage the rule never fired for
//     (expected_alerts filtering applies).
//   - Unverifiable: incidents with NO overlapping coverage segment — no
//     verdict is possible and none is given (architecture rule 7).
//   - FalsePositives: firing intervals outside every incident window ± grace.
//   - PagesPerWeek: firing intervals per COVERED hour × 168 — coverage-based
//     extrapolation, reported next to Coverage so it can't pose as calendar.
//   - Flappiness: firing intervals per detected incident; 1.0 = one clean
//     page per incident.
type Scorecard struct {
	Rule           string
	Detections     []Detection
	Missed         []string
	Unverifiable   []string
	FalsePositives []Firing
	Coverage       time.Duration
	PagesPerWeek   float64
	Flappiness     float64
}

// Score judges one rule's replayed firings against the incident registry.
// Deterministic: outputs are sorted (detections and misses by incident ID
// order of the registry, false positives by fire time).
func Score(rule string, firings []Firing, incidents []Incident, segs []Segment, opts ScoreOptions) Scorecard {
	sc := Scorecard{Rule: rule, Coverage: totalCoverage(segs)}

	attributed := make([]bool, len(firings)) // firings some incident explains
	detectedIntervals := 0                   // firings during detected incidents
	for _, inc := range incidents {
		if !covered(inc, segs) {
			sc.Unverifiable = append(sc.Unverifiable, inc.ID)
			continue
		}
		during := firingsDuring(firings, inc, opts.Grace)
		// The incident explains these firings whether or not this rule was
		// expected to catch it, so none of them is a false positive.
		for _, i := range during {
			attributed[i] = true
		}
		// A rule the incident does not list in expected_alerts gets no credit
		// for firing: the incident counts as missed by this rule.
		if len(during) == 0 || !expects(inc, rule) {
			sc.Missed = append(sc.Missed, inc.ID)
			continue
		}
		// Firing before the labeled start (within grace) is not negative TTD.
		ttd := max(earliest(firings, during).Sub(inc.Start), 0)
		sc.Detections = append(sc.Detections, Detection{IncidentID: inc.ID, TTD: ttd})
		detectedIntervals += len(during)
	}
	if n := len(sc.Detections); n > 0 {
		sc.Flappiness = float64(detectedIntervals) / float64(n)
	}

	for i, f := range firings {
		if !attributed[i] {
			sc.FalsePositives = append(sc.FalsePositives, f)
		}
	}
	sort.Slice(sc.FalsePositives, func(i, j int) bool {
		return sc.FalsePositives[i].FiredAt.Before(sc.FalsePositives[j].FiredAt)
	})

	if sc.Coverage > 0 {
		perHour := float64(len(firings)) / sc.Coverage.Hours()
		sc.PagesPerWeek = perHour * 24 * 7
	}
	return sc
}

// totalCoverage is the time the segments span together.
func totalCoverage(segs []Segment) time.Duration {
	var total time.Duration
	for _, s := range segs {
		total += s.End.Sub(s.Start)
	}
	return total
}

// covered reports whether any segment overlaps the incident, so a verdict on
// it is possible at all.
func covered(inc Incident, segs []Segment) bool {
	for _, s := range segs {
		if inc.Start.Before(s.End) && inc.End.After(s.Start) {
			return true
		}
	}
	return false
}

// expects reports whether rule may claim detection credit for the incident:
// any rule may, unless the incident names its expected alerts.
func expects(inc Incident, rule string) bool {
	return len(inc.ExpectedAlerts) == 0 || slices.Contains(inc.ExpectedAlerts, rule)
}

// firingsDuring returns the indexes of the firings that began inside the
// incident window widened by grace on both sides.
func firingsDuring(firings []Firing, inc Incident, grace time.Duration) []int {
	lo, hi := inc.Start.Add(-grace), inc.End.Add(grace)
	var idx []int
	for i, f := range firings {
		if !f.FiredAt.Before(lo) && !f.FiredAt.After(hi) {
			idx = append(idx, i)
		}
	}
	return idx
}

// earliest is the first fire time among the indexed firings; idx is not empty.
func earliest(firings []Firing, idx []int) time.Time {
	first := firings[idx[0]].FiredAt
	for _, i := range idx[1:] {
		if firings[i].FiredAt.Before(first) {
			first = firings[i].FiredAt
		}
	}
	return first
}
