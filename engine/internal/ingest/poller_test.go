package ingest

import (
	"context"
	"fmt"
	"regexp"
	"testing"

	"github.com/tamen25/Argus/engine/internal/rules"
)

type fakeMimir struct {
	jobs        []string
	cardinality map[string]int64 // "service|metric|label" -> distinct values
	err         error
	calls       int
}

func (f *fakeMimir) LabelValues(_ context.Context, label string) ([]string, error) {
	return f.jobs, f.err
}

// selectorParts reads back what serviceSelector built.
var selectorParts = regexp.MustCompile(`^\{__name__="([^"]+)", job=~"\(\.\+/\)\?([^"]+)"\}$`)

func (f *fakeMimir) LabelCardinality(_ context.Context, selector, label string) (int64, error) {
	f.calls++
	if f.err != nil {
		return 0, f.err
	}
	m := selectorParts.FindStringSubmatch(selector)
	if m == nil {
		return 0, fmt.Errorf("selector %s is not scoped to a service", selector)
	}
	return f.cardinality[m[2]+"|"+m[1]+"|"+label], nil
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
	col := rules.NewCollector(eng, rules.CollectorOptions{})

	// stream saw checkout fail RES-005 (sampled artifact); Mimir knows the
	// service exists with a proper job label -> verified pass overrides
	p := NewPipeline(col, TrackerOpts{MaxPairs: 10})
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
	col := rules.NewCollector(eng, rules.CollectorOptions{})

	// sampled sketch flagged user_id on requests_total for service ad
	col.ObserveAggregates([]rules.AggregateRow{{Service: "ad", Aggregate: "metric_attribute_cardinality",
		Fields: map[string]any{"metric": "requests_total", "attribute": "user_id", "cardinality": int64(20000)}}})

	// Mimir agrees: 55k series for that label -> verified fail
	poller := NewPoller(&fakeMimir{cardinality: map[string]int64{"ad|requests_total|user_id": 55000}}, eng)
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
	col := rules.NewCollector(eng, rules.CollectorOptions{})
	col.ObserveAggregates([]rules.AggregateRow{{Service: "ad", Aggregate: "metric_attribute_cardinality",
		Fields: map[string]any{"metric": "stream_only_metric", "attribute": "user_id", "cardinality": int64(20000)}}})

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
	col := rules.NewCollector(eng, rules.CollectorOptions{})
	p := NewPipeline(col, TrackerOpts{MaxPairs: 10})
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

// cardinalityRows is one export of sampled cardinality estimates, each over
// the limit, so every pair is flagged and goes to the poller.
func cardinalityRows(rows ...[3]string) []rules.AggregateRow {
	var out []rules.AggregateRow
	for _, r := range rows {
		out = append(out, rules.AggregateRow{Service: r[0], Aggregate: "metric_attribute_cardinality",
			Fields: map[string]any{"metric": r[1], "attribute": r[2], "cardinality": int64(20000)}})
	}
	return out
}

// R18: one result per service, combining its pairs. A pair Mimir measures
// over the limit fails the rule whatever order the pairs are checked in; the
// last pair checked used to overwrite the rest.
func TestPollerCombinesPairsPerService(t *testing.T) {
	for _, order := range [][2]string{{"user_id", "region"}, {"region", "user_id"}} {
		rs, _ := rules.LoadBytes([]byte(met001))
		eng, _ := rules.NewEngine(rs)
		col := rules.NewCollector(eng, rules.CollectorOptions{})
		col.ObserveAggregates(cardinalityRows(
			[3]string{"ad", "requests_total", order[0]},
			[3]string{"ad", "requests_total", order[1]},
		))
		mimir := &fakeMimir{cardinality: map[string]int64{
			"ad|requests_total|user_id": 55000, // over the limit
			"ad|requests_total|region":  4,     // well under it
		}}
		if err := NewPoller(mimir, eng).Run(context.Background(), col, []string{"ad"}); err != nil {
			t.Fatal(err)
		}
		f := col.Snapshot().Service("ad").Findings
		if len(f) != 1 || f[0].Confidence != rules.ConfidenceVerified {
			t.Errorf("order %v: findings = %+v, want one verified failure", order, f)
		}
	}
}

// R18: each service is measured on its own series, and a pair named twice is
// queried once.
func TestPollerScopesToServiceAndDedupes(t *testing.T) {
	rs, _ := rules.LoadBytes([]byte(met001))
	eng, _ := rules.NewEngine(rs)
	col := rules.NewCollector(eng, rules.CollectorOptions{})
	col.ObserveAggregates(cardinalityRows(
		[3]string{"ad", "requests_total", "user_id"},
		[3]string{"ad", "requests_total", "user_id"},
		[3]string{"cart", "requests_total", "user_id"},
	))
	mimir := &fakeMimir{cardinality: map[string]int64{
		"ad|requests_total|user_id":   55000,
		"cart|requests_total|user_id": 12,
	}}
	if err := NewPoller(mimir, eng).Run(context.Background(), col, []string{"ad", "cart"}); err != nil {
		t.Fatal(err)
	}
	if mimir.calls != 2 {
		t.Errorf("queries = %d, want 2 (ad's duplicate pair asked once)", mimir.calls)
	}
	snap := col.Snapshot()
	if f := snap.Service("ad").Findings; len(f) != 1 {
		t.Errorf("ad findings = %+v, want its verified failure", f)
	}
	if f := snap.Service("cart").Findings; len(f) != 0 {
		t.Errorf("cart findings = %+v, want none: its own series are well under the limit", f)
	}
}
