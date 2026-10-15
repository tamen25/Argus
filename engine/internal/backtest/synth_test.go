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

// A recording rule's static labels are on the series it records, so an alert
// built on it carries them. Inlined, the expression gets them back through
// label_replace.
func TestSynthesizeKeepsRecordingLabels(t *testing.T) {
	rs := backtest.RuleSet{Groups: []backtest.Group{{Name: "g", Rules: []backtest.Rule{{
		Name: "job:errors:rate5m", Expr: `sum by (job) (rate(errors_total[5m]))`,
		Labels: map[string]string{"team": "payments", "tier": "1"},
	}}}}}
	got, _, err := backtest.Synthesize(rs, `job:errors:rate5m > 1`)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"team", "payments", "", ""`, `"tier", "1", "", ""`} {
		if !strings.Contains(got, want) {
			t.Errorf("synthesized = %s, want label_replace setting %s", got, want)
		}
	}
}

// An offset or @ on a reference means "the recorded value at that time": it
// moves to every selector the definition reads, adding to an offset already
// there. A subquery is shifted as a whole, not its inner selectors.
func TestSynthesizeMovesOffsetAndAtIntoTheDefinition(t *testing.T) {
	rs := recordingRules(
		[2]string{"job:errors:rate5m", `sum by (job) (rate(errors_total[5m]))`},
		[2]string{"job:late:rate5m", `rate(late_total[5m] offset 30m)`},
		[2]string{"job:peak:max", `max_over_time(rate(req_total[1m])[10m:1m])`},
	)
	for _, tc := range []struct{ expr, want, notWant string }{
		{`job:errors:rate5m offset 1h > 1`, `errors_total[5m] offset 1h`, ""},
		{`job:late:rate5m offset 1h`, `late_total[5m] offset 1h30m`, ""},
		{`job:errors:rate5m @ 1700000000`, `errors_total[5m] @ 1700000000.000`, ""},
		{`job:peak:max offset 1h`, `[10m:1m] offset 1h`, `req_total[1m] offset`},
	} {
		got, _, err := backtest.Synthesize(rs, tc.expr)
		if err != nil {
			t.Errorf("%s: %v", tc.expr, err)
			continue
		}
		if !strings.Contains(got, tc.want) || (tc.notWant != "" && strings.Contains(got, tc.notWant)) {
			t.Errorf("%s synthesized to %s, want %q", tc.expr, got, tc.want)
		}
	}
}

// Where moving the time would change the meaning, synthesis refuses.
func TestSynthesizeRefusesTimeItCannotMove(t *testing.T) {
	rs := recordingRules(
		[2]string{"job:pinned", `errors_total @ 1600000000`},
		[2]string{"job:age", `time() - last_seen_seconds`},
	)
	for _, expr := range []string{`job:pinned @ 1700000000`, `job:age offset 1h`} {
		if _, _, err := backtest.Synthesize(rs, expr); err == nil {
			t.Errorf("%s: synthesized, want a refusal", expr)
		}
	}
	// Without an offset or @, time() in a definition is fine.
	if _, _, err := backtest.Synthesize(rs, `job:age > 300`); err != nil {
		t.Errorf("job:age > 300: %v", err)
	}
}
