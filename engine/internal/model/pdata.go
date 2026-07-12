package model

import (
	"strings"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/ptrace"
)

// FromTraces converts an OTLP trace payload into normalized Items.
func FromTraces(td ptrace.Traces) []Item {
	var items []Item
	rss := td.ResourceSpans()
	for i := 0; i < rss.Len(); i++ {
		rs := rss.At(i)
		res, svc := convertResource(rs.Resource().Attributes())
		sss := rs.ScopeSpans()
		for j := 0; j < sss.Len(); j++ {
			ss := sss.At(j)
			scope := Scope{Name: ss.Scope().Name(), Version: ss.Scope().Version()}
			spans := ss.Spans()
			for k := 0; k < spans.Len(); k++ {
				sp := spans.At(k)
				items = append(items, Item{
					Kind:     KindSpan,
					Service:  svc,
					Resource: res,
					Scope:    scope,
					Span: &Span{
						Name:      sp.Name(),
						Kind:      spanKindString(sp.Kind()),
						HasParent: !sp.ParentSpanID().IsEmpty(),
						Status:    statusString(sp.Status().Code()),
						Attrs:     convertAttrs(sp.Attributes()),
					},
				})
			}
		}
	}
	return items
}

// FromMetrics converts an OTLP metrics payload into normalized Items, one per
// data point (cardinality lives at the data-point level).
func FromMetrics(md pmetric.Metrics) []Item {
	var items []Item
	rms := md.ResourceMetrics()
	for i := 0; i < rms.Len(); i++ {
		rm := rms.At(i)
		res, svc := convertResource(rm.Resource().Attributes())
		sms := rm.ScopeMetrics()
		for j := 0; j < sms.Len(); j++ {
			sm := sms.At(j)
			scope := Scope{Name: sm.Scope().Name(), Version: sm.Scope().Version()}
			ms := sm.Metrics()
			for k := 0; k < ms.Len(); k++ {
				m := ms.At(k)
				mtype, points := metricPoints(m)
				for _, p := range points {
					items = append(items, Item{
						Kind:     KindMetricPoint,
						Service:  svc,
						Resource: res,
						Scope:    scope,
						Metric: &MetricPoint{
							Name:          m.Name(),
							Type:          mtype,
							Unit:          m.Unit(),
							HasExemplars:  p.exemplarCount > 0,
							ExemplarCount: p.exemplarCount,
							BucketBounds:  p.bounds,
							BucketCounts:  p.counts,
							Attrs:         convertAttrs(p.attrs),
						},
					})
				}
			}
		}
	}
	return items
}

// FromLogs converts an OTLP logs payload into normalized Items.
func FromLogs(ld plog.Logs) []Item {
	var items []Item
	rls := ld.ResourceLogs()
	for i := 0; i < rls.Len(); i++ {
		rl := rls.At(i)
		res, svc := convertResource(rl.Resource().Attributes())
		sls := rl.ScopeLogs()
		for j := 0; j < sls.Len(); j++ {
			sl := sls.At(j)
			scope := Scope{Name: sl.Scope().Name(), Version: sl.Scope().Version()}
			lrs := sl.LogRecords()
			for k := 0; k < lrs.Len(); k++ {
				lr := lrs.At(k)
				items = append(items, Item{
					Kind:     KindLogRecord,
					Service:  svc,
					Resource: res,
					Scope:    scope,
					Log: &LogRecord{
						SeverityText:   lr.SeverityText(),
						SeverityNumber: normalizeSeverity(lr),
						HasTraceID:     !lr.TraceID().IsEmpty(),
						BodyLen:        len(lr.Body().AsString()),
						Attrs:          convertAttrs(lr.Attributes()),
					},
				})
			}
		}
	}
	return items
}

// convertResource extracts the service name and converts remaining resource
// attributes. The returned map reflects exactly what was observed — including
// service.name when present.
func convertResource(attrs pcommon.Map) (map[string]any, string) {
	res := convertAttrs(attrs)
	svc := UnknownService
	if v, ok := res["service.name"]; ok {
		if s, ok := v.(string); ok && s != "" {
			svc = s
		}
	}
	return res, svc
}

func convertAttrs(attrs pcommon.Map) map[string]any {
	out := make(map[string]any, attrs.Len())
	for k, v := range attrs.All() {
		out[k] = convertValue(v)
	}
	return out
}

