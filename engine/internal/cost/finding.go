package cost

import "github.com/tamen25/Argus/engine/internal/rules"

// PriceFindings sets EstimatedMonthlyCost on every finding whose rule declares
// a cost block and carries the cost-bearing quantity. It mutates the snapshot
// in place. Findings without a cost block, an unknown driver, or a missing
// quantity are left nil — an unpriceable finding is never a fabricated zero.
//
// This is the product's signature move: a quality finding priced in dollars.
func PriceFindings(snap *rules.Snapshot, rls []*rules.Rule, p *Pricing) {
	costByRule := map[string]rules.CostSpec{}
	for _, r := range rls {
		if r.Cost.Driver != "" {
			costByRule[r.ID] = r.Cost
		}
	}

	for si := range snap.Services {
		findings := snap.Services[si].Findings
		for fi := range findings {
			f := &findings[fi]
			spec, ok := costByRule[f.RuleID]
			if !ok {
				continue
			}
			qty, ok := findingQuantity(f, spec.QuantityField)
			if !ok {
				continue
			}
			monthly, ok := priceQuantity(p, spec.Driver, qty)
			if !ok {
				continue
			}
			f.EstimatedMonthlyCost = &monthly
		}
	}
}

// priceQuantity converts a driver-specific quantity to a monthly dollar cost.
func priceQuantity(p *Pricing, driver string, qty float64) (float64, bool) {
	switch driver {
	case "active_series":
		// series is a gauge: qty series priced directly against the monthly
		// per-million rate, no window scaling.
		return qty / 1e6 * p.ActiveSeries.PerMillion, true
	default:
		return 0, false
	}
}

// findingQuantity extracts the cost-bearing quantity: the poller-verified
// value in Details wins (it saw the unsampled stream); otherwise the worst
// (max) truncated evidence sample, so cost is never understated.
func findingQuantity(f *rules.Finding, field string) (float64, bool) {
	if v, ok := toFloat(f.Details[field]); ok {
		return v, true
	}
	best, found := 0.0, false
	for _, ev := range f.Evidence {
		if v, ok := toFloat(ev.Attrs[field]); ok && (!found || v > best) {
			best, found = v, true
		}
	}
	return best, found
}

func toFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case int64:
		return float64(x), true
	case int:
		return float64(x), true
	default:
		return 0, false
	}
}
