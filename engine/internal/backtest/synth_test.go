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