func convertValue(v pcommon.Value) any {
	switch v.Type() {
	case pcommon.ValueTypeStr:
		s := v.Str()
		if len(s) > MaxAttrValueLen {
			s = s[:MaxAttrValueLen]
		}
		return s
	case pcommon.ValueTypeInt:
		return v.Int()
	case pcommon.ValueTypeDouble:
		return v.Double()
	case pcommon.ValueTypeBool:
		return v.Bool()
	default:
		// Maps/slices/bytes: rules only need scalar attributes; represent
		// complex values by their string form, truncated.
		s := v.AsString()
		if len(s) > MaxAttrValueLen {
			s = s[:MaxAttrValueLen]
		}
		return s
	}
}

type pointInfo struct {
	attrs         pcommon.Map
	exemplarCount int
	bounds        []float64
	counts        []uint64
}

func metricPoints(m pmetric.Metric) (string, []pointInfo) {
	switch m.Type() {
	case pmetric.MetricTypeGauge:
		dps := m.Gauge().DataPoints()
		out := make([]pointInfo, 0, dps.Len())
		for i := 0; i < dps.Len(); i++ {
			out = append(out, pointInfo{attrs: dps.At(i).Attributes(), exemplarCount: dps.At(i).Exemplars().Len()})
		}
		return "gauge", out
	case pmetric.MetricTypeSum:
		dps := m.Sum().DataPoints()
		out := make([]pointInfo, 0, dps.Len())
		for i := 0; i < dps.Len(); i++ {
			out = append(out, pointInfo{attrs: dps.At(i).Attributes(), exemplarCount: dps.At(i).Exemplars().Len()})
		}
		return "sum", out
	case pmetric.MetricTypeHistogram:
		dps := m.Histogram().DataPoints()
		out := make([]pointInfo, 0, dps.Len())
		for i := 0; i < dps.Len(); i++ {
			dp := dps.At(i)
			out = append(out, pointInfo{
				attrs:         dp.Attributes(),
				exemplarCount: dp.Exemplars().Len(),
				bounds:        dp.ExplicitBounds().AsRaw(),
				counts:        dp.BucketCounts().AsRaw(),
			})
		}
		return "histogram", out
	case pmetric.MetricTypeExponentialHistogram:
		dps := m.ExponentialHistogram().DataPoints()
		out := make([]pointInfo, 0, dps.Len())
		for i := 0; i < dps.Len(); i++ {
			out = append(out, pointInfo{attrs: dps.At(i).Attributes(), exemplarCount: dps.At(i).Exemplars().Len()})
		}
		return "exponential_histogram", out
	case pmetric.MetricTypeSummary:
		dps := m.Summary().DataPoints()
		out := make([]pointInfo, 0, dps.Len())
		for i := 0; i < dps.Len(); i++ {
			out = append(out, pointInfo{attrs: dps.At(i).Attributes()})
		}
		return "summary", out
	default:
		return "unknown", nil
	}
}

// normalizeSeverity returns the record's severity number; when only
// SeverityText is set (common with collector-converted logs), the text is
// mapped to the OTel severity scale so severity rules see one vocabulary.
func normalizeSeverity(lr plog.LogRecord) int32 {
	if n := lr.SeverityNumber(); n != plog.SeverityNumberUnspecified {
		return int32(n)
	}
	switch strings.ToUpper(lr.SeverityText()) {
	case "TRACE":
		return int32(plog.SeverityNumberTrace)
	case "DEBUG":
		return int32(plog.SeverityNumberDebug)
	case "INFO":
		return int32(plog.SeverityNumberInfo)
	case "WARN", "WARNING":
		return int32(plog.SeverityNumberWarn)
	case "ERROR":
		return int32(plog.SeverityNumberError)
	case "FATAL":
		return int32(plog.SeverityNumberFatal)
	default:
		return 0
	}
}

func spanKindString(k ptrace.SpanKind) string {
	switch k {
	case ptrace.SpanKindInternal:
		return "internal"
	case ptrace.SpanKindServer:
		return "server"
	case ptrace.SpanKindClient:
		return "client"
	case ptrace.SpanKindProducer:
		return "producer"
	case ptrace.SpanKindConsumer:
		return "consumer"
	default:
		return "unspecified"
	}
}

func statusString(c ptrace.StatusCode) string {
	switch c {
	case ptrace.StatusCodeOk:
		return "ok"
	case ptrace.StatusCodeError:
		return "error"
	default:
		return "unset"
	}
}
