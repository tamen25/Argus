package ingest

import (
	"container/list"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/axiomhq/hyperloglog"

	"github.com/tamen25/Argus/engine/internal/rules"
)

// DefaultMaxTrackedPairs bounds distinct sketch entries per generation.
// Memory envelope: a dense HLL-14 sketch is ~16KiB; worst case ≈ cap × 16KiB
// × 2 generations ≈ 128MiB at 4096 — in practice far lower because
// low-cardinality entries stay in sparse representation.
const DefaultMaxTrackedPairs = 4096

// DefaultWindow is the tumbling-window length for aggregate sketches,
// matching MET-001's 1-hour criteria window.
const DefaultWindow = time.Hour

type sketchKey struct {
	service string
	dims    string // joined dim values, \x00-separated
}

type sketchEntry struct {
	sketch *hyperloglog.Sketch
	elem   *list.Element // position in the LRU list (current generation only)
	dims   []string
}

// SketchTracker estimates distinct values per (service, dims...) with
// HyperLogLog sketches — never exact sets (architecture rule 3). One named
// aggregate per tracker instance; dimNames label the dims in emitted rows.
//
// Window semantics: two-generation tumbling; estimates report
// max(current, previous) — see docs/rules/authoring.md.
// Admission: hard cap per generation with LRU eviction, evictions counted.
type SketchTracker struct {
	mu        sync.Mutex
	aggregate string
	dimNames  []string
	max       int
	window    time.Duration
	now       func() time.Time
	rotatedAt time.Time

	cur  map[sketchKey]*sketchEntry
	lru  *list.List // front = most recently observed; values are sketchKey
	prev map[sketchKey]*sketchEntry

	evictions int64
}

// NewSketchTracker builds a named tracker.
func NewSketchTracker(aggregate string, dimNames []string, maxEntries int, window time.Duration, now func() time.Time) *SketchTracker {
	return &SketchTracker{
		aggregate: aggregate, dimNames: dimNames,
		max: maxEntries, window: window, now: now, rotatedAt: now(),
		cur: make(map[sketchKey]*sketchEntry), lru: list.New(),
	}
}

// CardinalityTracker is the metric-attribute instance of SketchTracker.
type CardinalityTracker = SketchTracker

// NewCardinalityTracker builds the metric_attribute_cardinality tracker with
// the default 1h tumbling window.
func NewCardinalityTracker(maxPairs int) *CardinalityTracker {
	return NewCardinalityTrackerWithClock(maxPairs, DefaultWindow, time.Now)
}

// NewCardinalityTrackerWithClock injects window length and clock (tests).
func NewCardinalityTrackerWithClock(maxPairs int, window time.Duration, now func() time.Time) *CardinalityTracker {
	return NewSketchTracker("metric_attribute_cardinality", []string{"metric", "attribute"}, maxPairs, window, now)
}

// rotateLocked ages generations forward as needed. Called with mu held.
func (t *SketchTracker) rotateLocked() {
	now := t.now()
	elapsed := now.Sub(t.rotatedAt)
	if elapsed < t.window {
		return
	}
	if elapsed >= 2*t.window {
		t.prev = nil // idle for 2+ windows: everything ages out
	} else {
		t.prev = t.cur
	}
	t.cur = make(map[sketchKey]*sketchEntry)
	t.lru.Init()
	t.rotatedAt = now
}

// Observe records one value occurrence for (service, dims...).
func (t *SketchTracker) Observe(service string, dims []string, value string) {
	k := sketchKey{service, strings.Join(dims, "\x00")}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.rotateLocked()

	e, ok := t.cur[k]
	if !ok {
		if len(t.cur) >= t.max {
			back := t.lru.Back()
			if back == nil {
				return // max <= 0
			}
			victim := back.Value.(sketchKey)
			delete(t.cur, victim)
			t.lru.Remove(back)
			t.evictions++
		}
		e = &sketchEntry{sketch: hyperloglog.New14(), dims: append([]string(nil), dims...)}
		e.elem = t.lru.PushFront(k)
		t.cur[k] = e
	} else {
		t.lru.MoveToFront(e.elem)
	}
	e.sketch.Insert([]byte(value))
}

// Evictions returns how many entries were LRU-evicted (reported as a
// self-metric, never silently dropped — architecture rule 7).
func (t *SketchTracker) Evictions() int64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.evictions
}

// PairsTracked returns the live entry count across both generations.
func (t *SketchTracker) PairsTracked() int {
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

// Rows snapshots every entry as an aggregate row, reporting
// max(current, previous) per key.
func (t *SketchTracker) Rows() []rules.AggregateRow {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.rotateLocked()

	type acc struct {
		dims []string
		est  uint64
	}
	est := make(map[sketchKey]acc, len(t.cur)+len(t.prev))
	for k, e := range t.cur {
		est[k] = acc{e.dims, e.sketch.Estimate()}
	}
	for k, e := range t.prev {
		if v := e.sketch.Estimate(); v > est[k].est {
			est[k] = acc{e.dims, v}
		}
	}

	rows := make([]rules.AggregateRow, 0, len(est))
	for k, a := range est {
		fields := make(map[string]any, len(t.dimNames)+1)
		for i, name := range t.dimNames {
			if i < len(a.dims) {
				fields[name] = a.dims[i]
			}
		}
		fields["cardinality"] = int64(a.est)
		rows = append(rows, rules.AggregateRow{Service: k.service, Aggregate: t.aggregate, Fields: fields})
	}
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.Service != b.Service {
			return a.Service < b.Service
		}
		return fieldsKey(a.Fields, t.dimNames) < fieldsKey(b.Fields, t.dimNames)
	})
	return rows
}

func fieldsKey(f map[string]any, names []string) string {
	var b strings.Builder
	for _, n := range names {
		if s, ok := f[n].(string); ok {
			b.WriteString(s)
		}
		b.WriteByte(0)
	}
	return b.String()
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
