package export

import (
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/tamen25/Argus/engine/internal/rules"
)

func TestScoreGauges(t *testing.T) {
	reg := prometheus.NewRegistry()
	e := NewPrometheus(reg)

	ext := 40.0
	e.Update(&rules.Snapshot{
		FleetScore: 80,
		Services: []rules.ServiceReport{
			{ServiceName: "checkout", SpecScore: 100, Category: "Excellent"},
			{ServiceName: "ad", SpecScore: 60, Category: "Needs Improvement", ExtensionScore: &ext,
				Findings: []rules.Finding{{RuleID: "MET-001", Impact: rules.ImpactImportant}}},
		},
	})

	want := `
# HELP argus_instrumentation_score Instrumentation Score (0-100) per service, spec rules only.
# TYPE argus_instrumentation_score gauge
argus_instrumentation_score{service="ad"} 60
argus_instrumentation_score{service="checkout"} 100
# HELP argus_instrumentation_score_fleet Mean Instrumentation Score across services.
# TYPE argus_instrumentation_score_fleet gauge
argus_instrumentation_score_fleet 80
# HELP argus_findings Open findings per service and impact.
# TYPE argus_findings gauge
argus_findings{impact="important",service="ad"} 1
`
	if err := testutil.GatherAndCompare(reg, strings.NewReader(want),
		"argus_instrumentation_score", "argus_instrumentation_score_fleet", "argus_findings"); err != nil {
		t.Error(err)
	}

	RegisterAggregateStats(reg, func() int { return 7 }, func() int64 { return 3 })
	wantStats := `
# HELP argus_aggregate_pair_evictions_total Sketch pairs evicted by the LRU admission policy (estimates lost).
# TYPE argus_aggregate_pair_evictions_total counter
argus_aggregate_pair_evictions_total 3
# HELP argus_aggregate_pairs_tracked Live (service, metric, attribute) sketch pairs across both window generations.
# TYPE argus_aggregate_pairs_tracked gauge
argus_aggregate_pairs_tracked 7
`
	if err := testutil.GatherAndCompare(reg, strings.NewReader(wantStats),
		"argus_aggregate_pairs_tracked", "argus_aggregate_pair_evictions_total"); err != nil {
		t.Error(err)
	}

	// stale services must disappear on next update
	e.Update(&rules.Snapshot{FleetScore: 100, Services: []rules.ServiceReport{
		{ServiceName: "checkout", SpecScore: 100, Category: "Excellent"},
	}})
	if n := testutil.CollectAndCount(e.score); n != 1 {
		t.Errorf("score series = %d, want 1 after stale reset", n)
	}
}

// Soak observability: per-signal item throughput (items/sec derives from the
// counter's rate).
func TestItemStats(t *testing.T) {
	reg := prometheus.NewRegistry()
	RegisterItemStats(reg, func() (int64, int64, int64) { return 11, 22, 33 })
	want := `
# HELP argus_items_consumed_total Telemetry items consumed since startup, by signal.
# TYPE argus_items_consumed_total counter
argus_items_consumed_total{signal="traces"} 11
argus_items_consumed_total{signal="metrics"} 22
argus_items_consumed_total{signal="logs"} 33
`
	if err := testutil.GatherAndCompare(reg, strings.NewReader(want), "argus_items_consumed_total"); err != nil {
		t.Error(err)
	}
}
