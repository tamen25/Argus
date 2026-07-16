package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tamen25/Argus/engine/internal/cost"
)

type countingSeries struct {
	calls *int
	m     map[string]int64
}

func (c countingSeries) ActiveSeriesByService(context.Context) (map[string]int64, error) {
	*c.calls++
	return c.m, nil
}

func testCostEndpoint(t *testing.T, ttl time.Duration, calls *int) *costEndpoint {
	t.Helper()
	p := &cost.Pricing{Schema: cost.PricingSchema, Currency: "USD", ActiveSeries: cost.ActiveSeries{PerMillion: 8.0}}
	srcs := cost.Sources{Series: countingSeries{calls: calls, m: map[string]int64{"checkout": 1_000_000}}}
	return newCostEndpoint(p, srcs, nil, time.Hour, ttl)
}

// The endpoint serves the priced showback and caches it: a second request
// within the TTL must not re-hit the backend.
func TestCostEndpointCaches(t *testing.T) {
	calls := 0
	ep := testCostEndpoint(t, time.Minute, &calls)

	for i := 0; i < 3; i++ {
		rec := httptest.NewRecorder()
		ep.handler()(rec, httptest.NewRequest(http.MethodGet, "/api/cost", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d", rec.Code)
		}
		var sb cost.Showback
		if err := json.Unmarshal(rec.Body.Bytes(), &sb); err != nil {
			t.Fatal(err)
		}
		if sb.Report.TotalMonthly != 8.0 {
			t.Errorf("total = %v, want 8.0", sb.Report.TotalMonthly)
		}
	}
	if calls != 1 {
		t.Errorf("backend hit %d times, want 1 (cached)", calls)
	}
}

// Past the TTL the endpoint refreshes.
func TestCostEndpointRefreshesAfterTTL(t *testing.T) {
	calls := 0
	ep := testCostEndpoint(t, time.Minute, &calls)
	now := time.Unix(1_700_000_000, 0)
	ep.now = func() time.Time { return now }

	_, _ = ep.get(context.Background())
	now = now.Add(2 * time.Minute) // past TTL
	_, _ = ep.get(context.Background())

	if calls != 2 {
		t.Errorf("backend hit %d times, want 2 (refreshed after TTL)", calls)
	}
}

// When no pricing is configured the endpoint 404s (the plugin renders "not
// configured"), never a fake empty report.
func TestCostNotConfiguredReturns404(t *testing.T) {
	rec := httptest.NewRecorder()
	notConfiguredCostHandler(rec, httptest.NewRequest(http.MethodGet, "/api/cost", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

// registerCostEndpoint mounts the 404 handler with no pricing and no store
// closer to clean up.
func TestRegisterCostEndpointNotConfigured(t *testing.T) {
	mux := http.NewServeMux()
	closer, err := registerCostEndpoint(context.Background(), mux, serveCostConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if closer != nil {
		t.Error("no store configured, want nil closer")
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/cost", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

// With pricing + a source configured the endpoint mounts; an unreachable
// backend yields a gateway error, never a fake report.
func TestRegisterCostEndpointConfigured(t *testing.T) {
	mux := http.NewServeMux()
	closer, err := registerCostEndpoint(context.Background(), mux, serveCostConfig{
		pricingPath:      writePricing(t),
		window:           time.Hour,
		cacheTTL:         time.Minute,
		costSourceConfig: costSourceConfig{mimirURL: "http://127.0.0.1:1", serviceLabel: "service_name"},
	})
	if err != nil {
		t.Fatalf("registration failed: %v", err)
	}
	if closer != nil {
		t.Error("no store configured, want nil closer")
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/cost", nil))
	if rec.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want 502 (mimir unreachable)", rec.Code)
	}
}
