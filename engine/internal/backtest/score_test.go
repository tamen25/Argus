package backtest_test

import (
	"testing"
	"time"

	"github.com/tamen25/Argus/engine/internal/backtest"
)

func ts(h, m int) time.Time { return time.Date(2026, 7, 18, h, m, 0, 0, time.UTC) }

// Score judges one rule's replayed firings against the incident registry:
// detections with TTD, misses, unverifiable incidents (no telemetry
// coverage), false positives (fires outside every incident window ± grace),
// pages/week, and flappiness. Definitions live on the Scorecard fields —
// they are part of the report contract.
func TestScoreDetectionAndTTD(t *testing.T) {
	incidents := []backtest.Incident{
		{ID: "inc-1", Start: ts(5, 16), End: ts(5, 30), Services: []string{"ad"}},
	}
	firings := []backtest.Firing{
		{Rule: "HighErr", Series: `{service="ad"}`, ActiveAt: ts(5, 18), FiredAt: ts(5, 23), ResolvedAt: ts(5, 35)},
	}
	segs := []backtest.Segment{{Start: ts(5, 0), End: ts(6, 0)}}

	sc := backtest.Score("HighErr", firings, incidents, segs, backtest.ScoreOptions{Grace: 5 * time.Minute})

	if len(sc.Detections) != 1 {
		t.Fatalf("detections = %+v, want 1", sc.Detections)
	}
	d := sc.Detections[0]
	if d.IncidentID != "inc-1" {
		t.Errorf("incident = %q", d.IncidentID)
	}
	// TTD is firing time minus incident start: 05:23 - 05:16 = 7m
	if d.TTD != 7*time.Minute {
		t.Errorf("TTD = %v, want 7m", d.TTD)
	}
	if len(sc.Missed) != 0 || len(sc.Unverifiable) != 0 || len(sc.FalsePositives) != 0 {
		t.Errorf("missed=%v unverifiable=%v fps=%v, want none", sc.Missed, sc.Unverifiable, sc.FalsePositives)
	}
}

// An incident with telemetry coverage but no firing is MISSED; an incident
// with no overlapping coverage segment is UNVERIFIABLE, never a miss — the
// spike's own first baseline (lost with the WAL) is the canonical case.
func TestScoreMissedVsUnverifiable(t *testing.T) {
	incidents := []backtest.Incident{
		{ID: "covered-and-missed", Start: ts(5, 0), End: ts(5, 10)},
		{ID: "no-telemetry", Start: ts(9, 0), End: ts(9, 10)},
	}
	segs := []backtest.Segment{{Start: ts(4, 0), End: ts(6, 0)}}

	sc := backtest.Score("HighErr", nil, incidents, segs, backtest.ScoreOptions{})

	if len(sc.Missed) != 1 || sc.Missed[0] != "covered-and-missed" {
		t.Errorf("missed = %v", sc.Missed)
	}
	if len(sc.Unverifiable) != 1 || sc.Unverifiable[0] != "no-telemetry" {
		t.Errorf("unverifiable = %v", sc.Unverifiable)
	}
}

// A firing outside every incident window ± grace is a false positive; one
// inside the grace margin is not.
func TestScoreFalsePositivesRespectGrace(t *testing.T) {
	incidents := []backtest.Incident{
		{ID: "inc-1", Start: ts(5, 16), End: ts(5, 30)},
	}
	firings := []backtest.Firing{
		// 05:33 with 5m grace after 05:30 end → attributed, not FP
		{Rule: "R", Series: "a", FiredAt: ts(5, 33), ResolvedAt: ts(5, 40)},
		// 05:50 → outside grace → FP
		{Rule: "R", Series: "b", FiredAt: ts(5, 50), ResolvedAt: ts(5, 55)},
	}
	segs := []backtest.Segment{{Start: ts(5, 0), End: ts(6, 0)}}

	sc := backtest.Score("R", firings, incidents, segs, backtest.ScoreOptions{Grace: 5 * time.Minute})

	if len(sc.FalsePositives) != 1 || !sc.FalsePositives[0].FiredAt.Equal(ts(5, 50)) {
		t.Errorf("false positives = %+v, want exactly the 05:50 firing", sc.FalsePositives)
	}
}

