package rules

import (
	"math"
	"testing"
)

// The worked example from the vendored spec (rules/spec/upstream/
// specification.md): 4/8 Critical, 8/10 Important, 6/8 Normal, 1/5 Low
// => 530/830*100 ≈ 63.86.
func TestScoreSpecWorkedExample(t *testing.T) {
	res := []RuleResult{}
	res = append(res, nRuleResults(ImpactCritical, 4, 8)...)
	res = append(res, nRuleResults(ImpactImportant, 8, 10)...)
	res = append(res, nRuleResults(ImpactNormal, 6, 8)...)
	res = append(res, nRuleResults(ImpactLow, 1, 5)...)

	got := Score(res)
	if math.Abs(got-63.8554216867) > 0.0001 {
		t.Errorf("score = %v, want ≈63.8554", got)
	}
}

func TestScoreAllPassAndAllFail(t *testing.T) {
	pass := nRuleResults(ImpactCritical, 1, 1)
	if s := Score(pass); s != 100 {
		t.Errorf("all-pass score = %v, want 100", s)
	}
	fail := nRuleResults(ImpactCritical, 0, 1)
	if s := Score(fail); s != 0 {
		t.Errorf("all-fail score = %v, want 0", s)
	}
}

func TestScoreNoRules(t *testing.T) {
	// No evaluated rules => no evidence either way; define as 100 with the
	// "incomplete rule set" disclosure handled at report level.
	if s := Score(nil); s != 100 {
		t.Errorf("empty score = %v, want 100", s)
	}
}

func TestImpactWeights(t *testing.T) {
	for impact, want := range map[Impact]int{
		ImpactCritical: 40, ImpactImportant: 30, ImpactNormal: 20, ImpactLow: 10,
	} {
		if got := impact.Weight(); got != want {
			t.Errorf("weight(%s) = %d, want %d", impact, got, want)
		}
	}
}

func TestCategory(t *testing.T) {
	cases := map[float64]string{
		100: "Excellent", 90: "Excellent",
		89.9: "Good", 75: "Good",
		74.9: "Needs Improvement", 50: "Needs Improvement",
		49.9: "Poor", 0: "Poor",
	}
	for score, want := range cases {
		if got := Category(score); got != want {
			t.Errorf("category(%v) = %q, want %q", score, got, want)
		}
	}
}

func nRuleResults(impact Impact, passed, total int) []RuleResult {
	out := make([]RuleResult, 0, total)
	for i := 0; i < total; i++ {
		out = append(out, RuleResult{Impact: impact, Passed: i < passed})
	}
	return out
}
