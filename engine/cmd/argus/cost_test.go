package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tamen25/Argus/engine/internal/cost"
)

func writePricing(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "pricing.yaml")
	body := "schema: argus.pricing/v1\ncurrency: USD\nactive_series:\n  per_million: 8.0\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// With no backend flags the command refuses rather than printing an empty
// (misleadingly $0) report.
func TestRunCostRequiresASource(t *testing.T) {
	_, err := runCost(context.Background(), &costOptions{pricingPath: writePricing(t), window: time.Hour})
	if err == nil || !strings.Contains(err.Error(), "at least one source") {
		t.Errorf("want no-source error, got %v", err)
	}
}

// A bad pricing path surfaces the loader error, not a panic.
func TestRunCostBadPricing(t *testing.T) {
	_, err := runCost(context.Background(), &costOptions{pricingPath: "nope.yaml", window: time.Hour, mimirURL: "http://x"})
	if err == nil {
		t.Error("want error for missing pricing file")
	}
}

// renderShowback honors the format flag and rejects unknown ones.
func TestRenderShowbackFormats(t *testing.T) {
	sb := cost.Showback{
		GeneratedAt: time.Now().UTC(),
		Window:      "1h0m0s",
		Report: cost.Report{
			Currency:     "USD",
			Lines:        []cost.Line{{Service: "checkout", Signal: "metrics", TotalMonthly: 8.0}},
			TotalMonthly: 8.0,
		},
	}
	md, err := renderShowback(sb, "md")
	if err != nil || !strings.Contains(md, "Argus Cost & Showback") {
		t.Errorf("md render = %q err=%v", md, err)
	}
	js, err := renderShowback(sb, "json")
	if err != nil || !strings.Contains(js, `"total_monthly"`) {
		t.Errorf("json render = %q err=%v", js, err)
	}
	if _, err := renderShowback(sb, "xml"); err == nil {
		t.Error("want error for unknown format")
	}
}
