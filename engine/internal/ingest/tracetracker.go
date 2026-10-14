package ingest

import (
	"sort"
	"sync"
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/ptrace"

	"github.com/tamen25/Argus/engine/internal/model"
	"github.com/tamen25/Argus/engine/internal/rules"
)

// DefaultMaxTrackedTraces bounds trace states per generation. Memory: a
// trace state holds span/parent ID sets (8 bytes each, span count capped)
// plus small per-parent service sets, so worst case ≈ cap ×
// maxSpansPerTrace × ~32B ≈ 128MiB at 8192 × 512; real traces sit far
// below both caps.
const DefaultMaxTrackedTraces = 8192

// maxSpansPerTrace caps per-trace span-ID retention (bounded memory).
const maxSpansPerTrace = 512

// maxServicesPerTrace caps the participating-service set per trace.
const maxServicesPerTrace = 64

// svcOverflow marks spans from services beyond maxServicesPerTrace. Their
// span IDs still resolve other spans' parent references, but they are not
// counted as participants, so they record no parent references of their own:
// a service that breaks a trace it was never counted in would have a ratio
// with a zero denominator.
const svcOverflow = 0xFF

// maxGraphEdges caps the (caller, callee) pairs one Rows call reports. When
// there are more, the pairs seen in the most traces are kept (then by name),
// so the graph shows the busiest paths and is the same on every call; the
// number left out is reported (EdgesOmitted).
const maxGraphEdges = 4096

// traceState is GLOBAL per trace ID — spans from every service resolve into
// the same state. The first live soak run proved why: keyed per (service,
// trace), every downstream service scored orphan/missing-root 1.00 because
// its parents and the root lived in other services' fragments.
type traceState struct {
	// spans maps each seen span ID to the index of its service in svcList
	// (svcOverflow past the service cap) — parent resolution plus the
	// caller side of service_dependency edges.
	spans map[pcommon.SpanID]uint8
	// parents maps a referenced parent span ID to the services whose spans
	// reference it — unresolved parents are attributed to those services
	// (the break is visible where the dangling reference was emitted).
	parents map[pcommon.SpanID]map[string]struct{}
	svcIdx  map[string]uint8
	svcList []string
	hasRoot bool
}

// TraceTracker accumulates per-trace span topology on the sampled stream to
// evaluate trace completeness (SPA-002 orphans, ARG-SPA-002 missing roots).
//
// Judgement happens on the *previous* generation only: an in-flight trace
// must not be scored while its spans may still arrive. Same two-generation
// tumbling store as SketchTracker (generations).
//
// SAMPLING CAVEAT (reported via rule docs + confidence=sampled): with
// non-tail-based sampling, parts of a trace can be legitimately absent from
// the mirror. Ratios here are upper bounds; thresholds default accordingly.
type TraceTracker struct {
	mu   sync.Mutex
	gens *generations[pcommon.TraceID, *traceState]
	// edgesOmitted is how many edges the last Rows call left out.
	edgesOmitted int
}

// NewTraceTracker builds a tracker capped at maxTraces per generation.
func NewTraceTracker(maxTraces int, window time.Duration, now func() time.Time) *TraceTracker {
	return &TraceTracker{gens: newGenerations[pcommon.TraceID, *traceState](maxTraces, window, now)}
}

// ObserveTraces records span topology from one OTLP payload.
func (t *TraceTracker) ObserveTraces(td ptrace.Traces) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.gens.rotate()

	rss := td.ResourceSpans()
	for i := 0; i < rss.Len(); i++ {
		rs := rss.At(i)
		svc := model.UnknownService
		if v, ok := rs.Resource().Attributes().Get("service.name"); ok && v.Str() != "" {
			svc = model.ServiceName(v.Str()) // the same identity the collector uses
		}
		sss := rs.ScopeSpans()
		for j := 0; j < sss.Len(); j++ {
			spans := sss.At(j).Spans()
			for k := 0; k < spans.Len(); k++ {
				sp := spans.At(k)
				t.observeSpanLocked(svc, sp)
			}
		}
	}
}

func (t *TraceTracker) observeSpanLocked(svc string, sp ptrace.Span) {
	st, ok := t.gens.get(sp.TraceID(), newTraceState)
	if !ok {
		return
	}
	idx := st.serviceIndex(svc)
	if len(st.spans) < maxSpansPerTrace {
		st.spans[sp.SpanID()] = idx
	}

	parent := sp.ParentSpanID()
	switch refs, seen := st.parents[parent]; {
	case parent.IsEmpty():
		st.hasRoot = true
	case idx == svcOverflow:
		// Past the service cap: not a participant, so not blamed either.
	case seen:
		if len(refs) < maxServicesPerTrace {
			refs[svc] = struct{}{}
		}
	case len(st.parents) < maxSpansPerTrace:
		st.parents[parent] = map[string]struct{}{svc: {}}
	}
}

func newTraceState() *traceState {
	return &traceState{
		spans:   make(map[pcommon.SpanID]uint8, 8),
		parents: make(map[pcommon.SpanID]map[string]struct{}, 8),
		svcIdx:  make(map[string]uint8, 4),
	}
}

// serviceIndex returns svc's index in the trace's service list, adding it if
// there is room, and svcOverflow once the trace has maxServicesPerTrace.
func (st *traceState) serviceIndex(svc string) uint8 {
	if idx, ok := st.svcIdx[svc]; ok {
		return idx
	}
	if len(st.svcList) >= maxServicesPerTrace {
		return svcOverflow
	}
	idx := uint8(len(st.svcList)) //nolint:gosec // len < maxServicesPerTrace, which fits a uint8
	st.svcIdx[svc] = idx
	st.svcList = append(st.svcList, svc)
	return idx
}

