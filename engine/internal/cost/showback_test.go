package cost_test

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tamen25/Argus/engine/internal/cost"
)

var update = flag.Bool("update", false, "rewrite golden files")

func sampleShowback() cost.Showback {
	return cost.Showback{
		GeneratedAt: time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC),
		Window:      "1h0m0s",
		Report: cost.Report{
			Currency: "USD",
			Lines: []cost.Line{
				{Service: "cart", Signal: "logs", IngestMonthly: 12.34, TotalMonthly: 12.34},
				{Service: "checkout", Signal: "metrics", ActiveSeriesMonthly: 4.00, TotalMonthly: 4.00},
			},
			Storage:      []cost.StorageLine{{Class: "STANDARD", GB: 1000, Monthly: 23.00}},
			TotalMonthly: 39.34,
		},
		Lifecycle: []cost.Recommendation{
			{FromClass: "STANDARD", ToClass: "GLACIER_IR", GB: 1000, CurrentMonthly: 23.00, ProjectedMonthly: 4.00, SavingsMonthly: 19.00},
		},
		Trend: &cost.TrendReport{
			Currency:   "USD",
			TotalDelta: 5.00,
			Lines: []cost.TrendLine{
				{Service: "checkout", Signal: "metrics", Previous: 3.00, Current: 4.00, Delta: 1.00, PercentDelta: 33.33},
			},
		},
		Notes: []string{"Costs are modeled from your pricing.yaml, not billed."},
	}
}

func TestShowbackMarkdownGolden(t *testing.T) {
	md := cost.RenderMarkdown(sampleShowback())

	// invariants regardless of golden state
	for _, want := range []string{"Total: 39.34 USD", "Lifecycle savings", "19.00", "Week-over-week", "+5.00"} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown missing %q", want)
		}
	}

	golden := filepath.Join("testdata", "showback.golden.md")
	if *update {
		if err := os.WriteFile(golden, []byte(md), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("read golden (run with -update first): %v", err)
	}
	if md != string(want) {
		t.Errorf("markdown != golden; run -update to inspect the diff")
	}
}

func TestShowbackJSON(t *testing.T) {
	data, err := cost.RenderJSON(sampleShowback())
	if err != nil {
		t.Fatal(err)
	}
	var back cost.Showback
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	if back.Report.TotalMonthly != 39.34 || back.Trend == nil || back.Trend.TotalDelta != 5.00 {
		t.Errorf("round-trip = %+v", back)
	}
}
