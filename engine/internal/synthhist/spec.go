// Package synthhist generates synthetic telemetry history: deterministic,
// seeded metric series with embedded incident signatures, written as real
// TSDB blocks a Mimir store-gateway can serve. It exists because real dev
// history accumulates in short gappy sessions (measured: 36h across 6 days —
// docs/backtest-fidelity.md), while backtest demos and CI need multi-week
// continuous windows with KNOWN ground truth.
//
// Dev tool only (argus devtools synth-history): it writes blocks to a local
// directory; pushing them into a backend is a deliberate, documented step.
// The read-only-product rule (architecture rule 5) is about user systems —
// this generates files.
package synthhist

import (
	"bytes"
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// SpecSchema is the accepted schema tag.
const SpecSchema = "argus.synth/v1"

// Spec declares the history to fabricate — data, not code (architecture
// rule 4).
type Spec struct {
	Schema string    `yaml:"schema"`
	Seed   int64     `yaml:"seed"`
	From   time.Time `yaml:"from"`
	To     time.Time `yaml:"to"`
	// Step is the sample interval (default 30s).
	Step     time.Duration `yaml:"step"`
	Services []Service     `yaml:"services"`
	// Incidents are embedded fault signatures; the generator also emits a
	// matching incidents.yaml so the ground truth ships with the data.
	Incidents []SynthIncident `yaml:"incidents"`
}

// Service is one synthetic service's steady-state shape.
type Service struct {
	Name string `yaml:"name"`
	// RatePerSec is the request-counter rate.
	RatePerSec float64 `yaml:"rate_per_sec"`
	// ErrorRatio is the steady-state error fraction (0-1).
	ErrorRatio float64 `yaml:"error_ratio"`
	// Jitter is the relative random variation applied per sample (0-1).
	Jitter float64 `yaml:"jitter"`
}

// SynthIncident is one embedded fault: the named service's error ratio is
// raised to ErrorRatio for [Start, End).
type SynthIncident struct {
	ID         string    `yaml:"id"`
	Service    string    `yaml:"service"`
	Start      time.Time `yaml:"start"`
	End        time.Time `yaml:"end"`
	ErrorRatio float64   `yaml:"error_ratio"`
}

// LoadSpec strictly parses a synth spec. Unknown keys are errors.
func LoadSpec(path string) (Spec, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Spec{}, err
	}
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	var s Spec
	if err := dec.Decode(&s); err != nil {
		return Spec{}, fmt.Errorf("parsing synth spec %s: %w", path, err)
	}
	if err := s.Validate(); err != nil {
		return Spec{}, fmt.Errorf("invalid synth spec %s: %w", path, err)
	}
	return s, nil
}

// Validate enforces the schema tag and internally consistent windows.
func (s *Spec) Validate() error {
	if s.Schema != SpecSchema {
		return fmt.Errorf("schema %q, want %q", s.Schema, SpecSchema)
	}
	if !s.To.After(s.From) {
		return fmt.Errorf("to must be after from")
	}
	if s.Step <= 0 {
		s.Step = 30 * time.Second
	}
	if len(s.Services) == 0 {
		return fmt.Errorf("at least one service is required")
	}
	names := map[string]bool{}
	for _, svc := range s.Services {
		if svc.Name == "" {
			return fmt.Errorf("service with empty name")
		}
		names[svc.Name] = true
		if svc.ErrorRatio < 0 || svc.ErrorRatio > 1 {
			return fmt.Errorf("service %s: error_ratio %v outside [0,1]", svc.Name, svc.ErrorRatio)
		}
	}
	for _, inc := range s.Incidents {
		if !names[inc.Service] {
			return fmt.Errorf("incident %s references unknown service %q", inc.ID, inc.Service)
		}
		if !inc.End.After(inc.Start) {
			return fmt.Errorf("incident %s ends before it starts", inc.ID)
		}
		if inc.Start.Before(s.From) || inc.End.After(s.To) {
			return fmt.Errorf("incident %s falls outside the generated window", inc.ID)
		}
	}
	return nil
}
