package cost_test

import (
	"testing"

	"github.com/tamen25/Argus/engine/internal/cost"
)

func lifecyclePricing() *cost.Pricing {
	return &cost.Pricing{
		Schema:   cost.PricingSchema,
		Currency: "USD",
		Storage: cost.Storage{PerGBMonthByClass: map[string]float64{
			"STANDARD":    0.023,
			"STANDARD_IA": 0.0125,
			"GLACIER_IR":  0.004,
		}},
	}
}

// Lifecycle modeling answers "moving cold blocks to a cheaper class saves
// $X/mo". Deterministic: inventory + transition rules + pricing → sorted
// recommendations, biggest saving first.
func TestLifecycleSavings(t *testing.T) {
	storage := []cost.StorageObject{{Class: "STANDARD", Bytes: 1_000_000_000_000}} // 1000 GB
	transitions := []cost.LifecycleRule{
		{FromClass: "STANDARD", ToClass: "GLACIER_IR"},
		{FromClass: "STANDARD", ToClass: "STANDARD_IA"},
	}

	recs := cost.LifecycleSavings(storage, transitions, lifecyclePricing())

	if len(recs) != 2 {
		t.Fatalf("recs = %d, want 2: %+v", len(recs), recs)
	}
	// biggest saving first: STANDARD→GLACIER_IR (0.023→0.004)
	r := recs[0]
	if r.FromClass != "STANDARD" || r.ToClass != "GLACIER_IR" {
		t.Errorf("top rec = %+v, want STANDARD→GLACIER_IR", r)
	}
	if !approx(r.GB, 1000) || !approx(r.CurrentMonthly, 23.0) || !approx(r.ProjectedMonthly, 4.0) || !approx(r.SavingsMonthly, 19.0) {
		t.Errorf("rec = %+v, want GB 1000 current 23 projected 4 saving 19", r)
	}
	// second: STANDARD→STANDARD_IA saves less (0.023→0.0125 = 10.5)
	if !approx(recs[1].SavingsMonthly, 10.5) {
		t.Errorf("second saving = %v, want 10.5", recs[1].SavingsMonthly)
	}
}

// A transition whose source class holds nothing, or whose target isn't
// cheaper, produces no recommendation — no phantom or negative "savings".
func TestLifecycleSkipsEmptyAndNonSaving(t *testing.T) {
	storage := []cost.StorageObject{{Class: "GLACIER_IR", Bytes: 500_000_000_000}}
	transitions := []cost.LifecycleRule{
		{FromClass: "STANDARD", ToClass: "GLACIER_IR"},   // no STANDARD bytes
		{FromClass: "GLACIER_IR", ToClass: "STANDARD"},   // target more expensive
		{FromClass: "GLACIER_IR", ToClass: "GLACIER_IR"}, // no-op
	}
	recs := cost.LifecycleSavings(storage, transitions, lifecyclePricing())
	if len(recs) != 0 {
		t.Errorf("recs = %+v, want none", recs)
	}
}

// The default transition candidates cover the common cold-tiering moves; run
// through LifecycleSavings they surface whatever actually saves on this fleet.
func TestDefaultLifecycleRulesFindSavings(t *testing.T) {
	rules := cost.DefaultLifecycleRules()
	if len(rules) == 0 {
		t.Fatal("no default rules")
	}
	storage := []cost.StorageObject{{Class: "STANDARD", Bytes: 1_000_000_000_000}}
	recs := cost.LifecycleSavings(storage, rules, lifecyclePricing())
	// STANDARD has priced cheaper targets (STANDARD_IA, GLACIER_IR) in the
	// fixture pricing, so at least one recommendation must surface.
	if len(recs) == 0 {
		t.Error("default rules surfaced no savings on STANDARD-heavy fleet")
	}
	for i := 1; i < len(recs); i++ {
		if recs[i-1].SavingsMonthly < recs[i].SavingsMonthly {
			t.Errorf("recs not sorted by saving desc: %+v", recs)
		}
	}
}

// An unpriced target class can't be recommended (no rate = unknown, not free).
func TestLifecycleSkipsUnpricedTarget(t *testing.T) {
	storage := []cost.StorageObject{{Class: "STANDARD", Bytes: 1_000_000_000_000}}
	transitions := []cost.LifecycleRule{{FromClass: "STANDARD", ToClass: "DEEP_ARCHIVE"}}
	recs := cost.LifecycleSavings(storage, transitions, lifecyclePricing())
	if len(recs) != 0 {
		t.Errorf("recs = %+v, want none (DEEP_ARCHIVE unpriced)", recs)
	}
}
