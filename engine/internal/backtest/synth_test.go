package backtest_test

import (
	"strings"
	"testing"

	"github.com/tamen25/Argus/engine/internal/backtest"
)

// Synthesize rewrites an alert expression, replacing references to recording
// rules defined in the loaded set with their defining expressions inline —
// failure mode (a): replaying history where the recording rule never ran.
func TestSynthesizeInlinesDefinedRecording(t *testing.T) {
	rs, err := backtest.LoadRuleFiles("testdata/rules-valid.yaml")
	if err != nil {
		t.Fatal(err)
	}

	got, caveats, err := backtest.Synthesize(rs, `service:http_errors:rate5m > 0.05`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, `sum by (service_name) (rate(http_server_errors_total[5m]))`) {
		t.Errorf("synthesized = %q, want the recording rule's expression inlined", got)
	}
	if strings.Contains(got, "service:http_errors:rate5m") {
		t.Errorf("synthesized = %q, still references the recorded series", got)
	}
	if len(caveats) == 0 {
		t.Error("synthesis must carry a caveat — it is never silent")
	}
}

// A recording-rule reference carrying extra label matchers cannot have them
// pushed into the synthesized expression soundly — refuse rather than guess.
func TestSynthesizeRefusesMatcherPushdown(t *testing.T) {
	rs, err := backtest.LoadRuleFiles("testdata/rules-valid.yaml")
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = backtest.Synthesize(rs, `service:http_errors:rate5m{service_name="checkout"} > 0.05`)
	if err == nil {
		t.Error("want an error for matcher pushdown, got silent synthesis")
	}
}

// External recording rules (not defined in the set) cannot be synthesized.
func TestSynthesizeRefusesExternal(t *testing.T) {
	rs, err := backtest.LoadRuleFiles("testdata/rules-valid.yaml")
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = backtest.Synthesize(rs, `slo:sli_error:ratio_rate5m > 0.01`)
	if err == nil {
		t.Error("want an error for an external recording rule")
	}
}

// Plain expressions pass through unchanged, no caveats.
func TestSynthesizePassthrough(t *testing.T) {
	rs, err := backtest.LoadRuleFiles("testdata/rules-valid.yaml")
	if err != nil {
		t.Fatal(err)
	}
	got, caveats, err := backtest.Synthesize(rs, `up == 0`)
	if err != nil {
		t.Fatal(err)
	}
	if got != `up == 0` || len(caveats) != 0 {
		t.Errorf("got %q caveats %v, want passthrough with none", got, caveats)
	}
}

func recordingRules(defs ...[2]string) backtest.RuleSet {
	g := backtest.Group{Name: "g"}
	for _, d := range defs {
		g.Rules = append(g.Rules, backtest.Rule{Name: d[0], Expr: d[1]})
	}
	return backtest.RuleSet{Groups: []backtest.Group{g}}
}

// A bare reference at the root is inlined like any other, including the
// recording rules its own definition refers to. The root used to take a
// separate path that stopped one level down.
func TestSynthesizeRootReferenceIsFullyInlined(t *testing.T) {
	rs := recordingRules(
		[2]string{"job:errors:rate5m", `sum by (job) (rate(errors_total[5m]))`},
		[2]string{"job:errors:high", `job:errors:rate5m > 1`},
	)
	got, caveats, err := backtest.Synthesize(rs, `job:errors:high`)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "job:errors") || !strings.Contains(got, "rate(errors_total[5m])") {
		t.Errorf("synthesized = %q, want both rules inlined down to raw series", got)
	}
	if len(caveats) != 2 {
		t.Errorf("caveats = %v, want one per inlined rule", caveats)
	}
}

// An undefined recording rule is refused at the root too, as it is nested.
func TestSynthesizeRefusesExternalAtRoot(t *testing.T) {
	if _, _, err := backtest.Synthesize(recordingRules(), `slo:sli_error:ratio_rate5m`); err == nil {
		t.Error("want an error for a bare external recording rule")
	}
}

// Rules defined in terms of each other cannot be inlined; refusing beats
// recursing until the stack runs out.
func TestSynthesizeRefusesCycle(t *testing.T) {
	rs := recordingRules(
		[2]string{"a:x", `b:x + 1`},
		[2]string{"b:x", `a:x + 1`},
	)
	_, _, err := backtest.Synthesize(rs, `a:x > 0`)
	if err == nil || !strings.Contains(err.Error(), "in terms of itself") {
		t.Errorf("err = %v, want the cycle named", err)
	}
}

// A reference is inlined wherever it sits: under a function call, an
// aggregation, a unary minus, a subquery, or either side of a binary operator.
func TestSynthesizeInlinesInEveryPosition(t *testing.T) {
	rs := recordingRules([2]string{"job:x:rate5m", `rate(x_total[5m])`})
	for _, expr := range []string{
		`abs(job:x:rate5m)`,
		`sum by (job) (job:x:rate5m)`,
		`topk(3, job:x:rate5m)`,
		`-job:x:rate5m`,
		`max_over_time(job:x:rate5m[10m:1m])`,
		`1 - job:x:rate5m`,
		`(job:x:rate5m)`,
	} {
		got, _, err := backtest.Synthesize(rs, expr)
		if err != nil {
			t.Errorf("%s: %v", expr, err)
			continue
		}
		if strings.Contains(got, "job:x") || !strings.Contains(got, "rate(x_total[5m])") {
			t.Errorf("%s synthesized to %q, want the rule inlined", expr, got)
		}
	}
}

// Inside a range selector the stored series is needed: an expression cannot
// stand in for it, so synthesis refuses.
func TestSynthesizeRefusesRangeSelector(t *testing.T) {
	rs := recordingRules([2]string{"job:x:rate5m", `rate(x_total[5m])`})
	_, _, err := backtest.Synthesize(rs, `avg_over_time(job:x:rate5m[1h])`)
	if err == nil || !strings.Contains(err.Error(), "range selector") {
		t.Errorf("err = %v, want the range selector named", err)
	}
}
