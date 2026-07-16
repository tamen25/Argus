package backtest_test

import (
	"testing"

	"github.com/tamen25/Argus/engine/internal/backtest"
)

// Dependencies classifies every series an alert expression reads — the core
// of fidelity failure mode (a): a rule referencing a recording rule that did
// not run historically cannot be replayed as-is, it needs synthesis.
func TestDependenciesClassification(t *testing.T) {
	rs, err := backtest.LoadRuleFiles("testdata/rules-valid.yaml", "testdata/rules-external-dep.yaml")
	if err != nil {
		t.Fatal(err)
	}

	deps := backtest.Dependencies(rs)

	// HighErrorRate reads service:http_errors:rate5m, which IS defined in the
	// loaded set → synthesizable by evaluating that expression inline.
	got := deps["HighErrorRate"]
	if len(got) != 1 || got[0].Series != "service:http_errors:rate5m" || got[0].Kind != backtest.DepDefinedRecording {
		t.Errorf("HighErrorRate deps = %+v, want one defined-recording dep", got)
	}

	// SLOBurnRateFast reads slo:sli_error:ratio_rate5m — colons mark it as a
	// recording rule, but no loaded file defines it → external, replay cannot
	// synthesize it and must say so.
	got = deps["SLOBurnRateFast"]
	if len(got) != 1 || got[0].Kind != backtest.DepExternalRecording {
		t.Errorf("SLOBurnRateFast deps = %+v, want one external-recording dep", got)
	}

	// PlainSeriesAlert reads up — a plain scraped series, replayable directly.
	got = deps["PlainSeriesAlert"]
	if len(got) != 1 || got[0].Series != "up" || got[0].Kind != backtest.DepPlainSeries {
		t.Errorf("PlainSeriesAlert deps = %+v, want one plain-series dep", got)
	}

	// PodRestartStorm reads kube_pod_container_status_restarts_total — plain.
	got = deps["PodRestartStorm"]
	if len(got) != 1 || got[0].Kind != backtest.DepPlainSeries {
		t.Errorf("PodRestartStorm deps = %+v, want one plain-series dep", got)
	}
}
