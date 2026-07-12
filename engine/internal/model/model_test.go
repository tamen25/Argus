package model

import (
	"strings"
	"testing"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/ptrace"
)

func TestFromTraces(t *testing.T) {
	td := ptrace.NewTraces()
	rs := td.ResourceSpans().AppendEmpty()
	rs.Resource().Attributes().PutStr("service.name", "checkout")
	rs.Resource().Attributes().PutStr("service.version", "1.2.3")
	ss := rs.ScopeSpans().AppendEmpty()
	ss.Scope().SetName("otelsdk")
	sp := ss.Spans().AppendEmpty()
	sp.SetName("HTTP GET /cart")
	sp.SetKind(ptrace.SpanKindServer)
	sp.SetTraceID(pcommon.TraceID([16]byte{1}))
	sp.SetSpanID(pcommon.SpanID([8]byte{2}))
	sp.SetParentSpanID(pcommon.SpanID([8]byte{3}))
	sp.Attributes().PutStr("http.method", "GET")

	items := FromTraces(td)
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1", len(items))
	}
	it := items[0]
	if it.Kind != KindSpan {
		t.Errorf("kind = %v, want KindSpan", it.Kind)
	}
	if it.Service != "checkout" {
		t.Errorf("service = %q, want checkout", it.Service)
	}
	if it.Resource["service.version"] != "1.2.3" {
		t.Errorf("resource service.version = %v", it.Resource["service.version"])
	}
	if it.Span == nil || it.Span.Name != "HTTP GET /cart" || it.Span.Kind != "server" {
		t.Errorf("span = %+v", it.Span)
	}
	if !it.Span.HasParent {
		t.Error("HasParent = false, want true")
	}
	if it.Span.Attrs["http.method"] != "GET" {
		t.Errorf("span attr http.method = %v", it.Span.Attrs["http.method"])
	}
}

func TestFromTracesMissingServiceName(t *testing.T) {
	td := ptrace.NewTraces()
	rs := td.ResourceSpans().AppendEmpty()
	rs.ScopeSpans().AppendEmpty().Spans().AppendEmpty().SetName("x")

	items := FromTraces(td)
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1", len(items))
	}
	if items[0].Service != UnknownService {
		t.Errorf("service = %q, want %q", items[0].Service, UnknownService)
	}
	if _, ok := items[0].Resource["service.name"]; ok {
		t.Error("resource must not contain service.name")
	}
}

func TestFromMetrics(t *testing.T) {
	md := pmetric.NewMetrics()
	rm := md.ResourceMetrics().AppendEmpty()
	rm.Resource().Attributes().PutStr("service.name", "ad")
	sm := rm.ScopeMetrics().AppendEmpty()
	m := sm.Metrics().AppendEmpty()
	m.SetName("http_requests_total")
	m.SetUnit("1")
	dp := m.SetEmptySum().DataPoints().AppendEmpty()
	dp.Attributes().PutStr("user.id", "u-123456")
	dp.Exemplars().AppendEmpty()

	items := FromMetrics(md)
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1", len(items))
	}
	it := items[0]
	if it.Kind != KindMetricPoint || it.Metric == nil {
		t.Fatalf("bad item: %+v", it)
	}
	if it.Metric.Name != "http_requests_total" || it.Metric.Type != "sum" || it.Metric.Unit != "1" {
		t.Errorf("metric = %+v", it.Metric)
	}
	if !it.Metric.HasExemplars {
		t.Error("HasExemplars = false, want true")
	}
	if it.Metric.Attrs["user.id"] != "u-123456" {
		t.Errorf("metric attrs = %v", it.Metric.Attrs)
	}
}

func TestFromLogs(t *testing.T) {
	ld := plog.NewLogs()
	rl := ld.ResourceLogs().AppendEmpty()
	rl.Resource().Attributes().PutStr("service.name", "cart")
	lr := rl.ScopeLogs().AppendEmpty().LogRecords().AppendEmpty()
	lr.SetSeverityText("DEBUG")
	lr.SetSeverityNumber(plog.SeverityNumberDebug)
	lr.Body().SetStr("added item to cart")
	lr.SetTraceID(pcommon.TraceID([16]byte{9}))

	items := FromLogs(ld)
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1", len(items))
	}
	it := items[0]
	if it.Kind != KindLogRecord || it.Log == nil {
		t.Fatalf("bad item: %+v", it)
	}
	if it.Log.SeverityText != "DEBUG" || !it.Log.HasTraceID {
		t.Errorf("log = %+v", it.Log)
	}
}

