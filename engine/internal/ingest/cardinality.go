package ingest

import (
	"container/list"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/axiomhq/hyperloglog"

	"github.com/tamen25/Argus/engine/internal/rules"
)

// DefaultMaxTrackedPairs bounds distinct (service, metric, attribute) sketch
// entries per generation. Memory envelope: a dense HLL-14 sketch is ~16KiB;
// worst case ≈ cap × 16KiB × 2 generations ≈ 128MiB at 4096 — in practice far
// lower because low-cardinality pairs stay in sparse representation.
const DefaultMaxTrackedPairs = 4096

// DefaultWindow is the tumbling-window length for aggregate sketches,
// matching MET-001's 1-hour criteria window.
const DefaultWindow = time.Hour

type pairKey struct {
	service, metric, attr string
}

type pairEntry struct {
	sketch *hyperloglog.Sketch
	elem   *list.Element // position in the LRU list (current generation only)
}

// CardinalityTracker estimates distinct attribute values per
// (service, metric, attribute) with HyperLogLog sketches — never exact sets
// (architecture rule 3).
//
// Window semantics: two-generation tumbling. Observations land in the
// current generation; every window the current generation becomes the
// previous one and a fresh current starts. Estimates report
// max(current, previous), so a finding never vanishes at a window boundary —
// a value set stops influencing estimates only after it has been silent for
// between one and two full windows.
//
// Admission: hard cap per generation with LRU eviction — the least recently
// observed pair is evicted for a new one, and evictions are counted (exposed
// as argus_aggregate_pair_evictions_total; honest reporting, never silent).
type CardinalityTracker struct {
	mu        sync.Mutex
	max       int
	window    time.Duration
	now       func() time.Time
	rotatedAt time.Time

	cur  map[pairKey]*pairEntry
	lru  *list.List // front = most recently observed; values are pairKey
	prev map[pairKey]*hyperloglog.Sketch

	evictions int64
}

// NewCardinalityTracker builds a tracker with the default 1h tumbling window.
func NewCardinalityTracker(maxPairs int) *CardinalityTracker {
	return NewCardinalityTrackerWithClock(maxPairs, DefaultWindow, time.Now)
}

// NewCardinalityTrackerWithClock injects window length and clock (tests).
func NewCardinalityTrackerWithClock(maxPairs int, window time.Duration, now func() time.Time) *CardinalityTracker {
	return &CardinalityTracker{
		max: maxPairs, window: window, now: now, rotatedAt: now(),
		cur: make(map[pairKey]*pairEntry), lru: list.New(),
	}
}

// rotateLocked ages generations forward as needed. Called with mu held.
func (t *CardinalityTracker) rotateLocked() {
	now := t.now()
	elapsed := now.Sub(t.rotatedAt)
	if elapsed < t.window {
		return
	}
	if elapsed >= 2*t.window {
		// idle for 2+ windows: everything ages out
		t.prev = nil
	} else {
		t.prev = make(map[pairKey]*hyperloglog.Sketch, len(t.cur))
		for k, e := range t.cur {
			t.prev[k] = e.sketch
		}
	}
	t.cur = make(map[pairKey]*pairEntry)
	t.lru.Init()
	t.rotatedAt = now
}

// Observe records one attribute value occurrence.
func (t *CardinalityTracker) Observe(service, metric, attr, value string) {
	k := pairKey{service, metric, attr}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.rotateLocked()

	e, ok := t.cur[k]
	if !ok {
		if len(t.cur) >= t.max {
			// evict the least recently observed pair
			back := t.lru.Back()
			if back == nil {
				return // max <= 0
			}
			victim := back.Value.(pairKey)
			delete(t.cur, victim)
			t.lru.Remove(back)
			t.evictions++
		}
		e = &pairEntry{sketch: hyperloglog.New14()}
		e.elem = t.lru.PushFront(k)
		t.cur[k] = e
	} else {
		t.lru.MoveToFront(e.elem)
	}
	e.sketch.Insert([]byte(value))
}

// Evictions returns how many pairs were LRU-evicted (reported as a
// self-metric, never silently dropped — architecture rule 7).
func (t *CardinalityTracker) Evictions() int64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.evictions
}

// PairsTracked returns the live pair count across both generations.
func (t *CardinalityTracker) PairsTracked() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.rotateLocked()
	n := len(t.cur)
	for k := range t.prev {
		if _, in := t.cur[k]; !in {
			n++
		}
	}
	return n
}

// Rows snapshots every pair as a metric_attribute_cardinality aggregate row,
// reporting max(current, previous) per pair.
func (t *CardinalityTracker) Rows() []rules.AggregateRow {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.rotateLocked()

	est := make(map[pairKey]uint64, len(t.cur)+len(t.prev))
	for k, e := range t.cur {
		est[k] = e.sketch.Estimate()
	}
	for k, s := range t.prev {
		if v := s.Estimate(); v > est[k] {
			est[k] = v
		}
	}

	rows := make([]rules.AggregateRow, 0, len(est))
	for k, v := range est {
		rows = append(rows, rules.AggregateRow{
			Service:   k.service,
			Aggregate: "metric_attribute_cardinality",
			Fields: map[string]any{
				"metric":      k.metric,
				"attribute":   k.attr,
				"cardinality": int64(v),
			},
		})
	}
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.Service != b.Service {
			return a.Service < b.Service
		}
		if a.Fields["metric"] != b.Fields["metric"] {
			return a.Fields["metric"].(string) < b.Fields["metric"].(string)
		}
		return a.Fields["attribute"].(string) < b.Fields["attribute"].(string)
	})
	return rows
}

func stringify(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		return strconv.FormatFloat(x, 'g', -1, 64)
	case bool:
		return strconv.FormatBool(x)
	default:
		return ""
	}
}
