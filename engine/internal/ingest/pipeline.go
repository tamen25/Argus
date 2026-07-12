// Package ingest receives the sampled OTLP mirror, normalizes it through the
// model package, feeds the rule collector, and maintains bounded cardinality
// sketches. Process-and-discard: no raw telemetry is retained (architecture
// rule 3).
package ingest

import (
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/ptrace"

	"github.com/tamen25/Argus/engine/internal/model"
	"github.com/tamen25/Argus/engine/internal/rules"
)

// Pipeline wires OTLP payloads into the collector and sketches.
type Pipeline struct {
	col  *rules.Collector
	card *CardinalityTracker
}

// NewPipeline builds a pipeline over a collector and cardinality tracker.
func NewPipeline(col *rules.Collector, card *CardinalityTracker) *Pipeline {
	return &Pipeline{col: col, card: card}
}

// ConsumeTraces processes one trace payload and discards it.
func (p *Pipeline) ConsumeTraces(td ptrace.Traces) {
	for _, it := range model.FromTraces(td) {
		p.col.ObserveItem(it)
	}
}

// ConsumeMetrics processes one metrics payload and discards it. Metric
// attribute values additionally feed the cardinality sketches for MET-001.
func (p *Pipeline) ConsumeMetrics(md pmetric.Metrics) {
	for _, it := range model.FromMetrics(md) {
		p.col.ObserveItem(it)
		for k, v := range it.Metric.Attrs {
			p.card.Observe(it.Service, it.Metric.Name, k, stringify(v))
		}
	}
}

// ConsumeLogs processes one logs payload and discards it.
func (p *Pipeline) ConsumeLogs(ld plog.Logs) {
	for _, it := range model.FromLogs(ld) {
		p.col.ObserveItem(it)
	}
}

// CardinalityRows snapshots the sketches as aggregate rows and pushes them
// into the collector, returning them for inspection.
func (p *Pipeline) CardinalityRows() []rules.AggregateRow {
	rows := p.card.Rows()
	for _, r := range rows {
		p.col.ObserveAggregate(r)
	}
	return rows
}
