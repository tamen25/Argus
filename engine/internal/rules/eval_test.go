package rules

import (
	"testing"

	"github.com/tamen25/Argus/engine/internal/model"
)

func mustEngine(t *testing.T, yamls ...string) *Engine {
	t.Helper()
	var bs [][]byte
	for _, y := range yamls {
		bs = append(bs, []byte(y))
	}
	rs, err := LoadBytes(bs...)
	if err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}
	eng, err := NewEngine(rs)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	return eng
}

func TestEvalItemServiceNamePresent(t *testing.T) {
	eng := mustEngine(t, validRule)

	ok := model.Item{Kind: model.KindSpan, Service: "checkout",
		Resource: map[string]any{"service.name": "checkout"}, Span: &model.Span{Name: "x"}}
	if v := eng.EvalItem(ok); len(v) != 0 {
		t.Errorf("violations = %v, want none", v)
	}

	missing := model.Item{Kind: model.KindSpan, Service: model.UnknownService,
		Resource: map[string]any{}, Span: &model.Span{Name: "x"}}
	v := eng.EvalItem(missing)
	if len(v) != 1 || v[0].RuleID != "RES-005" {
		t.Fatalf("violations = %v, want RES-005", v)
	}

	empty := model.Item{Kind: model.KindSpan, Service: model.UnknownService,
		Resource: map[string]any{"service.name": ""}, Span: &model.Span{Name: "x"}}
	if v := eng.EvalItem(empty); len(v) != 1 {
		t.Errorf("empty service.name: violations = %v, want 1", v)
	}
}

// Item rules only apply to items matching their target.
func TestEvalItemTargetFiltering(t *testing.T) {
	spanRule := `
schema: argus.rules/v1
id: SPA-TEST
source: argus
name: span names are lowercase test rule
description: test
target: span
impact: low
evaluation:
  mode: item
  criteria: "span.name != 'BAD'"
`
	eng := mustEngine(t, spanRule)
	metricItem := model.Item{Kind: model.KindMetricPoint, Service: "s",
		Resource: map[string]any{}, Metric: &model.MetricPoint{Name: "BAD"}}
	if v := eng.EvalItem(metricItem); len(v) != 0 {
		t.Errorf("span rule fired on metric item: %v", v)
	}
	spanItem := model.Item{Kind: model.KindSpan, Service: "s",
		Resource: map[string]any{}, Span: &model.Span{Name: "BAD"}}
	if v := eng.EvalItem(spanItem); len(v) != 1 {
		t.Errorf("violations = %v, want 1", v)
	}
}

func TestEvalAggregateCardinality(t *testing.T) {
	agg := `
schema: argus.rules/v1
id: MET-001
source: spec
name: bounded metric attribute cardinality
description: test
target: metric
impact: important
evaluation:
  mode: aggregate
  aggregate: metric_attribute_cardinality
  criteria: "agg.cardinality < params.max_cardinality"
params:
  max_cardinality: 10000
`
	eng := mustEngine(t, agg)

	under := AggregateRow{Service: "ad", Aggregate: "metric_attribute_cardinality",
		Fields: map[string]any{"cardinality": 250, "metric": "http_requests_total", "attribute": "path"}}
	if v := eng.EvalAggregate(under); len(v) != 0 {
		t.Errorf("under-threshold violations = %v, want none", v)
	}

	over := AggregateRow{Service: "ad", Aggregate: "metric_attribute_cardinality",
		Fields: map[string]any{"cardinality": 15000, "metric": "http_requests_total", "attribute": "user_id"}}
	v := eng.EvalAggregate(over)
	if len(v) != 1 || v[0].RuleID != "MET-001" {
		t.Fatalf("violations = %v, want MET-001", v)
	}
}

// Rules whose target/mode do not match the aggregate name never fire.
func TestEvalAggregateNameFiltering(t *testing.T) {
	agg := `
schema: argus.rules/v1
id: MET-001
source: spec
name: bounded metric attribute cardinality
description: test
target: metric
impact: important
evaluation:
  mode: aggregate
  aggregate: metric_attribute_cardinality
  criteria: "agg.cardinality < 10"
`
	eng := mustEngine(t, agg)
	other := AggregateRow{Service: "s", Aggregate: "something_else", Fields: map[string]any{"cardinality": 99}}
	if v := eng.EvalAggregate(other); len(v) != 0 {
		t.Errorf("violations = %v, want none", v)
	}
}

// CEL runtime errors (e.g. missing field) must surface as errors, not silent
// passes — honest reporting.
func TestEvalAggregateMissingFieldIsError(t *testing.T) {
	agg := `
schema: argus.rules/v1
id: MET-001
source: spec
name: bounded metric attribute cardinality
description: test
target: metric
impact: important
evaluation:
  mode: aggregate
  aggregate: metric_attribute_cardinality
  criteria: "agg.cardinality < 10"
`
	eng := mustEngine(t, agg)
	row := AggregateRow{Service: "s", Aggregate: "metric_attribute_cardinality", Fields: map[string]any{}}
	v := eng.EvalAggregate(row)
	if len(v) != 1 || v[0].Err == nil {
		t.Errorf("want 1 violation carrying eval error, got %v", v)
	}
}
