// Package ingest receives the sampled OTLP mirror, normalizes it through the
// model package, feeds the rule collector, and maintains bounded sketches and
// trace state. Process-and-discard: no raw telemetry is retained
// (architecture rule 3).
package ingest

import (
	"strconv"
	"strings"
	"sync"
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

// consistencyAttrs are the resource attributes whose value spread per
// service feeds ARG-RES-004 (resource attribute consistency).
var consistencyAttrs = []string{"service.version", "deployment.environment.name", "telemetry.sdk.language"}

// Pipeline wires OTLP payloads into the collector and trackers.
type Pipeline struct {
	col        *rules.Collector
	card       *SketchTracker // metric_attribute_cardinality
	spanNames  *SketchTracker // span_name_cardinality
	bucketSigs *SketchTracker // histogram_bucket_signatures
	resAttrs   *SketchTracker // resource_attr_cardinality
	traces     *TraceTracker  // trace_health

	mu        sync.Mutex
	exemplars map[string]*exemplarCounts // per service, current window only
}

// exemplarCounts backs the exemplar_coverage aggregate: cheap counters, reset
// with AggregateRows consumption cycles via the same tumbling clock as the
// sketches is unnecessary — counts accumulate per run window (score CLI) or
// between exports (serve); bounded by service count.
type exemplarCounts struct {
	histogramPoints int64
	withExemplars   int64
}

// NewPipeline builds a pipeline over a collector with bounded trackers.
func NewPipeline(col *rules.Collector, opts TrackerOpts) *Pipeline {
	o := opts.withDefaults()
	return &Pipeline{
		col:        col,
		exemplars:  map[string]*exemplarCounts{},
		card:       NewSketchTracker("metric_attribute_cardinality", []string{"metric", "attribute"}, o.MaxPairs, o.Window, o.Now),
		spanNames:  NewSketchTracker("span_name_cardinality", []string{"attribute"}, o.MaxPairs, o.Window, o.Now),
		bucketSigs: NewSketchTracker("histogram_bucket_signatures", []string{"metric"}, o.MaxPairs, o.Window, o.Now),
		resAttrs:   NewSketchTracker("resource_attr_cardinality", []string{"attribute"}, o.MaxPairs, o.Window, o.Now),
		traces:     NewTraceTracker(o.MaxTraces, o.Window, o.Now),
	}
}

// ConsumeTraces processes one trace payload and discards it.
func (p *Pipeline) ConsumeTraces(td ptrace.Traces) {
	p.traces.ObserveTraces(td)
	for _, it := range model.FromTraces(td) {
		p.col.ObserveItem(it)
		p.spanNames.Observe(it.Service, []string{"span.name"}, it.Span.Name)
		p.observeResource(it)
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
		if it.Metric.Type == "histogram" {
			p.bucketSigs.Observe(it.Service, []string{it.Metric.Name}, bucketSignature(it.Metric.BucketBounds))
			p.mu.Lock()
			ec, ok := p.exemplars[it.Service]
			if !ok {
				ec = &exemplarCounts{}
				p.exemplars[it.Service] = ec
			}
			ec.histogramPoints++
			if it.Metric.ExemplarCount > 0 {
				ec.withExemplars++
			}
			p.mu.Unlock()
		}
		p.observeResource(it)
	}
}

// ConsumeLogs processes one logs payload and discards it.
func (p *Pipeline) ConsumeLogs(ld plog.Logs) {
	for _, it := range model.FromLogs(ld) {
		p.col.ObserveItem(it)
		p.observeResource(it)
	}
}

// AggregateRows snapshots all trackers into the collector and returns the
// rows for inspection.
func (p *Pipeline) AggregateRows() []rules.AggregateRow {
	var rows []rules.AggregateRow
	p.mu.Lock()
	for svc, ec := range p.exemplars {
		rows = append(rows, rules.AggregateRow{
			Service:   svc,
			Aggregate: "exemplar_coverage",
			Fields: map[string]any{
				"histogram_points": ec.histogramPoints,
				"with_exemplars":   ec.withExemplars,
			},
		})
	}
	p.mu.Unlock()
	rows = append(rows, p.card.Rows()...)
	rows = append(rows, p.spanNames.Rows()...)
	rows = append(rows, p.bucketSigs.Rows()...)
	rows = append(rows, p.resAttrs.Rows()...)
	rows = append(rows, p.traces.Rows()...)
	for _, r := range rows {
		p.col.ObserveAggregate(r)
	}
	return rows
}

// observeResource feeds the consistency sketches with this item's resource
// attribute values (ARG-RES-004: same service, conflicting resource attrs).
func (p *Pipeline) observeResource(it model.Item) {
	for _, key := range consistencyAttrs {
		if v, ok := it.Resource[key]; ok {
			p.resAttrs.Observe(it.Service, []string{key}, stringify(v))
		}
	}
}

// bucketSignature canonicalizes a histogram bucket layout for
// distinct-layout counting (MET-004).
func bucketSignature(bounds []float64) string {
	var b strings.Builder
	for _, f := range bounds {
		b.WriteString(strconv.FormatFloat(f, 'g', -1, 64))
		b.WriteByte(',')
	}
	return b.String()
}

// PairsTracked sums live sketch entries (self-metric).
func (p *Pipeline) PairsTracked() int {
	return p.card.PairsTracked() + p.spanNames.PairsTracked() + p.bucketSigs.PairsTracked() + p.resAttrs.PairsTracked()
}

// Evictions sums LRU evictions across all trackers (self-metric).
func (p *Pipeline) Evictions() int64 {
	return p.card.Evictions() + p.spanNames.Evictions() + p.bucketSigs.Evictions() + p.resAttrs.Evictions() + p.traces.Evictions()
}
