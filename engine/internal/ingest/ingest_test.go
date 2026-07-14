package ingest

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/pmetric/pmetricotlp"
	"go.opentelemetry.io/collector/pdata/ptrace"
	"go.opentelemetry.io/collector/pdata/ptrace/ptraceotlp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/tamen25/Argus/engine/internal/rules"
)

const res005 = `
schema: argus.rules/v1
id: RES-005
source: spec
name: service.name is present
description: test
target: resource
impact: critical
evaluation:
  mode: item
  criteria: "'service.name' in resource && string(resource['service.name']) != ''"
`

func newPipeline(t *testing.T) (*Pipeline, *rules.Collector) {
	t.Helper()
	rs, err := rules.LoadBytes([]byte(res005))
	if err != nil {
		t.Fatal(err)
	}
	eng, err := rules.NewEngine(rs)
	if err != nil {
		t.Fatal(err)
	}
	col := rules.NewCollector(eng)
	return NewPipeline(col, TrackerOpts{}), col
}

func testTraces(svc string, n int) ptrace.Traces {
	td := ptrace.NewTraces()
	rs := td.ResourceSpans().AppendEmpty()
	if svc != "" {
		rs.Resource().Attributes().PutStr("service.name", svc)
	}
	ss := rs.ScopeSpans().AppendEmpty()
	for i := 0; i < n; i++ {
		ss.Spans().AppendEmpty().SetName(fmt.Sprintf("op-%d", i))
	}
	return td
}

func testMetrics(svc, metric, attr string, values int) pmetric.Metrics {
	md := pmetric.NewMetrics()
	rm := md.ResourceMetrics().AppendEmpty()
	rm.Resource().Attributes().PutStr("service.name", svc)
	m := rm.ScopeMetrics().AppendEmpty().Metrics().AppendEmpty()
	m.SetName(metric)
	m.SetUnit("1")
	dps := m.SetEmptySum().DataPoints()
	for i := 0; i < values; i++ {
		dps.AppendEmpty().Attributes().PutStr(attr, fmt.Sprintf("val-%d", i))
	}
	return md
}

func TestPipelineFeedsCollector(t *testing.T) {
	p, col := newPipeline(t)
	p.ConsumeTraces(testTraces("checkout", 3))
	p.ConsumeTraces(testTraces("", 2))

	snap := col.Snapshot()
	if rep := snap.Service("checkout"); rep == nil || rep.SpecScore != 100 {
		t.Errorf("checkout report = %+v", rep)
	}
	unk := snap.Service("<unknown>")
	if unk == nil || len(unk.Findings) != 1 || unk.Findings[0].Stats.Violations != 2 {
		t.Errorf("unknown report = %+v", unk)
	}
}

func TestCardinalityTrackerEstimates(t *testing.T) {
	p, _ := newPipeline(t)
	const distinct = 5000
	p.ConsumeMetrics(testMetrics("ad", "requests_total", "user_id", distinct))
	// re-send same values: estimate must not double (it's distinct-count)
	p.ConsumeMetrics(testMetrics("ad", "requests_total", "user_id", distinct))

	rows := p.AggregateRows()
	var got int64 = -1
	for _, r := range rows {
		if r.Service == "ad" && r.Fields["metric"] == "requests_total" && r.Fields["attribute"] == "user_id" {
			got = r.Fields["cardinality"].(int64)
		}
	}
	if got < 0 {
		t.Fatalf("no cardinality row: %+v", rows)
	}
	if got < distinct*98/100 || got > distinct*102/100 {
		t.Errorf("estimate = %d, want %d ±2%%", got, distinct)
	}
}

func TestCardinalityTrackerBoundedPairs(t *testing.T) {
	tr := NewCardinalityTracker(10)
	for i := 0; i < 50; i++ {
		tr.Observe("svc", []string{fmt.Sprintf("metric-%d", i), "attr"}, "v")
	}
	if n := len(tr.Rows()); n > 10 {
		t.Errorf("tracked pairs = %d, want <= 10 (bounded memory)", n)
	}
	if tr.Evictions() == 0 {
		t.Error("eviction counter = 0, want > 0 (honest reporting of dropped pairs)")
	}
}

func TestGRPCReceiverEndToEnd(t *testing.T) {
	p, col := newPipeline(t)
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := NewGRPCServer(p)
	go func() { _ = srv.Serve(lis) }()
	defer srv.Stop()

	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()

	tc := ptraceotlp.NewGRPCClient(conn)
	if _, err := tc.Export(context.Background(), ptraceotlp.NewExportRequestFromTraces(testTraces("checkout", 2))); err != nil {
		t.Fatalf("traces export: %v", err)
	}
	mc := pmetricotlp.NewGRPCClient(conn)
	if _, err := mc.Export(context.Background(), pmetricotlp.NewExportRequestFromMetrics(testMetrics("payment", "m", "a", 3))); err != nil {
		t.Fatalf("metrics export: %v", err)
	}

	snap := col.Snapshot()
	if rep := snap.Service("checkout"); rep == nil {
		t.Error("checkout not observed via gRPC")
	}
	if rep := snap.Service("payment"); rep == nil {
		t.Error("payment not observed via gRPC")
	}
}

