package cost_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/tamen25/Argus/engine/internal/cost"
)

type fakeStore struct {
	prev     *cost.Report
	saved    bool
	savedRep cost.Report
}

func (f *fakeStore) SaveCostSnapshot(_ context.Context, r cost.Report, _ time.Time) (int64, error) {
	f.saved = true
	f.savedRep = r
	return 1, nil
}

func (f *fakeStore) LastCostSnapshot(_ context.Context, _ time.Time) (*cost.Report, time.Time, error) {
	return f.prev, time.Time{}, nil
}

// Assemble ties the pipeline together: gather → price → lifecycle → (trend +
// persist when a store is wired). It's what the `argus cost` CLI runs.
func TestAssembleWithStoreComputesTrendAndPersists(t *testing.T) {
	p := &cost.Pricing{
		Schema:       cost.PricingSchema,
		Currency:     "USD",
		Storage:      cost.Storage{PerGBMonthByClass: map[string]float64{"STANDARD": 0.023, "GLACIER_IR": 0.004}},
		ActiveSeries: cost.ActiveSeries{PerMillion: 8.00},
	}
	srcs := cost.Sources{
		Series:  fakeSeries{m: map[string]int64{"checkout": 1_000_000}},
		Storage: fakeStorage{m: map[string]int64{"STANDARD": 1_000_000_000_000}},
	}
	// prior snapshot: checkout/metrics cost $4/mo last week, no storage yet
	store := &fakeStore{prev: &cost.Report{
		Currency:     "USD",
		Lines:        []cost.Line{{Service: "checkout", Signal: "metrics", TotalMonthly: 4.00}},
		TotalMonthly: 4.00,
	}}

	sb, err := cost.Assemble(context.Background(), p, srcs, time.Hour, store, time.Now())
	if err != nil {
		t.Fatal(err)
	}

	if sb.Trend == nil {
		t.Fatal("no trend computed despite prior snapshot")
	}
	// checkout/metrics: 1e6 series × $8/M = $8 now vs $4 prior → +4 on the line
	var checkout *cost.TrendLine
	for i := range sb.Trend.Lines {
		if sb.Trend.Lines[i].Service == "checkout" {
			checkout = &sb.Trend.Lines[i]
		}
	}
	if checkout == nil || !approx(checkout.Delta, 4.00) {
		t.Errorf("checkout trend line = %+v, want delta 4.00", checkout)
	}
	// whole-report total: $4 → $31 ($8 series + $23 new storage) = +27
	if !approx(sb.Trend.TotalDelta, 27.00) {
		t.Errorf("trend total delta = %v, want 27.00", sb.Trend.TotalDelta)
	}
	// storage 1000 GB STANDARD → GLACIER_IR is a real saving, must be recommended
	if len(sb.Lifecycle) == 0 {
		t.Error("no lifecycle recommendation for STANDARD→cheaper")
	}
	if !store.saved {
		t.Error("current report not persisted")
	}
	// both honesty caveats always ride along: modeled-not-billed and
	// illustrative-template-rates (rule 7 — never present a modeled number
	// as a billed one)
	var modeled, illustrative bool
	for _, n := range sb.Notes {
		if strings.Contains(n, "not billed") {
			modeled = true
		}
		if strings.Contains(n, "illustrative") {
			illustrative = true
		}
	}
	if !modeled {
		t.Error("no modeled-not-billed note")
	}
	if !illustrative {
		t.Error("no illustrative-template-rates calibration note")
	}
}

// Without a store there's no trend and nothing persisted — the report still
// renders.
func TestAssembleWithoutStore(t *testing.T) {
	p := &cost.Pricing{Schema: cost.PricingSchema, Currency: "USD", ActiveSeries: cost.ActiveSeries{PerMillion: 8.00}}
	srcs := cost.Sources{Series: fakeSeries{m: map[string]int64{"a": 1_000_000}}}

	sb, err := cost.Assemble(context.Background(), p, srcs, time.Hour, nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if sb.Trend != nil {
		t.Error("trend without a store")
	}
	if !approx(sb.Report.TotalMonthly, 8.00) {
		t.Errorf("total = %v, want 8.00", sb.Report.TotalMonthly)
	}
}

// timedStore holds snapshots with their times and answers LastCostSnapshot
// the way the Postgres store does: the latest one strictly before the time.
type timedStore struct {
	snaps []timedSnap
}

type timedSnap struct {
	rep cost.Report
	at  time.Time
}

func (s *timedStore) SaveCostSnapshot(_ context.Context, r cost.Report, at time.Time) (int64, error) {
	s.snaps = append(s.snaps, timedSnap{r, at})
	return int64(len(s.snaps)), nil
}

func (s *timedStore) LastCostSnapshot(_ context.Context, before time.Time) (*cost.Report, time.Time, error) {
	var best *timedSnap
	for i := range s.snaps {
		if sn := &s.snaps[i]; sn.at.Before(before) && (best == nil || sn.at.After(best.at)) {
			best = sn
		}
	}
	if best == nil {
		return nil, time.Time{}, nil
	}
	return &best.rep, best.at, nil
}

// R20: the trend compares against the latest snapshot at least a week old,
// not the latest of any age, and says which.
func TestAssembleTrendsAgainstAWeekOldSnapshot(t *testing.T) {
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	p := &cost.Pricing{Schema: cost.PricingSchema, Currency: "USD", ActiveSeries: cost.ActiveSeries{PerMillion: 8}}
	store := &timedStore{snaps: []timedSnap{
		{cost.Report{Currency: "USD", TotalMonthly: 100}, now.Add(-8 * 24 * time.Hour)},
		{cost.Report{Currency: "USD", TotalMonthly: 999}, now.Add(-time.Minute)}, // too recent to be the baseline
	}}
	sb, err := cost.Assemble(context.Background(), p, cost.Sources{}, time.Hour, store, now)
	if err != nil {
		t.Fatal(err)
	}
	if sb.Trend == nil || !sb.Trend.BaselineAt.Equal(now.Add(-8*24*time.Hour)) {
		t.Fatalf("trend = %+v, want the 8-day-old baseline", sb.Trend)
	}
	if sb.Trend.TotalDelta != sb.Report.TotalMonthly-100 {
		t.Errorf("delta = %v, want against the week-old total of 100", sb.Trend.TotalDelta)
	}
}

// With no snapshot a week old, or one in another currency, there is no
// trend, and a note says why.
func TestAssembleNoTrendWithoutAComparableBaseline(t *testing.T) {
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	p := &cost.Pricing{Schema: cost.PricingSchema, Currency: "USD", ActiveSeries: cost.ActiveSeries{PerMillion: 8}}
	for name, snaps := range map[string][]timedSnap{
		"only recent snapshots": {{cost.Report{Currency: "USD"}, now.Add(-time.Hour)}},
		"another currency":      {{cost.Report{Currency: "EUR"}, now.Add(-10 * 24 * time.Hour)}},
	} {
		sb, err := cost.Assemble(context.Background(), p, cost.Sources{}, time.Hour, &timedStore{snaps: snaps}, now)
		if err != nil {
			t.Fatal(err)
		}
		if sb.Trend != nil {
			t.Errorf("%s: trend = %+v, want none", name, sb.Trend)
		}
		if len(sb.Notes) < 3 {
			t.Errorf("%s: notes = %q, want one saying why there is no trend", name, sb.Notes)
		}
	}
}
