package ingest

import (
	"fmt"
	"testing"
	"time"

	"go.opentelemetry.io/collector/pdata/ptrace"

	"github.com/tamen25/Argus/engine/internal/rules"
)

func mkTrace(svc string, traceID byte, spans []spanSpec) ptrace.Traces {
	td := ptrace.NewTraces()
	rs := td.ResourceSpans().AppendEmpty()
	rs.Resource().Attributes().PutStr("service.name", svc)
	ss := rs.ScopeSpans().AppendEmpty()
	for _, s := range spans {
		sp := ss.Spans().AppendEmpty()
		sp.SetName(s.name)
		sp.SetTraceID([16]byte{traceID})
		sp.SetSpanID([8]byte{s.id})
		if s.parent != 0 {
			sp.SetParentSpanID([8]byte{s.parent})
		}
		sp.SetKind(s.kind)
	}
	return td
}

type spanSpec struct {
	name   string
	id     byte
	parent byte
	kind   ptrace.SpanKind
}

func rowsFor(rows []rules.AggregateRow, agg, svc string) map[string]any {
	for _, r := range rows {
		if r.Aggregate == agg && r.Service == svc {
			return r.Fields
		}
	}
	return nil
}

func TestTraceTrackerOrphanAndMissingRoot(t *testing.T) {
	now := time.Unix(0, 0)
	tt := NewTraceTracker(100, time.Hour, func() time.Time { return now })

	// trace 1: complete (root 1, child 2 -> parent 1 present)
	tt.ObserveTraces(mkTrace("checkout", 1, []spanSpec{
		{name: "root", id: 1, kind: ptrace.SpanKindServer},
		{name: "child", id: 2, parent: 1, kind: ptrace.SpanKindInternal},
	}))
	// trace 2: orphan (child 5 references parent 9 never seen) and no root
	tt.ObserveTraces(mkTrace("checkout", 2, []spanSpec{
		{name: "lost", id: 5, parent: 9, kind: ptrace.SpanKindInternal},
	}))

	// rows come from the completed (previous) generation only
	now = now.Add(61 * time.Minute)
	rows := tt.Rows()
	f := rowsFor(rows, "trace_health", "checkout")
	if f == nil {
		t.Fatalf("no trace_health row: %+v", rows)
	}
	if f["traces"].(int64) != 2 {
		t.Errorf("traces = %v, want 2", f["traces"])
	}
	if or := f["orphan_ratio"].(float64); or < 0.49 || or > 0.51 {
		t.Errorf("orphan_ratio = %v, want 0.5 (1 of 2 traces has orphans)", or)
	}
	if mr := f["missing_root_ratio"].(float64); mr < 0.49 || mr > 0.51 {
		t.Errorf("missing_root_ratio = %v, want 0.5", mr)
	}
}

// Traces are resolved ACROSS services: a checkout span whose parent lives in
// the frontend's batch is not an orphan, and a downstream service is not
// "rootless" just because the root span belongs to the entry service. The
// first live soak run scored orphan/missing-root 1.00 on every service
// because judgement was fragmented per (service, trace).
func TestTraceTrackerResolvesAcrossServices(t *testing.T) {
	now := time.Unix(0, 0)
	tt := NewTraceTracker(100, time.Hour, func() time.Time { return now })

	// one healthy distributed trace: frontend owns the root, checkout's span
	// points at frontend's child span
	tt.ObserveTraces(mkTrace("frontend", 1, []spanSpec{
		{name: "GET /", id: 1, kind: ptrace.SpanKindServer},
		{name: "call checkout", id: 2, parent: 1, kind: ptrace.SpanKindClient},
	}))
	tt.ObserveTraces(mkTrace("checkout", 1, []spanSpec{
		{name: "checkout", id: 3, parent: 2, kind: ptrace.SpanKindServer},
	}))

	// one genuinely broken trace, break visible at cart (parent never seen,
	// no root anywhere)
	tt.ObserveTraces(mkTrace("cart", 2, []spanSpec{
		{name: "lost", id: 5, parent: 9, kind: ptrace.SpanKindInternal},
	}))

	now = now.Add(61 * time.Minute)
	rows := tt.Rows()

	for _, svc := range []string{"frontend", "checkout"} {
		f := rowsFor(rows, "trace_health", svc)
		if f == nil {
			t.Fatalf("no trace_health row for %s: %+v", svc, rows)
		}
		if or := f["orphan_ratio"].(float64); or != 0 {
			t.Errorf("%s orphan_ratio = %v, want 0 (parent resolves cross-service)", svc, or)
		}
		if mr := f["missing_root_ratio"].(float64); mr != 0 {
			t.Errorf("%s missing_root_ratio = %v, want 0 (root lives with the entry service)", svc, mr)
		}
	}

	f := rowsFor(rows, "trace_health", "cart")
	if f == nil {
		t.Fatalf("no trace_health row for cart: %+v", rows)
	}
	if or := f["orphan_ratio"].(float64); or != 1 {
		t.Errorf("cart orphan_ratio = %v, want 1 (break point is attributed)", or)
	}
	if mr := f["missing_root_ratio"].(float64); mr != 1 {
		t.Errorf("cart missing_root_ratio = %v, want 1", mr)
	}
}

