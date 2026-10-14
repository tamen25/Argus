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
			f.CostCurrency = p.Currency
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

// findingQuantity extracts the cost-bearing quantity: the largest value a
// poller verified (it saw the unsampled stream), or else the largest sampled
// evidence value, so cost is never understated.
func findingQuantity(f *rules.Finding, field string) (float64, bool) {
	if v, ok := largest(verifiedPairs(f), field); ok {
		return v, true
	}
	samples := make([]map[string]any, 0, len(f.Evidence))
	for _, ev := range f.Evidence {
		samples = append(samples, ev.Attrs)
	}
	return largest(samples, field)
}

// verifiedPairs returns what a cardinality poller measured for the finding:
// Details["poller"]["pairs"], one map per (metric, attribute). A report read
// back from JSON holds the same shape as []any, so both are accepted.
func verifiedPairs(f *rules.Finding) []map[string]any {
	poller, _ := f.Details["poller"].(map[string]any)
	switch pairs := poller["pairs"].(type) {
	case []map[string]any:
		return pairs
	case []any:
		out := make([]map[string]any, 0, len(pairs))
		for _, p := range pairs {
			if m, ok := p.(map[string]any); ok {
				out = append(out, m)
			}
		}
		return out
	}
	return nil
}

// largest is the largest numeric value of field among the rows.
func largest(rows []map[string]any, field string) (float64, bool) {
	best, found := 0.0, false
	for _, row := range rows {
		if v, ok := rules.AsFloat(row[field]); ok && (!found || v > best) {
			best, found = v, true
		}
	}
	return best, found
}
