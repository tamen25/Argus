package main

import (
	"errors"
	"testing"
	"time"
)

func TestTTLCache(t *testing.T) {
	now := time.Unix(0, 0)
	c := newTTLCache[int](time.Minute)
	c.now = func() time.Time { return now }
	calls := 0
	compute := func() (int, error) { calls++; return calls, nil }

	if v, _ := c.get(compute); *v != 1 {
		t.Fatalf("first get = %d, want 1", *v)
	}
	now = now.Add(30 * time.Second)
	if v, _ := c.get(compute); *v != 1 || calls != 1 {
		t.Errorf("fresh get = %d after %d computes, want the cached 1", *v, calls)
	}
	now = now.Add(time.Minute)
	if v, _ := c.get(compute); *v != 2 {
		t.Errorf("stale get = %d, want a recompute (2)", *v)
	}

	// An error is returned and not cached: the next get computes again.
	c = newTTLCache[int](time.Minute)
	if _, err := c.get(func() (int, error) { return 0, errors.New("backend down") }); err == nil {
		t.Error("the error was swallowed")
	}
	if v, err := c.get(func() (int, error) { return 7, nil }); err != nil || *v != 7 {
		t.Errorf("after an error: %v, %v; want a fresh compute", v, err)
	}
}
