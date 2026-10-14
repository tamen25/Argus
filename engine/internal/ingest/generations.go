package ingest

import (
	"container/list"
	"time"
)

// generations is the bounded store behind every tracker: values keyed by K
// over a two-generation tumbling window, with a hard cap on the current
// generation and least-recently-used eviction beyond it.
//
// The window: values accumulate in cur; once a window has passed, cur becomes
// prev and a fresh cur starts. After two windows with no rotation (an idle
// engine), prev is dropped too, so nothing older than two windows is ever
// reported. Readers decide how to combine the generations: the sketch trackers
// report the larger of the two, the trace tracker judges only the completed
// prev, never the in-flight cur.
//
// It is not safe for concurrent use: each tracker holds its own mutex.
type generations[K comparable, V any] struct {
	max       int // entries allowed in cur; beyond it the least recently seen is evicted
	window    time.Duration
	now       func() time.Time
	rotatedAt time.Time

	cur  map[K]*slot[V]
	prev map[K]*slot[V]
	lru  *list.List // keys of cur, most recently seen at the front

	evictions int64
}

// slot is one value and its place in the LRU list.
type slot[V any] struct {
	val  V
	elem *list.Element
}

func newGenerations[K comparable, V any](maxEntries int, window time.Duration, now func() time.Time) *generations[K, V] {
	return &generations[K, V]{
		max: maxEntries, window: window, now: now, rotatedAt: now(),
		cur: make(map[K]*slot[V]), lru: list.New(),
	}
}

// rotate moves the window forward if it has elapsed.
func (g *generations[K, V]) rotate() {
	now := g.now()
	elapsed := now.Sub(g.rotatedAt)
	if elapsed < g.window {
		return
	}
	if elapsed >= 2*g.window {
		g.prev = nil // idle for two windows or more: everything ages out
	} else {
		g.prev = g.cur
	}
	g.cur = make(map[K]*slot[V])
	g.lru.Init()
	g.rotatedAt = now
}

// get returns k's value in the current generation, creating it with create
// and evicting the least recently seen entry if the generation is full. ok is
// false only for a store with no room at all (max <= 0).
func (g *generations[K, V]) get(k K, create func() V) (v V, ok bool) {
	if s, found := g.cur[k]; found {
		g.lru.MoveToFront(s.elem)
		return s.val, true
	}
	if len(g.cur) >= g.max {
		oldest := g.lru.Back()
		if oldest == nil {
			return v, false
		}
		delete(g.cur, oldest.Value.(K))
		g.lru.Remove(oldest)
		g.evictions++
	}
	s := &slot[V]{val: create()}
	s.elem = g.lru.PushFront(k)
	g.cur[k] = s
	return s.val, true
}
