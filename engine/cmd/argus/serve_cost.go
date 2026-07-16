package main

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/tamen25/Argus/engine/internal/cost"
	"github.com/tamen25/Argus/engine/internal/store"
)

// registerCostEndpoint mounts /api/cost. With no --cost-pricing it mounts the
// 404 "not configured" handler and returns a nil closer. Otherwise it loads
// pricing, builds the configured sources, optionally opens a store for trends,
// and mounts the cached live endpoint — returning a closer for the store.
func registerCostEndpoint(ctx context.Context, mux *http.ServeMux, cfg serveCostConfig) (func(), error) {
	if cfg.pricingPath == "" {
		mux.HandleFunc("/api/cost", notConfiguredCostHandler)
		return nil, nil
	}
	pricing, err := cost.LoadPricing(cfg.pricingPath)
	if err != nil {
		return nil, fmt.Errorf("cost pricing: %w", err)
	}
	srcs, err := buildCostSources(ctx, cfg.costSourceConfig)
	if err != nil {
		return nil, fmt.Errorf("cost sources: %w", err)
	}

	var snapStore cost.SnapshotStore
	var closer func()
	if cfg.storeDSN != "" {
		pg, err := store.Open(ctx, cfg.storeDSN)
		if err != nil {
			return nil, fmt.Errorf("cost store: %w", err)
		}
		snapStore = pg
		closer = pg.Close
	}

	ep := newCostEndpoint(pricing, srcs, snapStore, cfg.window, cfg.cacheTTL)
	mux.HandleFunc("/api/cost", ep.handler())
	return closer, nil
}

// costEndpoint serves /api/cost from the cost pipeline, caching the showback
// for a short TTL so the plugin's polling doesn't hammer the backends (and,
// with a store, doesn't persist a snapshot on every scrape).
type costEndpoint struct {
	pricing *cost.Pricing
	srcs    cost.Sources
	store   cost.SnapshotStore
	window  time.Duration
	ttl     time.Duration
	now     func() time.Time

	mu       sync.Mutex
	cached   *cost.Showback
	cachedAt time.Time
}

func newCostEndpoint(pricing *cost.Pricing, srcs cost.Sources, store cost.SnapshotStore, window, ttl time.Duration) *costEndpoint {
	return &costEndpoint{pricing: pricing, srcs: srcs, store: store, window: window, ttl: ttl, now: time.Now}
}

// get returns a cached showback within the TTL, otherwise recomputes it.
// Errors are not cached: a transient backend failure retries next request.
func (c *costEndpoint) get(ctx context.Context) (*cost.Showback, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cached != nil && c.now().Sub(c.cachedAt) < c.ttl {
		return c.cached, nil
	}
	sb, err := cost.Assemble(ctx, c.pricing, c.srcs, c.window, c.store, c.now())
	if err != nil {
		return nil, err
	}
	c.cached = &sb
	c.cachedAt = c.now()
	return c.cached, nil
}

func (c *costEndpoint) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sb, err := c.get(r.Context())
		if err != nil {
			// backend unreachable: a gateway error, never a fake $0 report
			http.Error(w, "cost sources unavailable: "+err.Error(), http.StatusBadGateway)
			return
		}
		writeJSON(w, sb)
	}
}

// notConfiguredCostHandler answers /api/cost when no pricing is configured —
// a 404 the plugin renders as "not configured", never an empty $0 report.
func notConfiguredCostHandler(w http.ResponseWriter, _ *http.Request) {
	http.Error(w, "cost reporting is not configured (start the engine with --cost-pricing)", http.StatusNotFound)
}
