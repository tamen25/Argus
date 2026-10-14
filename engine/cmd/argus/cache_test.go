package main

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestTTLCache(t *testing.T) {
	ctx := context.Background()
	now := time.Unix(0, 0)
	c := newTTLCache[int](time.Minute, time.Minute)
	c.now = func() time.Time { return now }
	calls := 0
	compute := func(context.Context) (int, error) { calls++; return calls, nil }

	if v, _ := c.get(ctx, compute); *v != 1 {
		t.Fatalf("first get = %d, want 1", *v)
	}
	now = now.Add(30 * time.Second)
	if v, _ := c.get(ctx, compute); *v != 1 || calls != 1 {
		t.Errorf("fresh get = %d after %d computes, want the cached 1", *v, calls)
	}
	now = now.Add(time.Minute)
	if v, _ := c.get(ctx, compute); *v != 2 {
		t.Errorf("stale get = %d, want a recompute (2)", *v)
	}

	// An error is returned and not cached: the next get computes again.
	c = newTTLCache[int](time.Minute, time.Minute)
	if _, err := c.get(ctx, func(context.Context) (int, error) { return 0, errors.New("backend down") }); err == nil {
		t.Error("the error was swallowed")
	}
	if v, err := c.get(ctx, func(context.Context) (int, error) { return 7, nil }); err != nil || *v != 7 {
		t.Errorf("after an error: %v, %v; want a fresh compute", v, err)
	}
}

// R13: a caller that stops waiting does not cancel the computation. The
// plugin's proxy gives up after ten seconds, and a week of replay takes
// longer; the computation used to die with the request, so it never reached
// the cache and every load started it again.
func TestTTLCacheOutlivesTheCaller(t *testing.T) {
	c := newTTLCache[int](time.Minute, time.Minute)
	release := make(chan struct{})
	var computeCancelled atomic.Bool
	compute := func(ctx context.Context) (int, error) {
		select {
		case <-release:
			return 42, nil
		case <-ctx.Done():
			computeCancelled.Store(true)
			return 0, ctx.Err()
		}
	}

	waiting, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := c.get(waiting, compute); !errors.Is(err, errStillComputing) {
		t.Fatalf("err = %v, want errStillComputing when the caller gives up first", err)
	}
	close(release)

	v, err := c.get(context.Background(), func(context.Context) (int, error) {
		t.Error("a second computation started while the first was in flight or cached")
		return 0, nil
	})
	if err != nil || *v != 42 || computeCancelled.Load() {
		t.Errorf("got %v, %v (cancelled %v); want the first computation's 42", v, err, computeCancelled.Load())
	}
}

// Concurrent callers share the computation in flight instead of each
// starting one against the backends.
func TestTTLCacheSharesOneComputation(t *testing.T) {
	c := newTTLCache[int](time.Minute, time.Minute)
	var computes atomic.Int32
	release := make(chan struct{})
	compute := func(context.Context) (int, error) { //nolint:unparam // matches the cache's compute signature
		computes.Add(1)
		<-release
		return 1, nil
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if v, err := c.get(context.Background(), compute); err != nil || *v != 1 {
				t.Errorf("get = %v, %v", v, err)
			}
		}()
	}
	time.Sleep(20 * time.Millisecond) // let every caller join
	close(release)
	wg.Wait()
	if n := computes.Load(); n != 1 {
		t.Errorf("%d computations for 8 concurrent callers, want 1", n)
	}
}
