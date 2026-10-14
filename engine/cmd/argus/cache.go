package main

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"
)

// errStillComputing is returned when a caller stops waiting for a
// computation that has not finished. The computation goes on, and the next
// caller gets its result from the cache.
var errStillComputing = errors.New("still being computed")

// ttlCache holds one computed value for a while, so the plugin's polling does
// not recompute an expensive report (a cost showback, a week of backtest
// replay) on every request.
//
// A computation runs on its own goroutine and its own context: detached from
// the request that started it, and bounded by the cache's timeout. A request
// that gives up (the plugin's proxy times out after ten seconds, a browser
// tab closes) used to cancel the computation with it, so a replay longer than
// the timeout never reached the cache and every load started it again.
// Concurrent callers share the one computation in flight, and each waits only
// as long as its own context allows. Errors are not cached, so a transient
// backend failure is retried by the next request.
type ttlCache[T any] struct {
	ttl     time.Duration
	timeout time.Duration // bounds one computation
	now     func() time.Time

	mu       sync.Mutex
	val      *T
	at       time.Time
	inflight *flight[T]
}

// flight is one computation: done is closed when val or err is set.
type flight[T any] struct {
	done chan struct{}
	val  *T
	err  error
}

func newTTLCache[T any](ttl, timeout time.Duration) *ttlCache[T] {
	return &ttlCache[T]{ttl: ttl, timeout: timeout, now: time.Now}
}

// get returns the cached value while it is fresh. Otherwise it joins the
// computation in flight, or starts one, and waits for it until ctx is done,
// returning errStillComputing if that comes first.
func (c *ttlCache[T]) get(ctx context.Context, compute func(context.Context) (T, error)) (*T, error) {
	c.mu.Lock()
	if c.val != nil && c.now().Sub(c.at) < c.ttl {
		v := c.val
		c.mu.Unlock()
		return v, nil
	}
	f := c.inflight
	if f == nil {
		f = &flight[T]{done: make(chan struct{})}
		c.inflight = f
		go c.run(ctx, f, compute)
	}
	c.mu.Unlock()

	select {
	case <-f.done:
		return f.val, f.err
	case <-ctx.Done():
		return nil, errStillComputing
	}
}

// run performs one computation and stores its result. Its context keeps
// ctx's values but not its cancellation (context.WithoutCancel), so the
// caller that started it cannot stop it, and is bounded by the timeout.
func (c *ttlCache[T]) run(ctx context.Context, f *flight[T], compute func(context.Context) (T, error)) {
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), c.timeout)
	defer cancel()
	v, err := compute(cctx)

	c.mu.Lock()
	if err == nil {
		c.val, c.at = &v, c.now()
		f.val = c.val
	}
	f.err = err
	c.inflight = nil
	c.mu.Unlock()
	close(f.done)
}

// Timeouts for the cached endpoints. A request waits maxResponseWait, well
// inside the Grafana plugin's 10-second proxy timeout; a computation that
// takes longer goes on in the background and the client is told to retry.
const (
	backtestComputeTimeout = 10 * time.Minute
	costComputeTimeout     = 2 * time.Minute
	// retryAfterSeconds is the Retry-After sent while a report is computing.
	retryAfterSeconds = "10"
)

// maxResponseWait is a variable only so tests can shorten it.
var maxResponseWait = 5 * time.Second

// stillComputing answers a request whose report is not ready yet: 503 with
// Retry-After. The plugin's pages show that the report is computing and ask
// again, and the computation keeps running meanwhile.
func stillComputing(w http.ResponseWriter, what string) {
	w.Header().Set("Retry-After", retryAfterSeconds)
	http.Error(w, what+" is still being computed; try again shortly", http.StatusServiceUnavailable)
}