// Evictions returns LRU-evicted trace count.
func (t *TraceTracker) Evictions() int64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.gens.evictions
}

// TracesTracked returns the live trace count in the current generation.
func (t *TraceTracker) TracesTracked() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.gens.rotate()
	return len(t.gens.cur)
}

// Rows emits one trace_health aggregate per service from the completed
// (previous) generation: traces the service participated in, and the
// fraction of those with orphaned spans / no root — attributed to the
// services whose spans reference the unresolved parents (the visible break
// point), never collectively. It also emits one service_dependency row per
// caller→callee pair, counting traces.
func (t *TraceTracker) Rows() []rules.AggregateRow {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.gens.rotate()

	health := map[string]*traceHealth{}
	edgeTraces := map[edge]int64{}
	for _, s := range t.gens.prev {
		st := s.val
		breaks, edges := st.analyze()
		tallyHealth(health, st, breaks)
		for e := range edges {
			edgeTraces[e]++
		}
	}
	deps, omitted := dependencyRows(edgeTraces)
	t.edgesOmitted = omitted
	return append(healthRows(health), deps...)
}

// EdgesOmitted is how many caller→callee edges the last Rows call left out
// because there were more than maxGraphEdges.
func (t *TraceTracker) EdgesOmitted() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.edgesOmitted
}

// CloseWindow ends the current window now, so Rows judges the traces seen so
// far. For a finite collection (argus score), which would otherwise never
// complete a window: once collection has stopped no more spans can arrive,
// though a trace still in progress at that moment can look broken.
func (t *TraceTracker) CloseWindow() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.gens.closeWindow()
}

// edge is a caller→callee service pair.
type edge struct{ caller, callee string }

// traceHealth counts, for one service, the traces it took part in and how
// many of those it broke.
type traceHealth struct{ traces, orphaned, rootless int64 }

// analyze resolves one trace's parent references. breaks are the services
// holding a reference to a parent span that never arrived; edges are the
// cross-service caller→callee pairs that did resolve, once each per trace,
// so an edge counts traces rather than spans.
func (st *traceState) analyze() (breaks map[string]struct{}, edges map[edge]struct{}) {
	breaks = map[string]struct{}{}
	edges = map[edge]struct{}{}
	for parent, refs := range st.parents {
		idx, resolved := st.spans[parent]
		if !resolved {
			for svc := range refs {
				breaks[svc] = struct{}{}
			}
			continue
		}
		if idx == svcOverflow {
			continue // the caller is past the service cap: no attribution
		}
		caller := st.svcList[idx]
		for callee := range refs {
			if callee != caller {
				edges[edge{caller, callee}] = struct{}{}
			}
		}
	}
	return breaks, edges
}

// tallyHealth adds one trace to the per-service counts. A trace without a
// root is blamed on its break points; if it has none (the span cap truncated
// it), every participant carries it, an honest upper bound.
func tallyHealth(health map[string]*traceHealth, st *traceState, breaks map[string]struct{}) {
	of := func(svc string) *traceHealth {
		h, ok := health[svc]
		if !ok {
			h = &traceHealth{}
			health[svc] = h
		}
		return h
	}
	for svc := range st.svcIdx {
		of(svc).traces++
	}
	for svc := range breaks {
		of(svc).orphaned++
	}
	if st.hasRoot {
		return
	}
	if len(breaks) > 0 {
		for svc := range breaks {
			of(svc).rootless++
		}
		return
	}
	for svc := range st.svcIdx {
		of(svc).rootless++
	}
}

// healthRows renders the per-service counts as trace_health rows, sorted by
// service. A service with no counted trace has no ratio, and gets no row.
func healthRows(health map[string]*traceHealth) []rules.AggregateRow {
	rows := make([]rules.AggregateRow, 0, len(health))
	for svc, h := range health {
		if h.traces == 0 {
			continue
		}
		rows = append(rows, rules.AggregateRow{
			Service:   svc,
			Aggregate: "trace_health",
			Fields: map[string]any{
				"traces":             h.traces,
				"orphan_ratio":       float64(h.orphaned) / float64(h.traces),
				"missing_root_ratio": float64(h.rootless) / float64(h.traces),
			},
		})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Service < rows[j].Service })
	return rows
}

// dependencyRows renders the edge counts as service_dependency rows, sorted by
// caller then callee. Past maxGraphEdges it keeps the edges seen in the most
// traces (ties by name) and returns how many it left out.
func dependencyRows(edgeTraces map[edge]int64) (rows []rules.AggregateRow, omitted int) {
	edges := make([]edge, 0, len(edgeTraces))
	for e := range edgeTraces {
		edges = append(edges, e)
	}
	byName := func(a, b edge) bool {
		if a.caller != b.caller {
			return a.caller < b.caller
		}
		return a.callee < b.callee
	}
	if len(edges) > maxGraphEdges {
		sort.Slice(edges, func(i, j int) bool {
			if ti, tj := edgeTraces[edges[i]], edgeTraces[edges[j]]; ti != tj {
				return ti > tj
			}
			return byName(edges[i], edges[j])
		})
		omitted = len(edges) - maxGraphEdges
		edges = edges[:maxGraphEdges]
	}
	sort.Slice(edges, func(i, j int) bool { return byName(edges[i], edges[j]) })
	rows = make([]rules.AggregateRow, 0, len(edges))
	for _, e := range edges {
		rows = append(rows, rules.AggregateRow{
			Service:   e.caller,
			Aggregate: "service_dependency",
			Fields:    map[string]any{"callee": e.callee, "traces": edgeTraces[e]},
		})
	}
	return rows, omitted
}
