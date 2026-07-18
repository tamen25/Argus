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
	sc := Scorecard{Rule: rule}
	for _, s := range segs {
		sc.Coverage += s.End.Sub(s.Start)
	}

	expected := func(inc Incident) bool {
		return len(inc.ExpectedAlerts) == 0 || slices.Contains(inc.ExpectedAlerts, rule)
	}
	covered := func(inc Incident) bool {
		for _, s := range segs {
			if inc.Start.Before(s.End) && inc.End.After(s.Start) {
				return true
			}
		}
		return false
	}
	inWindow := func(f Firing, inc Incident) bool {
		lo, hi := inc.Start.Add(-opts.Grace), inc.End.Add(opts.Grace)
		return !f.FiredAt.Before(lo) && !f.FiredAt.After(hi)
	}

	attributed := make([]bool, len(firings))
	for _, inc := range incidents {
		if !covered(inc) {
			sc.Unverifiable = append(sc.Unverifiable, inc.ID)
			continue
		}
		var first *Firing
		var intervals int
		for i := range firings {
			if !inWindow(firings[i], inc) {
				continue
			}
			// the incident explains the firing regardless of expected_alerts
			attributed[i] = true
			intervals++
			if first == nil || firings[i].FiredAt.Before(first.FiredAt) {
				first = &firings[i]
			}
		}
		if first == nil || !expected(inc) {
			sc.Missed = append(sc.Missed, inc.ID)
			continue
		}
		ttd := first.FiredAt.Sub(inc.Start)
		if ttd < 0 {
			ttd = 0
		}
		sc.Detections = append(sc.Detections, Detection{IncidentID: inc.ID, TTD: ttd})
		sc.Flappiness += float64(intervals)
	}
	if n := len(sc.Detections); n > 0 {
		sc.Flappiness /= float64(n)
	} else {
		sc.Flappiness = 0
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
