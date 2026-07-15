package cost_test

import (
	"testing"

	"github.com/tamen25/Argus/engine/internal/cost"
)

// Trend is week-over-week cost movement: per-line and total deltas between the
// current report and a prior one. Deterministic; new lines appear as
// full-value increases, vanished lines as full-value decreases.
func TestTrendDeltas(t *testing.T) {
	prev := cost.Report{
		Currency: "USD",
		Lines: []cost.Line{
			{Service: "checkout", Signal: "metrics", TotalMonthly: 100},
			{Service: "cart", Signal: "logs", TotalMonthly: 50},
		},
		TotalMonthly: 150,
	}
	cur := cost.Report{
		Currency: "USD",
		Lines: []cost.Line{
			{Service: "checkout", Signal: "metrics", TotalMonthly: 130}, // up 30
			{Service: "ad", Signal: "metrics", TotalMonthly: 20},        // new
			// cart/logs vanished (down 50)
		},
		TotalMonthly: 150,
	}

	tr := cost.Trend(cur, prev)

	if tr.Currency != "USD" {
		t.Errorf("currency = %q", tr.Currency)
	}
	// total unchanged in dollars (150→150) but composition shifted
	if !approx(tr.TotalDelta, 0) {
		t.Errorf("total delta = %v, want 0", tr.TotalDelta)
	}

	byKey := map[string]cost.TrendLine{}
	for _, l := range tr.Lines {
		byKey[l.Service+"/"+l.Signal] = l
	}
	if d := byKey["checkout/metrics"]; !approx(d.Delta, 30) || !approx(d.PercentDelta, 30) {
		t.Errorf("checkout = %+v, want delta 30 pct 30", d)
	}
	if d := byKey["ad/metrics"]; !approx(d.Delta, 20) || d.Previous != 0 {
		t.Errorf("ad (new) = %+v, want delta 20 previous 0", d)
	}
	if d, ok := byKey["cart/logs"]; !ok || !approx(d.Delta, -50) || d.Current != 0 {
		t.Errorf("cart (vanished) = %+v, want delta -50 current 0", d)
	}
}

// With no prior snapshot the trend is all-new: every line is a full increase
// and there's no divide-by-zero on percent.
func TestTrendAgainstEmptyPrevious(t *testing.T) {
	cur := cost.Report{Currency: "USD", Lines: []cost.Line{{Service: "a", Signal: "logs", TotalMonthly: 10}}, TotalMonthly: 10}
	tr := cost.Trend(cur, cost.Report{})
	if len(tr.Lines) != 1 || !approx(tr.Lines[0].Delta, 10) || tr.Lines[0].PercentDelta != 0 {
		t.Errorf("trend = %+v, want single +10 line, pct 0 (no prior baseline)", tr.Lines)
	}
}
