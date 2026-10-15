package export

import (
	"fmt"
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
	if n := testutil.CollectAndCount(e, "argus_instrumentation_score"); n != 1 {
		t.Errorf("score series = %d, want 1 after stale reset", n)
	}
}

// An empty fleet scores a vacuous 100. Exported, a dashboard would show it as
// perfect instrumentation, so no score series is reported, as before the
// first snapshot.
func TestEmptyFleetExportsNoScore(t *testing.T) {
	reg := prometheus.NewRegistry()
	e := NewPrometheus(reg)
	if n := testutil.CollectAndCount(e); n != 0 {
		t.Errorf("%d series before the first snapshot, want 0", n)
	}
	e.Update(&rules.Snapshot{FleetScore: 100, Services: []rules.ServiceReport{}})
	if n := testutil.CollectAndCount(e); n != 0 {
		t.Errorf("%d series for an empty fleet, want 0", n)
	}
}

// Updates and scrapes run concurrently without a data race (CI runs this with
// -race), and each scrape reports the services of one snapshot. That holds by
// construction, since a scrape loads the snapshot pointer once; a reset and
// refill, as before, left a window this test can hit only by chance.
func TestScrapeSeesAWholeSnapshot(t *testing.T) {
	reg := prometheus.NewRegistry()
	e := NewPrometheus(reg)
	fleet := func(n int) *rules.Snapshot {
		s := &rules.Snapshot{FleetScore: 50}
		for i := range n {
			s.Services = append(s.Services, rules.ServiceReport{ServiceName: fmt.Sprintf("svc-%d", i), SpecScore: 50})
		}
		return s
	}
	small, large := fleet(3), fleet(40)
	e.Update(small)

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := range 500 {
			if i%2 == 0 {
				e.Update(large)
			} else {
				e.Update(small)
			}
		}
	}()
	for {
		select {
		case <-done:
			return
		default:
		}
		if n := testutil.CollectAndCount(e, "argus_instrumentation_score"); n != 3 && n != 40 {
			t.Fatalf("a scrape saw %d services; a whole snapshot has 3 or 40", n)
		}
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
