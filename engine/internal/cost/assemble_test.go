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
