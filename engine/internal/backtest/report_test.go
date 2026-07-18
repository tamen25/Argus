package backtest_test

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tamen25/Argus/engine/internal/backtest"
)

var update = flag.Bool("update", false, "rewrite golden files")

func sampleReport() backtest.Report {
	return backtest.Report{
		GeneratedAt: time.Date(2026, 7, 18, 9, 0, 0, 0, time.UTC),
		From:        ts(5, 10),
		To:          ts(5, 45),
		Step:        time.Minute,
		Coverage:    32 * time.Minute,
		Segments:    1,
		Rules: []backtest.Scorecard{
			{
				Rule: "HighSpanErrorRatio",
				Detections: []backtest.Detection{
					{IncidentID: "2026-07-18-adfailure-baseline-2", TTD: 7 * time.Minute},
				},
				Missed:       []string{"2026-07-12-adfailure-toggle-test"},
				Unverifiable: []string{"2026-07-16-adfailure-spike-baseline"},
				Coverage:     32 * time.Minute,
				PagesPerWeek: 31.5,
				Flappiness:   1.0,
			},
			{
				Rule:     "SpanErrorRatioInstant",
				Coverage: 32 * time.Minute,
				FalsePositives: []backtest.Firing{
					{Rule: "SpanErrorRatioInstant", Series: `{service="flagd"}`, FiredAt: ts(5, 44)},
				},
				PagesPerWeek: 148.4,
				Flappiness:   0,
			},
		},
		Caveats: []string{
			"replay steps instant queries through time — staleness and ruler-alignment semantics differ from live evaluation",
			"telemetry covers 32m0s of the 35m0s window — verdicts apply to covered segments only",
		},
	}
}

// Golden files pin both renderings — the quality bar requires them for every
// report format, and determinism means byte-identical output.
func TestReportGoldens(t *testing.T) {
	r := sampleReport()
	for name, got := range map[string]string{
		"backtest-report.golden.md":   backtest.RenderReportMarkdown(r),
		"backtest-report.golden.json": mustJSON(t, r),
	} {
		path := filepath.Join("testdata", name)
		if *update {
			if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		want, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s: %v (run with -update to create)", name, err)
		}
		if got != string(want) {
			t.Errorf("%s drift:\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
		}
	}
}

func mustJSON(t *testing.T, r backtest.Report) string {
	t.Helper()
	b, err := backtest.RenderReportJSON(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// The caveat section is not optional: rendering a report with no caveats
// still prints the standing replay caveat header so nothing can strip it.
func TestReportMarkdownAlwaysHasCaveats(t *testing.T) {
	r := sampleReport()
	r.Caveats = nil
	out := backtest.RenderReportMarkdown(r)
	if !strings.Contains(out, "Fidelity caveats") {
		t.Errorf("markdown lost the caveat section:\n%s", out)
	}
}