// The tracker already sees who calls whom (resolved cross-service parent
// references); service_dependency rows expose that as caller→callee edges
// for the plugin's service graph. Edges count traces, not spans; self-calls
// and unresolved parents produce no edges.
func TestTraceTrackerServiceDependencyEdges(t *testing.T) {
	now := time.Unix(0, 0)
	tt := NewTraceTracker(100, time.Hour, func() time.Time { return now })

	// trace 1: frontend → checkout → payment
	tt.ObserveTraces(mkTrace("frontend", 1, []spanSpec{
		{name: "GET /", id: 1, kind: ptrace.SpanKindServer},
		{name: "call checkout", id: 2, parent: 1, kind: ptrace.SpanKindClient},
	}))
	tt.ObserveTraces(mkTrace("checkout", 1, []spanSpec{
		{name: "checkout", id: 3, parent: 2, kind: ptrace.SpanKindServer},
		{name: "call payment", id: 4, parent: 3, kind: ptrace.SpanKindClient},
	}))
	tt.ObserveTraces(mkTrace("payment", 1, []spanSpec{
		{name: "charge", id: 5, parent: 4, kind: ptrace.SpanKindServer},
	}))

	// trace 2: frontend → checkout again — the edge counts traces, not spans
	tt.ObserveTraces(mkTrace("frontend", 2, []spanSpec{
		{name: "GET /", id: 1, kind: ptrace.SpanKindServer},
		{name: "call checkout", id: 2, parent: 1, kind: ptrace.SpanKindClient},
	}))
	tt.ObserveTraces(mkTrace("checkout", 2, []spanSpec{
		{name: "checkout", id: 3, parent: 2, kind: ptrace.SpanKindServer},
	}))

	// trace 3: broken at cart (parent never resolves) — no edge invented
	tt.ObserveTraces(mkTrace("cart", 3, []spanSpec{
		{name: "lost", id: 5, parent: 9, kind: ptrace.SpanKindInternal},
	}))

	now = now.Add(61 * time.Minute)
	rows := tt.Rows()

	edges := map[string]int64{} // "caller→callee" → traces
	for _, r := range rows {
		if r.Aggregate != "service_dependency" {
			continue
		}
		edges[r.Service+"→"+r.Fields["callee"].(string)] = r.Fields["traces"].(int64)
	}
	want := map[string]int64{
		"frontend→checkout": 2,
		"checkout→payment":  1,
	}
	if len(edges) != len(want) {
		t.Errorf("edges = %v, want exactly %v (no self-edges, nothing from cart)", edges, want)
	}
	for k, n := range want {
		if edges[k] != n {
			t.Errorf("edge %s = %d traces, want %d", k, edges[k], n)
		}
	}
}

func TestTraceTrackerCurrentGenerationNotReported(t *testing.T) {
	now := time.Unix(0, 0)
	tt := NewTraceTracker(100, time.Hour, func() time.Time { return now })
	tt.ObserveTraces(mkTrace("s", 3, []spanSpec{{name: "in-flight", id: 1, parent: 7}}))
	if rows := tt.Rows(); len(rows) != 0 {
		t.Errorf("in-flight generation must not be judged (spans may still arrive): %+v", rows)
	}
}

func TestTraceTrackerBounded(t *testing.T) {
	tt := NewTraceTracker(5, time.Hour, time.Now)
	for i := byte(1); i <= 20; i++ {
		tt.ObserveTraces(mkTrace("s", i, []spanSpec{{name: "x", id: 1}}))
	}
	if n := tt.TracesTracked(); n > 5 {
		t.Errorf("traces tracked = %d, want <= 5", n)
	}
	if tt.Evictions() == 0 {
		t.Error("evictions = 0, want > 0")
	}
}

func TestSketchTrackerNamedAggregates(t *testing.T) {
	st := NewSketchTracker("span_name_cardinality", []string{"attribute"}, 100, time.Hour, time.Now)
	for i := 0; i < 300; i++ {
		st.Observe("ad", []string{"span.name"}, fmt.Sprintf("GET /u/%d", i))
	}
	rows := st.Rows()
	f := rowsFor(rows, "span_name_cardinality", "ad")
	if f == nil {
		t.Fatalf("no row: %+v", rows)
	}
	if c := f["cardinality"].(int64); c < 290 || c > 310 {
		t.Errorf("cardinality = %d, want ~300", c)
	}
	if f["attribute"] != "span.name" {
		t.Errorf("fields = %v", f)
	}
}
