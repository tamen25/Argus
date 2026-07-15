package ingest

import (
	"container/list"
	"sort"
	"sync"
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/ptrace"

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

// svcOverflow marks spans from services beyond maxServicesPerTrace: they
// still resolve parent references but carry no caller attribution.
const svcOverflow = 0xFF

// maxGraphEdges caps distinct (caller, callee) pairs per Rows() call — a
// defensive bound; real fleets sit at (distinct services)² far below it.
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
	elem    *list.Element
}

// TraceTracker accumulates per-trace span topology on the sampled stream to
// evaluate trace completeness (SPA-002 orphans, ARG-SPA-002 missing roots).
//
// Judgement happens on the *previous* generation only: an in-flight trace
// must not be scored while its spans may still arrive. Same two-generation
// tumbling clock as SketchTracker.
//
// SAMPLING CAVEAT (reported via rule docs + confidence=sampled): with
// non-tail-based sampling, parts of a trace can be legitimately absent from
// the mirror. Ratios here are upper bounds; thresholds default accordingly.
type TraceTracker struct {
	mu        sync.Mutex
	max       int
	window    time.Duration
	now       func() time.Time
	rotatedAt time.Time

	cur  map[pcommon.TraceID]*traceState
	lru  *list.List
	prev map[pcommon.TraceID]*traceState

	evictions int64
}

// NewTraceTracker builds a tracker capped at maxTraces per generation.
func NewTraceTracker(maxTraces int, window time.Duration, now func() time.Time) *TraceTracker {
	return &TraceTracker{
		max: maxTraces, window: window, now: now, rotatedAt: now(),
		cur: make(map[pcommon.TraceID]*traceState), lru: list.New(),
	}
}

func (t *TraceTracker) rotateLocked() {
	now := t.now()
	elapsed := now.Sub(t.rotatedAt)
	if elapsed < t.window {
		return
	}
	if elapsed >= 2*t.window {
		t.prev = nil
	} else {
		t.prev = t.cur
	}
	t.cur = make(map[pcommon.TraceID]*traceState)
	t.lru.Init()
	t.rotatedAt = now
}

// ObserveTraces records span topology from one OTLP payload.
func (t *TraceTracker) ObserveTraces(td ptrace.Traces) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.rotateLocked()

	rss := td.ResourceSpans()
	for i := 0; i < rss.Len(); i++ {
		rs := rss.At(i)
		svc := "<unknown>"
		if v, ok := rs.Resource().Attributes().Get("service.name"); ok && v.Str() != "" {
			svc = v.Str()
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
	id := sp.TraceID()
	st, ok := t.cur[id]
	if !ok {
		if len(t.cur) >= t.max {
			back := t.lru.Back()
			if back == nil {
				return
			}
			victim := back.Value.(pcommon.TraceID)
			delete(t.cur, victim)
			t.lru.Remove(back)
			t.evictions++
		}
		st = &traceState{
			spans:   make(map[pcommon.SpanID]uint8, 8),
			parents: make(map[pcommon.SpanID]map[string]struct{}, 8),
			svcIdx:  make(map[string]uint8, 4),
		}
		st.elem = t.lru.PushFront(id)
		t.cur[id] = st
	} else {
		t.lru.MoveToFront(st.elem)
	}
	idx, seen := st.svcIdx[svc]
	if !seen {
		if len(st.svcList) < maxServicesPerTrace {
			idx = uint8(len(st.svcList))
			st.svcIdx[svc] = idx
			st.svcList = append(st.svcList, svc)
		} else {
			idx = svcOverflow
		}
	}
	if len(st.spans) < maxSpansPerTrace {
		st.spans[sp.SpanID()] = idx
	}
	if sp.ParentSpanID().IsEmpty() {
		st.hasRoot = true
	} else if refs, seen := st.parents[sp.ParentSpanID()]; seen {
		if len(refs) < maxServicesPerTrace {
			refs[svc] = struct{}{}
		}
	} else if len(st.parents) < maxSpansPerTrace {
		st.parents[sp.ParentSpanID()] = map[string]struct{}{svc: {}}
	}
}

// Evictions returns LRU-evicted trace count.
func (t *TraceTracker) Evictions() int64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.evictions
}

// TracesTracked returns the live trace count in the current generation.
func (t *TraceTracker) TracesTracked() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.rotateLocked()
	return len(t.cur)
}

// Rows emits one trace_health aggregate per service from the completed
// (previous) generation: traces the service participated in, and the
// fraction of those with orphaned spans / no root — attributed to the
// services whose spans reference the unresolved parents (the visible break
// point), never collectively.
func (t *TraceTracker) Rows() []rules.AggregateRow {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.rotateLocked()

	type counts struct{ traces, orphaned, rootless int64 }
	perSvc := map[string]*counts{}
	svcCounts := func(svc string) *counts {
		c, ok := perSvc[svc]
		if !ok {
			c = &counts{}
			perSvc[svc] = c
		}
		return c
	}

	type edge struct{ caller, callee string }
	edgeTraces := map[edge]int64{}

	for _, st := range t.prev {
		for svc := range st.svcIdx {
			svcCounts(svc).traces++
		}
		// services holding a dangling parent reference in this trace
		breakPoints := map[string]struct{}{}
		// resolved cross-service references: caller→callee edges, deduped
		// per trace so an edge counts traces, not spans
		seenEdges := map[edge]struct{}{}
		for p, refs := range st.parents {
			idx, resolved := st.spans[p]
			if !resolved {
				for svc := range refs {
					breakPoints[svc] = struct{}{}
				}
				continue
			}
			if idx == svcOverflow {
				continue
			}
			caller := st.svcList[idx]
			for callee := range refs {
				e := edge{caller, callee}
				if callee == caller {
					continue
				}
				if _, dup := seenEdges[e]; dup {
					continue
				}
				seenEdges[e] = struct{}{}
				if _, known := edgeTraces[e]; known || len(edgeTraces) < maxGraphEdges {
					edgeTraces[e]++
				}
			}
		}
		for svc := range breakPoints {
			svcCounts(svc).orphaned++
		}
		if !st.hasRoot {
			if len(breakPoints) > 0 {
				for svc := range breakPoints {
					svcCounts(svc).rootless++
				}
			} else {
				// no root and no visible break (cap truncation): honest upper
				// bound — every participant carries it
				for svc := range st.svcIdx {
					svcCounts(svc).rootless++
				}
			}
		}
	}

	rows := make([]rules.AggregateRow, 0, len(perSvc)+len(edgeTraces))
	for svc, c := range perSvc {
		rows = append(rows, rules.AggregateRow{
			Service:   svc,
			Aggregate: "trace_health",
			Fields: map[string]any{
				"traces":             c.traces,
				"orphan_ratio":       float64(c.orphaned) / float64(c.traces),
				"missing_root_ratio": float64(c.rootless) / float64(c.traces),
			},
		})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Service < rows[j].Service })

	edges := make([]edge, 0, len(edgeTraces))
	for e := range edgeTraces {
		edges = append(edges, e)
	}
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].caller != edges[j].caller {
			return edges[i].caller < edges[j].caller
		}
		return edges[i].callee < edges[j].callee
	})
	for _, e := range edges {
		rows = append(rows, rules.AggregateRow{
			Service:   e.caller,
			Aggregate: "service_dependency",
			Fields:    map[string]any{"callee": e.callee, "traces": edgeTraces[e]},
		})
	}
	return rows
}
