// Package ingest receives the sampled OTLP mirror, normalizes it through the
// model package, feeds the rule collector, and maintains bounded sketches and
// trace state. Process-and-discard: no raw telemetry is retained
// (architecture rule 3).
package ingest

import (
	"time"

	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/ptrace"

	"github.com/tamen25/Argus/engine/internal/model"
	"github.com/tamen25/Argus/engine/internal/rules"
)

// TrackerOpts bounds the pipeline's aggregate state.
type TrackerOpts struct {
	MaxPairs  int
	MaxTraces int
	Window    time.Duration
	Now       func() time.Time
}

func (o TrackerOpts) withDefaults() TrackerOpts {
	if o.MaxPairs <= 0 {
		o.MaxPairs = DefaultMaxTrackedPairs
	}
	if o.MaxTraces <= 0 {
		o.MaxTraces = DefaultMaxTrackedTraces
	}
	if o.Window <= 0 {
		o.Window = DefaultWindow
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	return o
}

// Pipeline wires OTLP payloads into the collector and trackers.
type Pipeline struct {
	col       *rules.Collector
	card      *SketchTracker // metric_attribute_cardinality
	spanNames *SketchTracker // span_name_cardinality
	traces    *TraceTracker  // trace_health
}

// NewPipeline builds a pipeline over a collector with bounded trackers.
func NewPipeline(col *rules.Collector, opts TrackerOpts) *Pipeline {
	o := opts.withDefaults()
	return &Pipeline{
		col:       col,
		card:      NewSketchTracker("metric_attribute_cardinality", []string{"metric", "attribute"}, o.MaxPairs, o.Window, o.Now),
		spanNames: NewSketchTracker("span_name_cardinality", []string{"attribute"}, o.MaxPairs, o.Window, o.Now),
		traces:    NewTraceTracker(o.MaxTraces, o.Window, o.Now),
	}
}

// ConsumeTraces processes one trace payload and discards it.
func (p *Pipeline) ConsumeTraces(td ptrace.Traces) {
	p.traces.ObserveTraces(td)
	for _, it := range model.FromTraces(td) {
		p.col.ObserveItem(it)
		p.spanNames.Observe(it.Service, []string{"span.name"}, it.Span.Name)
	}
}

// ConsumeMetrics processes one metrics payload and discards it. Metric
// attribute values additionally feed the cardinality sketches for MET-001.
func (p *Pipeline) ConsumeMetrics(md pmetric.Metrics) {
	for _, it := range model.FromMetrics(md) {
		p.col.ObserveItem(it)
		for k, v := range it.Metric.Attrs {
			p.card.Observe(it.Service, []string{it.Metric.Name, k}, stringify(v))
		}
	}
}

// ConsumeLogs processes one logs payload and discards it.
func (p *Pipeline) ConsumeLogs(ld plog.Logs) {
	for _, it := range model.FromLogs(ld) {
		p.col.ObserveItem(it)
	}
}

// AggregateRows snapshots all trackers into the collector and returns the
// rows for inspection.
func (p *Pipeline) AggregateRows() []rules.AggregateRow {
	var rows []rules.AggregateRow
	rows = append(rows, p.card.Rows()...)
	rows = append(rows, p.spanNames.Rows()...)
	rows = append(rows, p.traces.Rows()...)
	for _, r := range rows {
		p.col.ObserveAggregate(r)
	}
	return rows
}

// PairsTracked sums live sketch entries (self-metric).
func (p *Pipeline) PairsTracked() int {
	return p.card.PairsTracked() + p.spanNames.PairsTracked()
}

// Evictions sums LRU evictions across all trackers (self-metric).
func (p *Pipeline) Evictions() int64 {
	return p.card.Evictions() + p.spanNames.Evictions() + p.traces.Evictions()
}