// Real-world collectors (Alloy, the OTel Collector) gzip OTLP exports by
// default; a receiver without the gzip decompressor drops every payload as a
// permanent error. Regression test for the live-cluster mirror.
func TestGRPCReceiverAcceptsGzip(t *testing.T) {
	p, col := newPipeline(t)
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := NewGRPCServer(p)
	go func() { _ = srv.Serve(lis) }()
	defer srv.Stop()

	// Compressor referenced by name, not by importing encoding/gzip here:
	// that import registers the codec process-globally and would green this
	// test even if the production receiver lost it. This way the registration
	// must come from the ingest package itself.
	conn, err := grpc.NewClient(lis.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.UseCompressor("gzip")))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()

	tc := ptraceotlp.NewGRPCClient(conn)
	if _, err := tc.Export(context.Background(), ptraceotlp.NewExportRequestFromTraces(testTraces("checkout", 2))); err != nil {
		t.Fatalf("gzip traces export: %v", err)
	}
	if rep := col.Snapshot().Service("checkout"); rep == nil {
		t.Error("gzip-compressed payload not observed")
	}
}

// CurrentRows is the read-only view for HTTP handlers: it must expose the
// same rows AggregateRows would without evaluating them into the collector,
// or every scrape would inflate observation counts.
func TestPipelineCurrentRowsIsReadOnly(t *testing.T) {
	rs, err := rules.LoadBytes([]byte(met001))
	if err != nil {
		t.Fatal(err)
	}
	eng, err := rules.NewEngine(rs)
	if err != nil {
		t.Fatal(err)
	}
	col := rules.NewCollector(eng)
	p := NewPipeline(col, TrackerOpts{})
	p.ConsumeMetrics(testMetrics("ad", "requests_total", "user_id", 12000))

	rows := p.CurrentRows()
	found := false
	for _, r := range rows {
		if r.Aggregate == "metric_attribute_cardinality" && r.Service == "ad" {
			found = true
		}
	}
	if !found {
		t.Fatalf("CurrentRows missing cardinality row: %+v", rows)
	}
	if rep := col.Snapshot().Service("ad"); rep != nil && len(rep.Findings) != 0 {
		t.Errorf("CurrentRows fed the collector: findings = %+v", rep.Findings)
	}

	p.AggregateRows()
	rep := col.Snapshot().Service("ad")
	if rep == nil || len(rep.Findings) != 1 || rep.Findings[0].RuleID != "MET-001" {
		t.Errorf("AggregateRows must still feed the collector, got %+v", rep)
	}
}

// Soak observability: items/sec is derived from monotonic per-signal item
// counters (argus_items_consumed_total).
func TestPipelineItemCounters(t *testing.T) {
	p, _ := newPipeline(t)
	p.ConsumeTraces(testTraces("checkout", 3))
	p.ConsumeMetrics(testMetrics("ad", "m", "a", 2)) // 2 datapoints = 2 items
	p.ConsumeMetrics(testMetrics("ad", "m", "a", 1))
	tr, me, lo := p.ItemsConsumed()
	if tr != 3 || me != 3 || lo != 0 {
		t.Errorf("ItemsConsumed() = %d,%d,%d, want 3,3,0", tr, me, lo)
	}
}

// Architecture rule 3: O(1) steady-state allocations per item. Processing
// must not allocate more per item as accumulated state grows.
func TestBoundedSteadyStateAllocations(t *testing.T) {
	p, _ := newPipeline(t)
	payload := testMetrics("ad", "requests_total", "user_id", 10)

	warm := func() { p.ConsumeMetrics(payload) }
	for i := 0; i < 100; i++ {
		warm()
	}
	before := testing.AllocsPerRun(200, warm)

	// grow accumulated state substantially
	for i := 0; i < 5000; i++ {
		p.ConsumeMetrics(payload)
	}
	after := testing.AllocsPerRun(200, warm)

	if after > before*1.5+16 {
		t.Errorf("allocs grew with state: before=%v after=%v", before, after)
	}
}

func TestCardinalityTwoGenerationTumbling(t *testing.T) {
	now := time.Unix(1000, 0)
	tr := NewCardinalityTrackerWithClock(100, time.Hour, func() time.Time { return now })

	for i := 0; i < 500; i++ {
		tr.Observe("s", []string{"m", "a"}, fmt.Sprintf("v%d", i))
	}
	est := func() int64 {
		for _, r := range tr.Rows() {
			if r.Fields["metric"] == "m" {
				return r.Fields["cardinality"].(int64)
			}
		}
		return -1
	}
	first := est()
	if first < 490 || first > 510 {
		t.Fatalf("estimate = %d, want ~500", first)
	}

	// cross one window boundary: previous generation still reported (max)
	now = now.Add(61 * time.Minute)
	if got := est(); got < 490 {
		t.Errorf("estimate right after rotation = %d; findings must not vanish at window boundary", got)
	}

	// cross a second boundary: the old generation ages out entirely
	now = now.Add(61 * time.Minute)
	if got := est(); got != -1 && got > 10 {
		t.Errorf("estimate after two rotations = %d, want gone/near-zero", got)
	}
}

