package cost_test

import (
	"testing"

	"github.com/tamen25/Argus/engine/internal/cost"
	"github.com/tamen25/Argus/engine/internal/rules"
	"github.com/tamen25/Argus/engine/internal/rules/builtin"
)

// End-to-end guard: the real built-in MET-001 must price a finding carrying
// the cardinality it reports as evidence. Locks the cost.quantity_field ↔
// evidence-field-name coupling against silent drift.
func TestPriceFindingsWiresBuiltinMET001(t *testing.T) {
	rls, err := builtin.Load()
	if err != nil {
		t.Fatal(err)
	}
	p := &cost.Pricing{Schema: cost.PricingSchema, Currency: "USD", ActiveSeries: cost.ActiveSeries{PerMillion: 8.00}}
	snap := &rules.Snapshot{Services: []rules.ServiceReport{{
		ServiceName: "frontend",
		Findings: []rules.Finding{{
			RuleID:   "MET-001",
			Service:  "frontend",
			Evidence: []rules.Evidence{{Attrs: map[string]any{"metric": "http.server.duration", "attribute": "user_id", "cardinality": 50_000.0}}},
		}},
	}}}

	cost.PriceFindings(snap, rls, p)

	got := snap.Services[0].Findings[0].EstimatedMonthlyCost
	if got == nil {
		t.Fatal("built-in MET-001 finding not priced — cost block or quantity_field drifted")
	}
	if !approx(*got, 0.40) { // 50_000/1e6 × 8.00
		t.Errorf("cost = %v, want 0.40", *got)
	}
}

// A rule with a cost block prices its findings: the signature move is
// MET-001, where a high-cardinality label's series count × the active-series
// rate is the monthly bill for that label. "Score 61, here's the invoice."
func TestPriceFindingsActiveSeries(t *testing.T) {
	p := &cost.Pricing{
		Schema:       cost.PricingSchema,
		Currency:     "USD",
		ActiveSeries: cost.ActiveSeries{PerMillion: 8.00},
	}
	rls := []*rules.Rule{{
		ID:   "MET-001",
		Cost: rules.CostSpec{Driver: "active_series", QuantityField: "series"},
	}}
	series := 500_000.0
	snap := &rules.Snapshot{Services: []rules.ServiceReport{{
		ServiceName: "checkout",
		Findings: []rules.Finding{{
			RuleID:  "MET-001",
			Service: "checkout",
			// poller-verified path records the true series count in Details
			Details: map[string]any{"series": series},
		}},
	}}}

	cost.PriceFindings(snap, rls, p)

	got := snap.Services[0].Findings[0].EstimatedMonthlyCost
	if got == nil {
		t.Fatal("finding not priced")
	}
	// 500_000 / 1e6 × 8.00 = 4.00 / month
	if !approx(*got, 4.00) {
		t.Errorf("estimated_monthly_cost = %v, want 4.00", *got)
	}
}

// A finding whose rule has no cost block, or whose quantity field is absent,
// is left unpriced (nil) — never a fabricated zero.
func TestPriceFindingsLeavesUnpriceableNil(t *testing.T) {
	p := &cost.Pricing{Schema: cost.PricingSchema, Currency: "USD", ActiveSeries: cost.ActiveSeries{PerMillion: 8.00}}
	rls := []*rules.Rule{
		{ID: "NO-COST"},
		{ID: "MET-001", Cost: rules.CostSpec{Driver: "active_series", QuantityField: "series"}},
	}
	snap := &rules.Snapshot{Services: []rules.ServiceReport{{
		ServiceName: "cart",
		Findings: []rules.Finding{
			{RuleID: "NO-COST", Service: "cart"},                            // no cost block
			{RuleID: "MET-001", Service: "cart", Details: map[string]any{}}, // field absent
		},
	}}}

	cost.PriceFindings(snap, rls, p)

	for _, f := range snap.Services[0].Findings {
		if f.EstimatedMonthlyCost != nil {
			t.Errorf("%s priced to %v, want nil (unpriceable)", f.RuleID, *f.EstimatedMonthlyCost)
		}
	}
}

// The quantity may live in truncated evidence attrs when no poller ran; the
// worst (max) observed sample is priced, so cost is never understated.
func TestPriceFindingsReadsEvidenceMax(t *testing.T) {
	p := &cost.Pricing{Schema: cost.PricingSchema, Currency: "USD", ActiveSeries: cost.ActiveSeries{PerMillion: 8.00}}
	rls := []*rules.Rule{{ID: "MET-001", Cost: rules.CostSpec{Driver: "active_series", QuantityField: "series"}}}
	snap := &rules.Snapshot{Services: []rules.ServiceReport{{
		ServiceName: "ad",
		Findings: []rules.Finding{{
			RuleID:  "MET-001",
			Service: "ad",
			Evidence: []rules.Evidence{
				{Attrs: map[string]any{"series": 100_000.0}},
				{Attrs: map[string]any{"series": 250_000.0}}, // worst sample
			},
		}},
	}}}

	cost.PriceFindings(snap, rls, p)

	got := snap.Services[0].Findings[0].EstimatedMonthlyCost
	if got == nil || !approx(*got, 2.00) { // 250_000/1e6 × 8.00
		t.Errorf("estimated_monthly_cost = %v, want 2.00 (max evidence sample)", got)
	}
}
