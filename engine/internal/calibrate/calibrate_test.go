package calibrate

import (
	"bytes"
	"strings"
	"testing"

	"github.com/tamen25/Argus/engine/internal/rules"
)

func loadRule(t *testing.T, yaml string) *rules.Rule {
	t.Helper()
	rs, err := rules.LoadBytes([]byte(yaml))
	if err != nil {
		t.Fatal(err)
	}
	return rs[0]
}

const spa003 = `
schema: argus.rules/v1
id: SPA-003
source: spec
name: bounded span name count
description: test
target: span
impact: important
evaluation:
  mode: aggregate
  aggregate: span_name_cardinality
  criteria: "agg.cardinality < params.max_span_names"
params:
  max_span_names: 200
calibration:
  param: max_span_names
  source: aggregate
  aggregate: span_name_cardinality
  field: cardinality
  kind: count
`

const argRes004 = `
schema: argus.rules/v1
id: ARG-RES-004
source: argus
name: consistent resource attrs
description: test
target: resource
impact: important
evaluation:
  mode: aggregate
  aggregate: resource_attr_cardinality
  criteria: "agg.cardinality <= params.max_values"
params:
  max_values: 3
calibration:
  param: max_values
  source: aggregate
  aggregate: resource_attr_cardinality
  field: cardinality
  kind: small_count
`

const argLog001 = `
schema: argus.rules/v1
id: ARG-LOG-001
source: argus
name: logs carry trace correlation
description: test
target: log
impact: important
evaluation:
  mode: item
  criteria: "log.has_trace_id == true"
service_violation:
  threshold_ratio: 0.5
calibration:
  param: service_violation.threshold_ratio
  source: finding_ratio
  kind: ratio
`

func rowsFor(vals map[string][]float64) []rules.AggregateRow {
	var rows []rules.AggregateRow
	for agg, vs := range vals {
		for i, v := range vs {
			rows = append(rows, rules.AggregateRow{
				Service:   "svc",
				Aggregate: agg,
				Fields:    map[string]any{"cardinality": v, "i": i},
			})
		}
	}
	return rows
}

// count kind: propose ceil-to-2-significant-digits of P99 × 2.
func TestProposeCountKind(t *testing.T) {
	in := Input{
		Rules: []*rules.Rule{loadRule(t, spa003)},
		Aggregates: rowsFor(map[string][]float64{
			// P99 (nearest-rank, n=5) = 130 -> ×2 = 260 -> 2-sig ceil = 260
			"span_name_cardinality": {40, 45, 60, 80, 130},
		}),
	}
	props := Propose(in)
	if len(props) != 1 {
		t.Fatalf("proposals = %+v", props)
	}
	p := props[0]
	if p.RuleID != "SPA-003" || p.Param != "max_span_names" || p.Current != 200 {
		t.Errorf("proposal = %+v", p)
	}
	if p.Proposed != 260 {
		t.Errorf("proposed = %v, want 260 (P99 130 ×2)", p.Proposed)
	}
}

// small_count kind: ceil(P99) + 1 — single-digit spreads must not double.
func TestProposeSmallCountKind(t *testing.T) {
	in := Input{
		Rules: []*rules.Rule{loadRule(t, argRes004)},
		Aggregates: rowsFor(map[string][]float64{
			"resource_attr_cardinality": {1, 1, 2, 2, 5},
		}),
	}
	p := Propose(in)[0]
	if p.Proposed != 6 {
		t.Errorf("proposed = %v, want 6 (P99 5 +1)", p.Proposed)
	}
}

// ratio kind from finding ratios, with the failing-services-only disclosure.
func TestProposeRatioKind(t *testing.T) {
	in := Input{
		Rules:  []*rules.Rule{loadRule(t, argLog001)},
		Ratios: map[string][]float64{"ARG-LOG-001": {0.52, 0.55, 0.62, 0.70}},
	}
	p := Propose(in)[0]
	// P99 = 0.70; MAD around median 0.55 (lower-middle) is 0.03
	// -> spread max(0.05, 2×0.03) = 0.06 -> 0.70 + 0.06 = 0.76
	if p.Proposed != 0.76 {
		t.Errorf("proposed = %v, want 0.76", p.Proposed)
	}
	if p.Current != 0.5 {
		t.Errorf("current = %v, want 0.5 from service_violation.threshold_ratio", p.Current)
	}
	if !strings.Contains(p.Note, "failing services only") {
		t.Errorf("note = %q, want failing-services disclosure", p.Note)
	}
}

// The dotted target writes service_violation.threshold_ratio, not params.
func TestOverrideYAMLServiceViolationTarget(t *testing.T) {
	r := loadRule(t, argLog001)
	p := Proposal{RuleID: "ARG-LOG-001", Param: "service_violation.threshold_ratio", Proposed: 0.76}
	out, err := OverrideYAML(r, p)
	if err != nil {
		t.Fatal(err)
	}
	rs, err := rules.LoadBytes(out)
	if err != nil {
		t.Fatalf("emitted override does not load: %v\n%s", err, out)
	}
	if got := rs[0].ServiceViolation.ThresholdRatio; got != 0.76 {
		t.Errorf("threshold_ratio after round trip = %v, want 0.76", got)
	}
}

// Rules without data must not produce proposals (never invent evidence).
func TestProposeSkipsRulesWithoutData(t *testing.T) {
	in := Input{Rules: []*rules.Rule{loadRule(t, spa003)}}
	if props := Propose(in); len(props) != 0 {
		t.Errorf("proposals without data = %+v", props)
	}
}

func TestOverrideYAMLRoundTrips(t *testing.T) {
	r := loadRule(t, spa003)
	p := Proposal{RuleID: "SPA-003", Param: "max_span_names", Proposed: 260}
	out, err := OverrideYAML(r, p)
	if err != nil {
		t.Fatal(err)
	}
	rs, err := rules.LoadBytes(out)
	if err != nil {
		t.Fatalf("emitted override does not load: %v\n%s", err, out)
	}
	if v, ok := rs[0].Params["max_span_names"].(int); !ok || v != 260 {
		t.Errorf("params after round trip = %#v, want max_span_names 260 (int)", rs[0].Params)
	}
	// criteria must be byte-identical: calibration never touches criteria
	if rs[0].Evaluation.Criteria != r.Evaluation.Criteria {
		t.Error("criteria changed by calibration")
	}
}

// Same input, same bytes — reviewability depends on it.
func TestRenderDeterministic(t *testing.T) {
	in := Input{
		Rules: []*rules.Rule{loadRule(t, argRes004), loadRule(t, spa003)},
		Aggregates: rowsFor(map[string][]float64{
			"span_name_cardinality":     {40, 130},
			"resource_attr_cardinality": {1, 5},
		}),
	}
	a, b := Render(Propose(in)), Render(Propose(in))
	if !bytes.Equal([]byte(a), []byte(b)) {
		t.Error("render not deterministic")
	}
	// proposals sorted by rule ID
	if strings.Index(a, "ARG-RES-004") > strings.Index(a, "SPA-003") {
		t.Error("proposals not sorted by rule ID")
	}
}