// Pages/week extrapolates firing count over COVERED time, never calendar
// time — 2 firings in 1h of coverage is 336/week, and the coverage ratio is
// reported alongside so nobody mistakes it for a calendar measurement.
func TestScorePagesPerWeekUsesCoverage(t *testing.T) {
	firings := []backtest.Firing{
		{Rule: "R", Series: "a", FiredAt: ts(5, 10), ResolvedAt: ts(5, 15)},
		{Rule: "R", Series: "a", FiredAt: ts(5, 40), ResolvedAt: ts(5, 45)},
	}
	segs := []backtest.Segment{{Start: ts(5, 0), End: ts(6, 0)}}

	sc := backtest.Score("R", firings, nil, segs, backtest.ScoreOptions{})

	if sc.Coverage != time.Hour {
		t.Errorf("coverage = %v, want 1h", sc.Coverage)
	}
	// 2 per hour × 168 h/week = 336
	if sc.PagesPerWeek < 335.9 || sc.PagesPerWeek > 336.1 {
		t.Errorf("pages/week = %v, want 336", sc.PagesPerWeek)
	}
}

// Flappiness is firing intervals per detected incident — 1.0 is a clean
// single page per incident; the spike measured 28 instant-twin intervals for
// one incident (flappiness 28).
func TestScoreFlappiness(t *testing.T) {
	incidents := []backtest.Incident{{ID: "inc-1", Start: ts(5, 0), End: ts(5, 59)}}
	var firings []backtest.Firing
	for m := 10; m < 22; m += 4 { // 3 short intervals inside the incident
		firings = append(firings, backtest.Firing{Rule: "R", Series: "a", FiredAt: ts(5, m), ResolvedAt: ts(5, m+2)})
	}
	segs := []backtest.Segment{{Start: ts(5, 0), End: ts(6, 0)}}

	sc := backtest.Score("R", firings, incidents, segs, backtest.ScoreOptions{})

	if sc.Flappiness != 3.0 {
		t.Errorf("flappiness = %v, want 3.0 (3 intervals, 1 incident)", sc.Flappiness)
	}
	// still exactly one detection, TTD from the FIRST firing
	if len(sc.Detections) != 1 || sc.Detections[0].TTD != 10*time.Minute {
		t.Errorf("detections = %+v", sc.Detections)
	}
}

// Expected-alert filtering: when an incident names expected_alerts, only
// those rules can claim detection credit for it; others' firings inside the
// window are still not false positives (the incident explains them).
func TestScoreExpectedAlerts(t *testing.T) {
	incidents := []backtest.Incident{
		{ID: "inc-1", Start: ts(5, 0), End: ts(5, 30), ExpectedAlerts: []string{"TheRightRule"}},
	}
	firings := []backtest.Firing{{Rule: "OtherRule", Series: "a", FiredAt: ts(5, 5), ResolvedAt: ts(5, 10)}}
	segs := []backtest.Segment{{Start: ts(4, 0), End: ts(6, 0)}}

	sc := backtest.Score("OtherRule", firings, incidents, segs, backtest.ScoreOptions{})

	if len(sc.Detections) != 0 {
		t.Errorf("detections = %+v, want none (not an expected alert)", sc.Detections)
	}
	if len(sc.FalsePositives) != 0 {
		t.Errorf("fps = %+v, want none (incident window explains the firing)", sc.FalsePositives)
	}
	if len(sc.Missed) != 1 {
		t.Errorf("missed = %v, want inc-1 (this rule did not detect it)", sc.Missed)
	}
}
