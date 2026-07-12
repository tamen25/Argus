package ingest

import (
	"context"
	"fmt"
	"net"
	"testing"

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
	return NewPipeline(col, NewCardinalityTracker(DefaultMaxTrackedPairs)), col
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

	rows := p.CardinalityRows()
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
		tr.Observe("svc", fmt.Sprintf("metric-%d", i), "attr", "v")
	}
	if n := len(tr.Rows()); n > 10 {
		t.Errorf("tracked pairs = %d, want <= 10 (bounded memory)", n)
	}
	if tr.Overflowed() == 0 {
		t.Error("overflow counter = 0, want > 0 (honest reporting of dropped pairs)")
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
