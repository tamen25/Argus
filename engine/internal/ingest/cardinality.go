package ingest

import (
	"sort"
	"strconv"
	"sync"

	"github.com/axiomhq/hyperloglog"

	"github.com/tamen25/Argus/engine/internal/rules"
)

// DefaultMaxTrackedPairs bounds distinct (service, metric, attribute) sketch
// entries. Beyond the cap, new pairs are counted in Overflowed instead of
// tracked — memory stays bounded no matter what the stream sends.
const DefaultMaxTrackedPairs = 10000

type pairKey struct {
	service, metric, attr string
}

// CardinalityTracker estimates distinct attribute values per
// (service, metric, attribute) with HyperLogLog sketches — never exact sets
// (architecture rule 3).
type CardinalityTracker struct {
	mu        sync.Mutex
	max       int
	sketches  map[pairKey]*hyperloglog.Sketch
	overflown int64
}

// NewCardinalityTracker builds a tracker capped at maxPairs sketches.
func NewCardinalityTracker(maxPairs int) *CardinalityTracker {
	return &CardinalityTracker{max: maxPairs, sketches: make(map[pairKey]*hyperloglog.Sketch)}
}

// Observe records one attribute value occurrence.
func (t *CardinalityTracker) Observe(service, metric, attr, value string) {
	k := pairKey{service, metric, attr}
	t.mu.Lock()
	defer t.mu.Unlock()
	s, ok := t.sketches[k]
	if !ok {
		if len(t.sketches) >= t.max {
			t.overflown++
			return
		}
		s = hyperloglog.New14()
		t.sketches[k] = s
	}
	s.Insert([]byte(value))
}

// Overflowed returns how many observations hit the pair cap (reported, never
// silently dropped — architecture rule 7).
func (t *CardinalityTracker) Overflowed() int64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.overflown
}

// Rows snapshots every sketch as a metric_attribute_cardinality aggregate row.
func (t *CardinalityTracker) Rows() []rules.AggregateRow {
	t.mu.Lock()
	defer t.mu.Unlock()
	rows := make([]rules.AggregateRow, 0, len(t.sketches))
	for k, s := range t.sketches {
		rows = append(rows, rules.AggregateRow{
			Service:   k.service,
			Aggregate: "metric_attribute_cardinality",
			Fields: map[string]any{
				"metric":      k.metric,
				"attribute":   k.attr,
				"cardinality": int64(s.Estimate()),
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
