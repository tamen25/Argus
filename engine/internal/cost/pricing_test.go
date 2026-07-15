package cost_test

import (
	"path/filepath"
	"testing"

	"github.com/tamen25/Argus/engine/internal/cost"
)

// A pricing config is data: a versioned, strictly-parsed YAML file that turns
// measured usage into money. The loader rejects unknown keys (typo'd rates
// silently costing nothing is a footgun) and validates the schema tag.
func TestLoadPricingValid(t *testing.T) {
	p, err := cost.LoadPricing(filepath.Join("testdata", "pricing-valid.yaml"))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if p.Currency != "USD" {
		t.Errorf("currency = %q, want USD", p.Currency)
	}
	// per-signal override wins; unlisted signals fall back to the default rate
	if got := p.Ingest.RateFor("logs"); got != 0.40 {
		t.Errorf("logs ingest rate = %v, want 0.40 (override)", got)
	}
	if got := p.Ingest.RateFor("traces"); got != 0.50 {
		t.Errorf("traces ingest rate = %v, want 0.50 (default)", got)
	}
	if got := p.Storage.RateFor("GLACIER_IR"); got != 0.004 {
		t.Errorf("GLACIER_IR rate = %v, want 0.004", got)
	}
	if p.ActiveSeries.PerMillion != 0.20 {
		t.Errorf("active series rate = %v, want 0.20", p.ActiveSeries.PerMillion)
	}
}

func TestLoadPricingRejectsUnknownKey(t *testing.T) {
	_, err := cost.LoadPricing(filepath.Join("testdata", "pricing-unknown-key.yaml"))
	if err == nil {
		t.Fatal("want error for unknown key, got nil")
	}
}

func TestLoadPricingRejectsWrongSchema(t *testing.T) {
	_, err := cost.LoadPricing(filepath.Join("testdata", "pricing-bad-schema.yaml"))
	if err == nil {
		t.Fatal("want error for wrong schema tag, got nil")
	}
}

func TestLoadPricingRejectsNegativeRate(t *testing.T) {
	_, err := cost.LoadPricing(filepath.Join("testdata", "pricing-negative.yaml"))
	if err == nil {
		t.Fatal("want error for negative rate, got nil")
	}
}
