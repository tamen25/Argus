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
