package backtest_test

import (
	"context"
	"testing"
	"time"

	"github.com/tamen25/Argus/engine/internal/backtest"
)

// fakeQuerier has data from `from` onward — the shape of a Mimir whose
// retention (or first write) starts at a point in time.
type fakeQuerier struct {
	from  time.Time
	calls int
}

func (f *fakeQuerier) HasData(_ context.Context, t time.Time) (bool, error) {
	f.calls++
	return !t.Before(f.from), nil
}

// UsableWindow finds the earliest timestamp with data (fidelity failure mode
// (c)): the backtest must report the window it can actually see instead of
// silently returning empty results for older ranges.
func TestUsableWindowFindsEarliestData(t *testing.T) {
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	dataFrom := now.Add(-5 * 24 * time.Hour)
	q := &fakeQuerier{from: dataFrom}

	got, err := backtest.UsableWindow(context.Background(), q, now.Add(-365*24*time.Hour), now, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	// binary search converges to within one resolution step
	if got.Before(dataFrom.Add(-time.Hour)) || got.After(dataFrom.Add(time.Hour)) {
		t.Errorf("usable from = %v, want %v ±1h", got, dataFrom)
	}
	// log-time, not linear: a year at 1h resolution must not mean 8760 queries
	if q.calls > 20 {
		t.Errorf("querier called %d times, want ≤20 (binary search)", q.calls)
	}
}

func TestUsableWindowNoData(t *testing.T) {
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	q := &fakeQuerier{from: now.Add(time.Hour)} // data only in the future
	_, err := backtest.UsableWindow(context.Background(), q, now.Add(-24*time.Hour), now, time.Hour)
	if err == nil {
		t.Error("want an error when no data exists in the searched range")
	}
}