func TestCardinalityLRUEvictionAndStats(t *testing.T) {
	tr := NewCardinalityTracker(3)
	tr.Observe("s", []string{"m1", "a"}, "v")
	tr.Observe("s", []string{"m2", "a"}, "v")
	tr.Observe("s", []string{"m3", "a"}, "v")
	tr.Observe("s", []string{"m1", "a"}, "v2") // refresh m1
	tr.Observe("s", []string{"m4", "a"}, "v")  // evicts m2

	rows := tr.Rows()
	seen := map[string]bool{}
	for _, r := range rows {
		seen[r.Fields["metric"].(string)] = true
	}
	if len(rows) != 3 || !seen["m1"] || !seen["m4"] || seen["m2"] {
		t.Errorf("tracked after eviction = %v, want m1,m3,m4", seen)
	}
	if tr.Evictions() != 1 {
		t.Errorf("evictions = %d, want 1", tr.Evictions())
	}
	if tr.PairsTracked() != 3 {
		t.Errorf("pairs tracked = %d, want 3", tr.PairsTracked())
	}
}

func testHistogram(svc string, bounds []float64) pmetric.Metrics {
	md := pmetric.NewMetrics()
	rm := md.ResourceMetrics().AppendEmpty()
	rm.Resource().Attributes().PutStr("service.name", svc)
	m := rm.ScopeMetrics().AppendEmpty().Metrics().AppendEmpty()
	m.SetName("latency")
	m.SetUnit("s")
	dp := m.SetEmptyHistogram().DataPoints().AppendEmpty()
	dp.ExplicitBounds().FromRaw(bounds)
	counts := make([]uint64, len(bounds)+1)
	dp.BucketCounts().FromRaw(counts)
	return md
}

func TestBucketSignatureTracking(t *testing.T) {
	p, _ := newPipeline(t)
	p.ConsumeMetrics(testHistogram("ad", []float64{0.1, 0.5, 1}))
	p.ConsumeMetrics(testHistogram("ad", []float64{0.1, 0.5, 1}))     // same buckets
	p.ConsumeMetrics(testHistogram("ad", []float64{0.25, 0.75, 1.5})) // drifted buckets

	rows := p.AggregateRows()
	f := rowsFor(rows, "histogram_bucket_signatures", "ad")
	if f == nil {
		t.Fatalf("no bucket-signature row: %+v", rows)
	}
	if c := f["cardinality"].(int64); c != 2 {
		t.Errorf("signature cardinality = %d, want 2 (two distinct bucket layouts)", c)
	}
	if f["metric"] != "latency" {
		t.Errorf("fields = %v", f)
	}
}

func TestResourceAttrConsistencyTracking(t *testing.T) {
	p, _ := newPipeline(t)
	send := func(version string) {
		md := pmetric.NewMetrics()
		rm := md.ResourceMetrics().AppendEmpty()
		rm.Resource().Attributes().PutStr("service.name", "checkout")
		rm.Resource().Attributes().PutStr("service.version", version)
		m := rm.ScopeMetrics().AppendEmpty().Metrics().AppendEmpty()
		m.SetName("m")
		m.SetEmptySum().DataPoints().AppendEmpty()
		p.ConsumeMetrics(md)
	}
	send("1.0.0")
	send("1.0.1")
	send("1.0.2")
	send("1.0.3")

	rows := p.AggregateRows()
	var card int64 = -1
	for _, r := range rows {
		if r.Aggregate == "resource_attr_cardinality" && r.Service == "checkout" && r.Fields["attribute"] == "service.version" {
			card = r.Fields["cardinality"].(int64)
		}
	}
	if card != 4 {
		t.Errorf("service.version cardinality = %d, want 4", card)
	}
}

func TestExemplarCoverageAggregate(t *testing.T) {
	p, _ := newPipeline(t)
	// 3 histogram points, none with exemplars
	for i := 0; i < 3; i++ {
		p.ConsumeMetrics(testHistogram("quote", []float64{0.1, 1}))
	}
	rows := p.AggregateRows()
	f := rowsFor(rows, "exemplar_coverage", "quote")
	if f == nil {
		t.Fatalf("no exemplar_coverage row: %+v", rows)
	}
	if f["histogram_points"].(int64) != 3 || f["with_exemplars"].(int64) != 0 {
		t.Errorf("fields = %v, want 3/0", f)
	}

	// a point with an exemplar flips coverage
	md := testHistogram("quote", []float64{0.1, 1})
	md.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics().At(0).Histogram().DataPoints().At(0).Exemplars().AppendEmpty()
	p.ConsumeMetrics(md)
	f = rowsFor(p.AggregateRows(), "exemplar_coverage", "quote")
	if f["with_exemplars"].(int64) != 1 {
		t.Errorf("with_exemplars = %v, want 1", f["with_exemplars"])
	}
}
