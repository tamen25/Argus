package backtest_test

import (
	"strings"
	"testing"
	"time"

	"github.com/tamen25/Argus/engine/internal/backtest"
)

func reportWith(rules ...backtest.Scorecard) backtest.Report {
	return backtest.Report{
		From: ts(5, 0), To: ts(6, 0), Step: time.Minute,
		Coverage: time.Hour, Segments: 1, Rules: rules,
	}
}

// DiffReports compares two rule sets over the SAME window: an incident is
// detected by a set if any rule detected it. Losing a detection or blowing
// the pages/week budget is a regression — the CI gate case.
func TestDiffLostDetectionIsRegression(t *testing.T) {
	a := reportWith(backtest.Scorecard{
		Rule:       "HighErr",
		Detections: []backtest.Detection{{IncidentID: "inc-1", TTD: 5 * time.Minute}},
	})
	b := reportWith(backtest.Scorecard{
		Rule:   "HighErr",
		Missed: []string{"inc-1"},
	})

	d, err := backtest.DiffReports(a, b, backtest.DiffOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(d.LostDetections) != 1 || d.LostDetections[0] != "inc-1" {
		t.Errorf("lost = %v", d.LostDetections)
	}
	if !d.Regression {
		t.Error("lost detection must be a regression")
	}
}

// Faster TTD and fewer pages is an improvement, not a regression.
func TestDiffImprovement(t *testing.T) {
	a := reportWith(backtest.Scorecard{
		Rule:         "R",
		Detections:   []backtest.Detection{{IncidentID: "inc-1", TTD: 10 * time.Minute}},
		PagesPerWeek: 300,
	})
	b := reportWith(backtest.Scorecard{
		Rule:         "R",
		Detections:   []backtest.Detection{{IncidentID: "inc-1", TTD: 4 * time.Minute}},
		PagesPerWeek: 40,
	})

	d, err := backtest.DiffReports(a, b, backtest.DiffOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if d.Regression {
		t.Errorf("improvement flagged as regression: %+v", d.Reasons)
	}
	if len(d.TTDChanges) != 1 || d.TTDChanges[0].B != 4*time.Minute {
		t.Errorf("ttd changes = %+v", d.TTDChanges)
	}
}

// TTD regression beyond tolerance and pages/week over budget both gate.
func TestDiffTTDAndBudgetGates(t *testing.T) {
	a := reportWith(backtest.Scorecard{
		Rule:       "R",
		Detections: []backtest.Detection{{IncidentID: "inc-1", TTD: 5 * time.Minute}},
	})
	b := reportWith(backtest.Scorecard{
		Rule:         "R",
		Detections:   []backtest.Detection{{IncidentID: "inc-1", TTD: 20 * time.Minute}},
		PagesPerWeek: 500,
	})

	d, err := backtest.DiffReports(a, b, backtest.DiffOptions{
		MaxTTDRegression: 10 * time.Minute,
		MaxPagesPerWeek:  100,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !d.Regression || len(d.Reasons) != 2 {
		t.Errorf("regression = %v reasons = %v, want both gates tripped", d.Regression, d.Reasons)
	}
}

// Reports over different coverage are not comparable — refuse, don't guess.
func TestDiffRefusesDifferentCoverage(t *testing.T) {
	a := reportWith()
	b := reportWith()
	b.Coverage = 30 * time.Minute
	if _, err := backtest.DiffReports(a, b, backtest.DiffOptions{}); err == nil {
		t.Error("different coverage compared without error")
	}
}

func TestDiffMarkdownRendering(t *testing.T) {
	a := reportWith(backtest.Scorecard{
		Rule:         "R",
		Detections:   []backtest.Detection{{IncidentID: "inc-1", TTD: 5 * time.Minute}},
		PagesPerWeek: 100,
	})
	b := reportWith(backtest.Scorecard{Rule: "R", Missed: []string{"inc-1"}, PagesPerWeek: 10})
	d, err := backtest.DiffReports(a, b, backtest.DiffOptions{})
	if err != nil {
		t.Fatal(err)
	}
	out := backtest.RenderDiffMarkdown(d)
	for _, want := range []string{"# Argus Backtest Diff", "inc-1", "REGRESSION", "Fidelity caveats"} {
		if !strings.Contains(out, want) {
			t.Errorf("markdown missing %q:\n%s", want, out)
		}
	}
}

// R22: equal covered time is not the same history. Different windows or
// different segments with the same total coverage used to be accepted; zero
// coverage used to pass the gate on no evidence.
func TestDiffRefusesDifferentHistory(t *testing.T) {
	shifted := reportWith()
	shifted.From, shifted.To = ts(7, 0), ts(8, 0) // also one covered hour
	otherGaps := reportWith()
	otherGaps.Spans = []backtest.Segment{{Start: ts(5, 30), End: ts(6, 0)}}
	steps := reportWith()
	steps.Step = 2 * time.Minute
	empty := reportWith()
	empty.Coverage = 0

	for name, b := range map[string]backtest.Report{
		"another window, same coverage": shifted,
		"other segments":                otherGaps,
		"another step":                  steps,
	} {
		if _, err := backtest.DiffReports(reportWith(), b, backtest.DiffOptions{}); err == nil {
			t.Errorf("%s: compared", name)
		}
	}
	if _, err := backtest.DiffReports(empty, empty, backtest.DiffOptions{}); err == nil ||
		!strings.Contains(err.Error(), "no telemetry coverage") {
		t.Errorf("zero coverage: err = %v, want it refused", err)
	}
}

// R22: each set's caveats travel into the diff, so a gate result is never
// read without knowing, say, that a rule in B could not be replayed.
func TestDiffCarriesEachSetsCaveats(t *testing.T) {
	a, b := reportWith(), reportWith()
	b.Caveats = []string{"HighErr not replayed: recording rule undefined"}
	d, err := backtest.DiffReports(a, b, backtest.DiffOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if md := backtest.RenderDiffMarkdown(d); !strings.Contains(md, "Set B: HighErr not replayed") {
		t.Errorf("the diff dropped B's caveat:\n%s", md)
	}
}
