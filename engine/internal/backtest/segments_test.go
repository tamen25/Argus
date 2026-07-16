package backtest_test

import (
	"context"
	"testing"
	"time"

	"github.com/tamen25/Argus/engine/internal/backtest"
)

// windowedQuerier has data only inside the given windows — the shape of a dev
// cluster that only runs while the machine is on.
type windowedQuerier struct {
	windows [][2]time.Time
}

func (w *windowedQuerier) HasData(_ context.Context, t time.Time) (bool, error) {
	for _, win := range w.windows {
		if !t.Before(win[0]) && !t.After(win[1]) {
			return true, nil
		}
	}
	return false, nil
}

// Segments maps presence over a range: real telemetry history is not
// contiguous (sessions, retention, outages), and the backtest must evaluate
// inside segments and disclose coverage instead of pretending the timeline is
// solid. Measured on the dev cluster: 36h of data across 6 calendar days.
func TestSegmentsFindsGappyHistory(t *testing.T) {
	day := func(d, h int) time.Time { return time.Date(2026, 7, d, h, 0, 0, 0, time.UTC) }
	q := &windowedQuerier{windows: [][2]time.Time{
		{day(12, 0), day(12, 3)},
		{day(14, 6), day(14, 21)},
		{day(16, 5), day(16, 8)},
	}}

	segs, err := backtest.Segments(context.Background(), q, day(11, 0), day(17, 0), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(segs) != 3 {
		t.Fatalf("segments = %d (%+v), want 3", len(segs), segs)
	}
	// boundaries are accurate to within one stride
	if segs[1].Start.Sub(day(14, 6)) > time.Hour || day(14, 21).Sub(segs[1].End) > time.Hour {
		t.Errorf("segment 1 = %+v, want ≈ 07-14 06:00–21:00", segs[1])
	}
	// coverage sums the segments, not the calendar range
	var cov time.Duration
	for _, s := range segs {
		cov += s.End.Sub(s.Start)
	}
	if cov > 24*time.Hour {
		t.Errorf("coverage = %v, want ≈ 21h (not the 6-day calendar span)", cov)
	}
}

func TestSegmentsEmptyRange(t *testing.T) {
	q := &windowedQuerier{}
	segs, err := backtest.Segments(context.Background(), q,
		time.Date(2026, 7, 11, 0, 0, 0, 0, time.UTC), time.Date(2026, 7, 12, 0, 0, 0, 0, time.UTC), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(segs) != 0 {
		t.Errorf("segments = %+v, want none", segs)
	}
}
