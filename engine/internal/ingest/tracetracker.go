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
// trace state holds span/parent ID sets (8 bytes each, span count capped),
// so worst case ≈ cap × maxSpansPerTrace × 16B ≈ 64MiB at 8192 × 512.
const DefaultMaxTrackedTraces = 8192

// maxSpansPerTrace caps per-trace span-ID retention (bounded memory).
const maxSpansPerTrace = 512

type traceKey struct {
	service string
	traceID pcommon.TraceID
}

type traceState struct {
	spans   map[pcommon.SpanID]struct{}
	parents map[pcommon.SpanID]struct{}
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

	cur  map[traceKey]*traceState
	lru  *list.List
	prev map[traceKey]*traceState

	evictions int64
}

// NewTraceTracker builds a tracker capped at maxTraces per generation.
func NewTraceTracker(maxTraces int, window time.Duration, now func() time.Time) *TraceTracker {
	return &TraceTracker{
		max: maxTraces, window: window, now: now, rotatedAt: now(),
		cur: make(map[traceKey]*traceState), lru: list.New(),
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
	t.cur = make(map[traceKey]*traceState)
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
				t.observeSpanLocked(traceKey{svc, sp.TraceID()}, sp)
			}
		}
	}
}

func (t *TraceTracker) observeSpanLocked(k traceKey, sp ptrace.Span) {
	st, ok := t.cur[k]
	if !ok {
		if len(t.cur) >= t.max {
			back := t.lru.Back()
			if back == nil {
				return
			}
			victim := back.Value.(traceKey)
			delete(t.cur, victim)
			t.lru.Remove(back)
			t.evictions++
		}
		st = &traceState{spans: make(map[pcommon.SpanID]struct{}, 8), parents: make(map[pcommon.SpanID]struct{}, 8)}
		st.elem = t.lru.PushFront(k)
		t.cur[k] = st
	} else {
		t.lru.MoveToFront(st.elem)
	}
	if len(st.spans) < maxSpansPerTrace {
		st.spans[sp.SpanID()] = struct{}{}
	}
	if sp.ParentSpanID().IsEmpty() {
		st.hasRoot = true
	} else if len(st.parents) < maxSpansPerTrace {
		st.parents[sp.ParentSpanID()] = struct{}{}
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
// (previous) generation: traces, orphan_ratio, missing_root_ratio.
func (t *TraceTracker) Rows() []rules.AggregateRow {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.rotateLocked()

	type counts struct{ traces, orphaned, rootless int64 }
	perSvc := map[string]*counts{}
	for k, st := range t.prev {
		c, ok := perSvc[k.service]
		if !ok {
			c = &counts{}
			perSvc[k.service] = c
		}
		c.traces++
		if !st.hasRoot {
			c.rootless++
		}
		for p := range st.parents {
			if _, seen := st.spans[p]; !seen {
				c.orphaned++
				break
			}
		}
	}

	rows := make([]rules.AggregateRow, 0, len(perSvc))
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
	return rows
}
