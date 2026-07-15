package cost_test

import (
	"testing"
	"time"

	"github.com/tamen25/Argus/engine/internal/cost"
)

func testPricing() *cost.Pricing {
	return &cost.Pricing{
		Schema:       cost.PricingSchema,
		Currency:     "USD",
		Ingest:       cost.Ingest{PerGB: 0.50, PerGBBySignal: map[string]float64{"logs": 0.40}},
		ActiveSeries: cost.ActiveSeries{PerMillion: 0.20},
		Storage:      cost.Storage{PerGBMonthByClass: map[string]float64{"STANDARD": 0.023}},
	}
}

func lineFor(r cost.Report, service, signal string) *cost.Line {
	for i := range r.Lines {
		if r.Lines[i].Service == service && r.Lines[i].Signal == signal {
			return &r.Lines[i]
		}
	}
	return nil
}

// Price attributes monthly cost per (service, signal). With a one-month window
// the extrapolation factor is 1, so the arithmetic is transparent: ingest GB ×
// rate, plus active series (a gauge, priced directly, no window scaling).
func TestPriceMonthlyWindow(t *testing.T) {
	u := cost.Usage{
		Window: 730 * time.Hour, // one month → factor 1.0
		Streams: []cost.Stream{
			{Service: "checkout", Signal: "metrics", IngestBytes: 1e9, ActiveSeries: 2e6},
			{Service: "checkout", Signal: "logs", IngestBytes: 2e9},
		},
		Storage: []cost.StorageObject{{Class: "STANDARD", Bytes: 100e9}},
	}

	r := cost.Price(testPricing(), u)

	if r.Currency != "USD" {
		t.Errorf("currency = %q", r.Currency)
	}
	m := lineFor(r, "checkout", "metrics")
	if m == nil {
		t.Fatalf("no metrics line: %+v", r.Lines)
	}
	// 1 GB × 0.50 = 0.50 ingest; 2e6/1e6 × 0.20 = 0.40 series; 0.90 total
	if !approx(m.IngestMonthly, 0.50) || !approx(m.ActiveSeriesMonthly, 0.40) || !approx(m.TotalMonthly, 0.90) {
		t.Errorf("metrics line = %+v, want ingest 0.50 series 0.40 total 0.90", *m)
	}
	l := lineFor(r, "checkout", "logs")
	if l == nil || !approx(l.IngestMonthly, 0.80) { // 2 GB × 0.40 override
		t.Errorf("logs line = %+v, want ingest 0.80", l)
	}
	// storage: 100 GB × 0.023 = 2.30/month
	if len(r.Storage) != 1 || !approx(r.Storage[0].Monthly, 2.30) {
		t.Errorf("storage = %+v, want STANDARD 2.30", r.Storage)
	}
	// total = 0.90 + 0.80 + 2.30 = 4.00
	if !approx(r.TotalMonthly, 4.00) {
		t.Errorf("total monthly = %v, want 4.00", r.TotalMonthly)
	}
}

// Ingest is a flow: a byte count over a short window extrapolates to a monthly
// bill. A gauge (active series) does not scale with the window.
func TestPriceExtrapolatesIngestToMonth(t *testing.T) {
	u := cost.Usage{
		Window:  1 * time.Hour, // factor 730
		Streams: []cost.Stream{{Service: "cart", Signal: "metrics", IngestBytes: 1e9, ActiveSeries: 1e6}},
	}
	r := cost.Price(testPricing(), u)
	l := lineFor(r, "cart", "metrics")
	if l == nil {
		t.Fatal("no line")
	}
	// 1 GB × 0.50 × 730 = 365.0 ingest; series gauge unscaled: 1e6/1e6 × 0.20 = 0.20
	if !approx(l.IngestMonthly, 365.0) {
		t.Errorf("ingest monthly = %v, want 365.0 (extrapolated)", l.IngestMonthly)
	}
	if !approx(l.ActiveSeriesMonthly, 0.20) {
		t.Errorf("series monthly = %v, want 0.20 (gauge, unscaled)", l.ActiveSeriesMonthly)
	}
}

// Streams sharing a (service, team, signal) key are summed, and output is
// deterministically ordered — showback reports must diff cleanly.
func TestPriceAggregatesAndSorts(t *testing.T) {
	u := cost.Usage{
		Window: 730 * time.Hour,
		Streams: []cost.Stream{
			{Service: "b", Signal: "logs", IngestBytes: 1e9},
			{Service: "a", Signal: "metrics", IngestBytes: 1e9},
			{Service: "b", Signal: "logs", IngestBytes: 1e9}, // same key as first
		},
	}
	r := cost.Price(testPricing(), u)
	if len(r.Lines) != 2 {
		t.Fatalf("want 2 aggregated lines, got %d: %+v", len(r.Lines), r.Lines)
	}
	if r.Lines[0].Service != "a" || r.Lines[1].Service != "b" {
		t.Errorf("lines not sorted by service: %+v", r.Lines)
	}
	// b/logs summed: 2 GB × 0.40 = 0.80
	if !approx(r.Lines[1].IngestMonthly, 0.80) {
		t.Errorf("b/logs = %v, want 0.80 (summed)", r.Lines[1].IngestMonthly)
	}
}

func approx(a, b float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d < 1e-6
}