func TestAttributeValueTruncation(t *testing.T) {
	td := ptrace.NewTraces()
	rs := td.ResourceSpans().AppendEmpty()
	rs.Resource().Attributes().PutStr("service.name", "x")
	long := strings.Repeat("a", 5000)
	sp := rs.ScopeSpans().AppendEmpty().Spans().AppendEmpty()
	sp.Attributes().PutStr("big", long)

	items := FromTraces(td)
	got, _ := items[0].Span.Attrs["big"].(string)
	if len(got) > MaxAttrValueLen {
		t.Errorf("attr value len = %d, want <= %d (bounded memory rule)", len(got), MaxAttrValueLen)
	}
}

func TestHistogramBucketFields(t *testing.T) {
	md := pmetric.NewMetrics()
	rm := md.ResourceMetrics().AppendEmpty()
	rm.Resource().Attributes().PutStr("service.name", "ad")
	m := rm.ScopeMetrics().AppendEmpty().Metrics().AppendEmpty()
	m.SetName("latency_seconds")
	m.SetUnit("s")
	dp := m.SetEmptyHistogram().DataPoints().AppendEmpty()
	dp.ExplicitBounds().FromRaw([]float64{0.1, 0.5, 1})
	dp.BucketCounts().FromRaw([]uint64{5, 3, 1, 0})
	dp.Exemplars().AppendEmpty()
	dp.Exemplars().AppendEmpty()

	items := FromMetrics(md)
	mp := items[0].Metric
	if len(mp.BucketBounds) != 3 || mp.BucketBounds[1] != 0.5 {
		t.Errorf("bounds = %v", mp.BucketBounds)
	}
	if len(mp.BucketCounts) != 4 || mp.BucketCounts[0] != 5 {
		t.Errorf("counts = %v", mp.BucketCounts)
	}
	if mp.ExemplarCount != 2 || !mp.HasExemplars {
		t.Errorf("exemplars = %d has=%v", mp.ExemplarCount, mp.HasExemplars)
	}
}

func TestSeverityTextNormalization(t *testing.T) {
	cases := map[string]int32{
		"TRACE": 1, "DEBUG": 5, "debug": 5, "INFO": 9, "Info": 9,
		"WARN": 13, "WARNING": 13, "ERROR": 17, "error": 17, "FATAL": 21,
		"weird": 0,
	}
	for text, want := range cases {
		ld := plog.NewLogs()
		rl := ld.ResourceLogs().AppendEmpty()
		rl.Resource().Attributes().PutStr("service.name", "s")
		lr := rl.ScopeLogs().AppendEmpty().LogRecords().AppendEmpty()
		lr.SetSeverityText(text) // SeverityNumber deliberately unset
		got := FromLogs(ld)[0].Log.SeverityNumber
		if got != want {
			t.Errorf("severity %q -> %d, want %d", text, got, want)
		}
	}
	// explicit number always wins over text
	ld := plog.NewLogs()
	rl := ld.ResourceLogs().AppendEmpty()
	lr := rl.ScopeLogs().AppendEmpty().LogRecords().AppendEmpty()
	lr.SetSeverityText("DEBUG")
	lr.SetSeverityNumber(plog.SeverityNumberError)
	if got := FromLogs(ld)[0].Log.SeverityNumber; got != 17 {
		t.Errorf("explicit number overridden: got %d", got)
	}
}

// The resource map must be built once per Resource* batch and shared across
// its items — deep-copying per item would make allocations scale with batch
// size and break the O(1) ingest guarantee on real payloads.
func TestResourceMapSharedAcrossBatchItems(t *testing.T) {
	td := ptrace.NewTraces()
	rs := td.ResourceSpans().AppendEmpty()
	rs.Resource().Attributes().PutStr("service.name", "checkout")
	ss := rs.ScopeSpans().AppendEmpty()
	ss.Spans().AppendEmpty().SetName("a")
	ss.Spans().AppendEmpty().SetName("b")

	items := FromTraces(td)
	if len(items) != 2 {
		t.Fatalf("items = %d", len(items))
	}
	items[0].Resource["__marker"] = true
	if _, ok := items[1].Resource["__marker"]; !ok {
		t.Error("resource maps are per-item copies; must be shared per batch")
	}
}
