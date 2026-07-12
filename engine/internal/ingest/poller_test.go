package ingest

import (
	"context"
	"testing"

	"github.com/tamen25/Argus/engine/internal/rules"
)

type fakeMimir struct {
	jobs        []string
	cardinality map[string]int64 // "metric|label" -> series count
	err         error
}

func (f *fakeMimir) LabelValues(_ context.Context, label string) ([]string, error) {
	return f.jobs, f.err
}

func (f *fakeMimir) LabelCardinality(_ context.Context, metric, label string) (int64, error) {
	if f.err != nil {
		return 0, f.err
	}
	return f.cardinality[metric+"|"+label], nil
}

const met001 = `
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
confidence:
  poller: mimir_label_cardinality
`

const res005WithPoller = res005 + `
confidence:
  poller: mimir_service_presence
`

func TestPollerVerifiesServicePresence(t *testing.T) {
	rs, err := rules.LoadBytes([]byte(res005WithPoller))
	if err != nil {
		t.Fatal(err)
	}
	eng, _ := rules.NewEngine(rs)
	col := rules.NewCollector(eng)

	// stream saw checkout fail RES-005 (sampled artifact); Mimir knows the
	// service exists with a proper job label -> verified pass overrides
	p := NewPipeline(col, NewCardinalityTracker(10))
	p.ConsumeTraces(testTraces("", 1)) // <unknown> violation stays sampled

	poller := NewPoller(&fakeMimir{jobs: []string{"otel-demo/checkout", "ad"}}, eng)
	if err := poller.Run(context.Background(), col, []string{"checkout", "ad", "<unknown>"}); err != nil {
		t.Fatal(err)
	}

	snap := col.Snapshot()
	chk := snap.Service("checkout")
	if chk == nil {
		t.Fatal("checkout missing — poller must create verified results for polled services")
	}
	if len(chk.Results) != 1 || !chk.Results[0].Passed {
		t.Errorf("checkout results = %+v, want verified RES-005 pass", chk.Results)
	}
	// <unknown> keeps its sampled finding: absence in Mimir is not proof
	unk := snap.Service("<unknown>")
	if unk == nil || len(unk.Findings) != 1 || unk.Findings[0].Confidence != rules.ConfidenceSampled {
		t.Errorf("unknown = %+v, want sampled finding intact", unk)
	}
}

func TestPollerVerifiesCardinality(t *testing.T) {
	rs, err := rules.LoadBytes([]byte(met001))
	if err != nil {
		t.Fatal(err)
	}
	eng, _ := rules.NewEngine(rs)
	col := rules.NewCollector(eng)

	// sampled sketch flagged user_id on requests_total for service ad
	col.ObserveAggregate(rules.AggregateRow{Service: "ad", Aggregate: "metric_attribute_cardinality",
		Fields: map[string]any{"metric": "requests_total", "attribute": "user_id", "cardinality": int64(20000)}})

	// Mimir agrees: 55k series for that label -> verified fail
	poller := NewPoller(&fakeMimir{cardinality: map[string]int64{"requests_total|user_id": 55000}}, eng)
	if err := poller.Run(context.Background(), col, []string{"ad"}); err != nil {
		t.Fatal(err)
	}

	f := col.Snapshot().Service("ad").Findings[0]
	if f.Confidence != rules.ConfidenceVerified {
		t.Errorf("confidence = %s, want verified", f.Confidence)
	}
}

// A metric Mimir has never seen (0 series) cannot be verified — the sampled
// finding must stand, never be "verified away".
func TestPollerZeroSeriesDoesNotVerify(t *testing.T) {
	rs, _ := rules.LoadBytes([]byte(met001))
	eng, _ := rules.NewEngine(rs)
	col := rules.NewCollector(eng)
	col.ObserveAggregate(rules.AggregateRow{Service: "ad", Aggregate: "metric_attribute_cardinality",
		Fields: map[string]any{"metric": "stream_only_metric", "attribute": "user_id", "cardinality": int64(20000)}})

	poller := NewPoller(&fakeMimir{cardinality: map[string]int64{}}, eng)
	if err := poller.Run(context.Background(), col, []string{"ad"}); err != nil {
		t.Fatal(err)
	}
	f := col.Snapshot().Service("ad").Findings[0]
	if f.Confidence != rules.ConfidenceSampled {
		t.Errorf("confidence = %s, want sampled (unverifiable must not flip)", f.Confidence)
	}
}

func TestPollerErrorLeavesSampledResults(t *testing.T) {
	rs, _ := rules.LoadBytes([]byte(res005WithPoller))
	eng, _ := rules.NewEngine(rs)
	col := rules.NewCollector(eng)
	p := NewPipeline(col, NewCardinalityTracker(10))
	p.ConsumeTraces(testTraces("", 1))

	poller := NewPoller(&fakeMimir{err: context.DeadlineExceeded}, eng)
	if err := poller.Run(context.Background(), col, []string{"<unknown>"}); err == nil {
		t.Error("want error surfaced (honest reporting), got nil")
	}
	unk := col.Snapshot().Service("<unknown>")
	if unk.Findings[0].Confidence != rules.ConfidenceSampled {
		t.Errorf("confidence = %s, want sampled preserved on poller failure", unk.Findings[0].Confidence)
	}
}
