// Package model defines the normalized telemetry item that all Argus rules
// evaluate against. It is the single vocabulary shared by the OTLP stream
// path and (indirectly) the backend pollers: one Item per span, metric data
// point, or log record, always carrying its resource context.
//
// Bounded memory (architecture rule 3): an Item lives for one OTLP payload
// (itself capped by the receiver) and is then discarded. Only what is kept
// beyond it is bounded: finding evidence (Truncate) and service identity
// (ServiceName).
package model

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"unicode/utf8"
)

// UnknownService is the service bucket for telemetry with no usable
// service.name resource attribute.
const UnknownService = "<unknown>"

// MaxAttrValueLen caps an attribute value kept beyond the item that carried
// it, as finding evidence. Items hold full values: they used to be truncated
// at conversion, so the cardinality sketches hashed only the first 256 bytes,
// and distinct values sharing that prefix counted as one.
const MaxAttrValueLen = 256

// MaxServiceNameLen caps the length of a service identity (see ServiceName).
const MaxServiceNameLen = 256

// ServiceName returns the identity Argus keys a service by: the name itself,
// or, for one longer than MaxServiceNameLen, a prefix and a hash of the
// whole name. Long names therefore stay distinct and bounded, where a plain
// prefix would merge them. The item path and the trace tracker both use it,
// so a service has one identity in scores and in the service graph.
func ServiceName(name string) string {
	if len(name) <= MaxServiceNameLen {
		return name
	}
	sum := sha256.Sum256([]byte(name))
	return Truncate(name, MaxServiceNameLen-18) + "…" + hex.EncodeToString(sum[:7])
}

// Truncate returns s cut to at most n bytes on a UTF-8 character boundary,
// as a copy: a short slice of a long string would keep the whole string's
// memory alive.
func Truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return strings.Clone(s[:cut])
}

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
	// Resource holds every resource attribute as sent, service.name
	// included, so resource rules see exactly what the SDK reported.
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
	// ExemplarCount is the number of exemplars on this data point, offered to
	// rules as exemplar_count (ARG-MET-001 reads only has_exemplars).
	ExemplarCount int
	// BucketBounds and BucketCounts are set for classic histograms only
	// (ARG-MET-002 checks the bounds). len(counts) == len(bounds)+1.
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
