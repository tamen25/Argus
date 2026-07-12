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
