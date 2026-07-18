package backtest_test

import (
	"strings"
	"testing"
	"time"

	"github.com/tamen25/Argus/engine/internal/backtest"
)

// LoadSLOPolicies strictly parses the SLO policy file; BurnRateRules turns
// each policy into replayable alert rules — the burn-rate simulation IS the
// replay pipeline over generated expressions (no separate evaluator to
// drift from the real one).
func TestLoadSLOPoliciesValid(t *testing.T) {
	ps, err := backtest.LoadSLOPolicies("testdata/slo-valid.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 1 {
		t.Fatalf("policies = %d, want 1", len(ps))
	}
	p := ps[0]
	if p.Name != "checkout-availability" || p.Objective != 0.999 {
		t.Errorf("policy = %+v", p)
	}
	if len(p.Windows) != 2 || p.Windows[0].Factor != 14.4 {
		t.Errorf("windows = %+v", p.Windows)
	}
}

func TestLoadSLOPoliciesRejectsBadObjective(t *testing.T) {
	if _, err := backtest.LoadSLOPolicies("testdata/slo-bad-objective.yaml"); err == nil {
		t.Error("objective >= 1 loaded without error")
	}
}

// The standard multi-window pairs from the SRE workbook ship as defaults.
func TestDefaultBurnRateWindows(t *testing.T) {
	ws := backtest.DefaultBurnRateWindows()
	if len(ws) != 2 {
		t.Fatalf("default windows = %+v, want fast+slow pair", ws)
	}
	fast, slow := ws[0], ws[1]
	if fast.Short != 5*time.Minute || fast.Long != time.Hour || fast.Factor != 14.4 || fast.Severity != "page" {
		t.Errorf("fast = %+v", fast)
	}
	if slow.Short != 30*time.Minute || slow.Long != 6*time.Hour || slow.Factor != 6.0 || slow.Severity != "ticket" {
		t.Errorf("slow = %+v", slow)
	}
}

// Expression generation: both windows must exceed factor × error budget,
// grouped consistently so the AND vector-matches.
func TestBurnRateRulesExpression(t *testing.T) {
	p := backtest.SLOPolicy{
		Name:      "checkout-availability",
		Objective: 0.999,
		SLI: backtest.SLI{
			Errors:  `traces_spanmetrics_calls_total{status_code="STATUS_CODE_ERROR"}`,
			Total:   `traces_spanmetrics_calls_total`,
			GroupBy: []string{"service"},
		},
		Windows: []backtest.BurnRateWindow{
			{Short: 5 * time.Minute, Long: time.Hour, Factor: 14.4, For: 2 * time.Minute, Severity: "page"},
		},
	}

	rules := backtest.BurnRateRules(p)
	if len(rules) != 1 {
		t.Fatalf("rules = %+v, want 1", rules)
	}
	r := rules[0]
	if r.Name != "checkout-availability-burnrate-page-5m0s" {
		t.Errorf("name = %q", r.Name)
	}
	if !r.Alert || r.For != 2*time.Minute {
		t.Errorf("alert = %v for = %v", r.Alert, r.For)
	}
	for _, frag := range []string{
		`rate(traces_spanmetrics_calls_total{status_code="STATUS_CODE_ERROR"}[5m])`,
		`rate(traces_spanmetrics_calls_total[5m])`,
		`[1h]`,
		`sum by (service)`,
		`> (14.400 * 0.001000)`,
		` and `,
	} {
		if !strings.Contains(r.Expr, frag) {
			t.Errorf("expr missing %q:\n%s", frag, r.Expr)
		}
	}
	// the generated expression must parse as valid PromQL — proven by the
	// dependency walker not skipping it
	deps := backtest.Dependencies(backtest.RuleSet{Groups: []backtest.Group{{Name: "g", Rules: rules}}})
	if _, ok := deps[r.Name]; !ok {
		t.Errorf("generated expression failed to parse: %s", r.Expr)
	}
}
