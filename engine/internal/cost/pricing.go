// Package cost is the deterministic cost engine (module A2, Spend): it turns
// measured LGTM usage — ingest bytes, active series, object storage by class —
// into attributed dollar costs, and prices findings that carry a cost
// dimension. It never imports the LLM client (architecture rule 2, depguard).
//
// Hexagonal: this package takes a plain Usage value; the Mimir/Loki/Tempo/S3
// pollers that measure usage live behind interfaces in adapter packages and
// never leak in here.
package cost

import (
	"bytes"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// PricingSchema is the only accepted schema tag; bump it deliberately when the
// shape changes so old configs fail loudly instead of parsing to zero rates.
const PricingSchema = "argus.pricing/v1"

// Pricing is a user's cost model for their self-hosted stack: the rates that
// convert usage into money. Self-hosted LGTM has no per-GB invoice, so these
// are the user's own modeled unit costs (compute + storage amortized), shipped
// with AWS-default and generic templates.
type Pricing struct {
	Schema       string       `yaml:"schema"`
	Currency     string       `yaml:"currency"`
	Ingest       Ingest       `yaml:"ingest"`
	ActiveSeries ActiveSeries `yaml:"active_series"`
	Storage      Storage      `yaml:"storage"`
}

// Ingest prices telemetry volume: a default $/GB with optional per-signal
// overrides (logs and metrics rarely cost the same to store).
type Ingest struct {
	PerGB         float64            `yaml:"per_gb"`
	PerGBBySignal map[string]float64 `yaml:"per_gb_by_signal"`
}

// RateFor returns the per-GB ingest rate for a signal, preferring an explicit
// override and falling back to the default.
func (i Ingest) RateFor(signal string) float64 {
	if r, ok := i.PerGBBySignal[signal]; ok {
		return r
	}
	return i.PerGB
}

// ActiveSeries prices Mimir's dominant cost driver: series held in memory.
type ActiveSeries struct {
	PerMillion float64 `yaml:"per_million"`
}

// Storage prices object storage per GB-month, keyed by storage class so
// lifecycle modeling (move cold blocks to a cheaper class) can be priced.
type Storage struct {
	PerGBMonthByClass map[string]float64 `yaml:"per_gb_month_by_class"`
}

// RateFor returns the $/GB-month rate for a storage class; unpriced classes
// return 0 (the caller decides whether that's a config gap to surface).
func (s Storage) RateFor(class string) float64 {
	return s.PerGBMonthByClass[class]
}

// LoadPricing reads and strictly parses a pricing config. Unknown keys are an
// error: a mistyped rate that silently prices at zero is worse than a failed
// load.
func LoadPricing(path string) (*Pricing, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	var p Pricing
	if err := dec.Decode(&p); err != nil {
		return nil, fmt.Errorf("parsing pricing %s: %w", path, err)
	}
	if err := p.Validate(); err != nil {
		return nil, fmt.Errorf("invalid pricing %s: %w", path, err)
	}
	return &p, nil
}

// Validate enforces the schema tag, a currency, and non-negative rates.
func (p *Pricing) Validate() error {
	if p.Schema != PricingSchema {
		return fmt.Errorf("schema %q, want %q", p.Schema, PricingSchema)
	}
	if p.Currency == "" {
		return fmt.Errorf("currency is required")
	}
	for name, r := range map[string]float64{
		"ingest.per_gb":             p.Ingest.PerGB,
		"active_series.per_million": p.ActiveSeries.PerMillion,
	} {
		if r < 0 {
			return fmt.Errorf("%s is negative (%v)", name, r)
		}
	}
	for sig, r := range p.Ingest.PerGBBySignal {
		if r < 0 {
			return fmt.Errorf("ingest.per_gb_by_signal[%s] is negative (%v)", sig, r)
		}
	}
	for class, r := range p.Storage.PerGBMonthByClass {
		if r < 0 {
			return fmt.Errorf("storage.per_gb_month_by_class[%s] is negative (%v)", class, r)
		}
	}
	return nil
}
