package rules

// RuleResult is one rule's per-service outcome feeding the score.
type RuleResult struct {
	RuleID string `json:"rule_id"`
	Impact Impact `json:"impact"`
	Passed bool   `json:"passed"`
}

// Score implements the Instrumentation Score formula from the vendored spec:
//
//	Score = Σ(P_i × W_i) / Σ(T_i × W_i) × 100
//
// over impact levels i, where P is rules passed and T total rules evaluated.
// With no evaluated rules the score is 100; the report layer is responsible
// for the spec-mandated disclosure that the rule set is incomplete.
func Score(results []RuleResult) float64 {
	num, den := 0, 0
	for _, r := range results {
		w := r.Impact.Weight()
		den += w
		if r.Passed {
			num += w
		}
	}
	if den == 0 {
		return 100
	}
	return float64(num) / float64(den) * 100
}

// Category maps a score to the spec's qualitative categories.
func Category(score float64) string {
	switch {
	case score >= 90:
		return "Excellent"
	case score >= 75:
		return "Good"
	case score >= 50:
		return "Needs Improvement"
	default:
		return "Poor"
	}
}
