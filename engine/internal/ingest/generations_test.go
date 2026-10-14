package ingest

import (
	"testing"
	"time"
)

func TestGenerationsRotate(t *testing.T) {
	now := time.Unix(0, 0)
	g := newGenerations[string, int](10, time.Hour, func() time.Time { return now })
	g.get("a", func() int { return 1 })

	now = now.Add(59 * time.Minute)
	g.rotate()
	if len(g.cur) != 1 || g.prev != nil {
		t.Fatalf("before a window: cur=%d prev=%v, want 1 and nil", len(g.cur), g.prev)
	}

	now = now.Add(2 * time.Minute) // 61m: one window passed
	g.rotate()
	if len(g.cur) != 0 || len(g.prev) != 1 {
		t.Fatalf("after one window: cur=%d prev=%d, want 0 and 1", len(g.cur), len(g.prev))
	}

	g.get("b", func() int { return 2 })
	now = now.Add(2 * time.Hour) // idle for two windows: everything ages out
	g.rotate()
	if len(g.cur) != 0 || g.prev != nil {
		t.Fatalf("after two idle windows: cur=%d prev=%v, want both empty", len(g.cur), g.prev)
	}
}

func TestGenerationsEvictsLeastRecentlySeen(t *testing.T) {
	now := time.Unix(0, 0)
	g := newGenerations[string, int](2, time.Hour, func() time.Time { return now })
	created := 0
	newVal := func() int { created++; return created }

	g.get("a", newVal)
	g.get("b", newVal)
	g.get("a", newVal) // touching a makes b the least recently seen
	g.get("c", newVal) // full: evicts b

	if _, ok := g.cur["b"]; ok {
		t.Error("b was not evicted")
	}
	if _, ok := g.cur["a"]; !ok {
		t.Error("a was evicted although it was used more recently than b")
	}
	if g.evictions != 1 || created != 3 {
		t.Errorf("evictions=%d created=%d, want 1 and 3 (getting a again must not recreate it)", g.evictions, created)
	}
}

func TestGenerationsWithNoRoom(t *testing.T) {
	g := newGenerations[string, int](0, time.Hour, time.Now)
	if _, ok := g.get("a", func() int { return 1 }); ok {
		t.Error("a store with max 0 accepted an entry")
	}
}
