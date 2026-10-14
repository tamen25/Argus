package main

import (
	"sync"
	"time"
)

// ttlCache holds one computed value for a while, so the plugin's polling does
// not recompute an expensive report (a cost showback, a week of backtest
// replay) on every request.
//
// The lock is held while computing: concurrent requests wait for the one
// computation instead of each starting their own against the backends.
// Errors are not cached, so a transient backend failure is retried by the
// next request.
type ttlCache[T any] struct {
	ttl time.Duration
	now func() time.Time

	mu  sync.Mutex
	val *T
	at  time.Time
}

func newTTLCache[T any](ttl time.Duration) *ttlCache[T] {
	return &ttlCache[T]{ttl: ttl, now: time.Now}
}

// get returns the cached value while it is fresh, and otherwise computes and
// caches a new one.
func (c *ttlCache[T]) get(compute func() (T, error)) (*T, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.val != nil && c.now().Sub(c.at) < c.ttl {
		return c.val, nil
	}
	v, err := compute()
	if err != nil {
		return nil, err
	}
	c.val, c.at = &v, c.now()
	return c.val, nil
}
