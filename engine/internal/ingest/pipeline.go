// Package ingest receives the sampled OTLP mirror, normalizes it through the
// model package, feeds the rule collector, and maintains bounded sketches and
// trace state. Process-and-discard: no raw telemetry is retained
// (architecture rule 3).
package ingest

import (
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
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
	exemplars *generations[string, *exemplarCounts] // per service, windowed like the sketches

	// monotonic per-signal item counts (soak/serve self-metrics)
	traceItems, metricItems, logItems atomic.Int64
}

// exemplarCounts backs the exemplar_coverage aggregate (ARG-MET-001: a
// service passes if a histogram point in the window carried an exemplar).
// The counts are windowed like the sketches: they once accumulated for the
// engine's lifetime, so in `argus serve` one exemplar at startup passed the
// rule forever and a bridge that broke later was never caught.
type exemplarCounts struct {
	histogramPoints int64
	withExemplars   int64
}

// NewPipeline builds a pipeline over a collector with bounded trackers.
func NewPipeline(col *rules.Collector, opts TrackerOpts) *Pipeline {
	o := opts.withDefaults()
	return &Pipeline{
		col:        col,
		exemplars:  newGenerations[string, *exemplarCounts](o.MaxPairs, o.Window, o.Now),
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
		p.traceItems.Add(1)
		p.col.ObserveItem(it)
		p.spanNames.Observe(it.Service, []string{"span.name"}, it.Span.Name)
		p.observeResource(it)
	}
}

// ConsumeMetrics processes one metrics payload and discards it. Metric
// attribute values additionally feed the cardinality sketches for MET-001.
func (p *Pipeline) ConsumeMetrics(md pmetric.Metrics) {
	for _, it := range model.FromMetrics(md) {
		p.metricItems.Add(1)
		p.col.ObserveItem(it)
		for k, v := range it.Metric.Attrs {
			p.card.Observe(it.Service, []string{it.Metric.Name, k}, stringify(v))
		}
		if it.Metric.Type == "histogram" {
			p.bucketSigs.Observe(it.Service, []string{it.Metric.Name}, bucketSignature(it.Metric.BucketBounds))
			p.countExemplars(it.Service, it.Metric.ExemplarCount > 0)
		}
		p.observeResource(it)
	}
}

// countExemplars counts one histogram point for a service, and whether it
// carried exemplars.
func (p *Pipeline) countExemplars(service string, hasExemplars bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.exemplars.rotate()
	ec, ok := p.exemplars.get(service, func() *exemplarCounts { return &exemplarCounts{} })
	if !ok {
		return
	}
	ec.histogramPoints++
	if hasExemplars {
		ec.withExemplars++
	}
}

// ConsumeLogs processes one logs payload and discards it.
func (p *Pipeline) ConsumeLogs(ld plog.Logs) {
	for _, it := range model.FromLogs(ld) {
		p.logItems.Add(1)
		p.col.ObserveItem(it)
		p.observeResource(it)
	}
}

// ItemsConsumed reports monotonic per-signal item totals (traces, metrics,
// logs) since startup — the basis for items/sec self-metrics.
func (p *Pipeline) ItemsConsumed() (traces, metrics, logs int64) {
	return p.traceItems.Load(), p.metricItems.Load(), p.logItems.Load()
}

// CloseWindow ends the trace window now, so the next rows judge every trace
// seen so far. For a finite collection: see TraceTracker.CloseWindow.
func (p *Pipeline) CloseWindow() { p.traces.CloseWindow() }

// EdgesOmitted is how many service-graph edges the last rows left out.
func (p *Pipeline) EdgesOmitted() int { return p.traces.EdgesOmitted() }

// AggregateRows evaluates every tracker's current rows into the collector,
// replacing the previous evaluation, and returns the rows for inspection.
func (p *Pipeline) AggregateRows() []rules.AggregateRow {
	rows := p.CurrentRows()
	p.col.ObserveAggregates(rows)
	return rows
}

// CurrentRows returns the current aggregate rows WITHOUT evaluating them into
// the collector — the read-only view for HTTP handlers, where repeated
// scrapes must not inflate observation counts.
func (p *Pipeline) CurrentRows() []rules.AggregateRow {
	rows := p.exemplarRows()
	rows = append(rows, p.card.Rows()...)
	rows = append(rows, p.spanNames.Rows()...)
	rows = append(rows, p.bucketSigs.Rows()...)
	rows = append(rows, p.resAttrs.Rows()...)
	rows = append(rows, p.traces.Rows()...)
	return rows
}

// exemplarRows renders exemplar_coverage rows over the current and the
// previous generation (between one and two windows of points), so a bridge
// that stops sending exemplars shows within two windows.
func (p *Pipeline) exemplarRows() []rules.AggregateRow {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.exemplars.rotate()

	total := map[string]exemplarCounts{}
	for _, gen := range []map[string]*slot[*exemplarCounts]{p.exemplars.prev, p.exemplars.cur} {
		for svc, s := range gen {
			t := total[svc]
			t.histogramPoints += s.val.histogramPoints
			t.withExemplars += s.val.withExemplars
			total[svc] = t
		}
	}
	rows := make([]rules.AggregateRow, 0, len(total))
	for svc, t := range total {
		rows = append(rows, rules.AggregateRow{
			Service:   svc,
			Aggregate: "exemplar_coverage",
			Fields: map[string]any{
				"histogram_points": t.histogramPoints,
				"with_exemplars":   t.withExemplars,
			},
		})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Service < rows[j].Service })
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
