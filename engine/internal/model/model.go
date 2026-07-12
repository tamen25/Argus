// Package model defines the normalized telemetry item that all Argus rules
// evaluate against. It is the single vocabulary shared by the OTLP stream
// path and (indirectly) the backend pollers: one Item per span, metric data
// point, or log record, always carrying its resource context.
//
// Bounded memory (architecture rule 3): conversion truncates attribute
// values; Items are processed and discarded, never persisted.
package model

// UnknownService is the service bucket for telemetry with no usable
// service.name resource attribute.
const UnknownService = "<unknown>"

// MaxAttrValueLen caps stored attribute value length (bytes). Longer values
// are truncated at conversion time so nothing downstream ever holds
// unbounded payloads.
const MaxAttrValueLen = 256

// Kind discriminates the item payload.
type Kind int8

const (
	KindSpan Kind = iota + 1
	KindMetricPoint
	KindLogRecord
)

func (k Kind) String() string {
	switch k {
	case KindSpan:
		return "span"
	case KindMetricPoint:
		return "metric"
	case KindLogRecord:
		return "log"
	default:
		return "unknown"
	}
}

// Item is one normalized telemetry item. Exactly one of Span, Metric, Log is
// non-nil, matching Kind.
type Item struct {
	Kind    Kind
	Service string // resource service.name, or UnknownService
	// Resource attributes minus service.name-derived Service duplication is
	// intentional NOT applied: the full attribute set (as observed, values
	// truncated) is kept so resource-target rules see exactly what was sent.
	Resource map[string]any
	Scope    Scope

	Span   *Span
	Metric *MetricPoint
	Log    *LogRecord
}

// Scope is the instrumentation scope that produced the item.
type Scope struct {
	Name    string
	Version string
}

// Span is the normalized span payload.
type Span struct {
	Name      string
	Kind      string // unspecified|internal|server|client|producer|consumer
	HasParent bool
	Status    string // unset|ok|error
	Attrs     map[string]any
}

// MetricPoint is one normalized metric data point.
type MetricPoint struct {
	Name         string
	Type         string // gauge|sum|histogram|exponential_histogram|summary
	Unit         string
	HasExemplars bool
	// ExemplarCount is the number of exemplars on this data point (rule 8
	// needs the count, not just presence).
	ExemplarCount int
	// BucketBounds/BucketCounts are populated for classic histograms only
	// (rule 12: bucket misconfiguration). len(counts) == len(bounds)+1.
	BucketBounds []float64
	BucketCounts []uint64
	Attrs        map[string]any
}

// LogRecord is the normalized log payload. Body content is not retained
// (bounded memory); only its presence and length matter to rules.
type LogRecord struct {
	SeverityText   string
	SeverityNumber int32
	HasTraceID     bool
	BodyLen        int
	Attrs          map[string]any
}
