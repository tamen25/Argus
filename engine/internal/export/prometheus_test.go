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

	// stale services must disappear on next update
	e.Update(&rules.Snapshot{FleetScore: 100, Services: []rules.ServiceReport{
		{ServiceName: "checkout", SpecScore: 100, Category: "Excellent"},
	}})
	if n := testutil.CollectAndCount(e.score); n != 1 {
		t.Errorf("score series = %d, want 1 after stale reset", n)
	}
}
