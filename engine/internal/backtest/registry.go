package backtest

import (
	"bytes"
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Registry is the parsed incident registry (incidents.yaml, schema v1) —
// user-labeled ground truth the backtest scores against.
type Registry struct {
	Version   int        `yaml:"version"`
	Incidents []Incident `yaml:"incidents"`
}

// Incident is one labeled impact window.
type Incident struct {
	ID             string    `yaml:"id"`
	Title          string    `yaml:"title"`
	Start          time.Time `yaml:"start"`
	End            time.Time `yaml:"end"`
	Services       []string  `yaml:"services"`
	ExpectedAlerts []string  `yaml:"expected_alerts"`
	Induced        bool      `yaml:"induced"`
	Fault          string    `yaml:"fault"`
	Notes          string    `yaml:"notes"`
}

// LoadIncidents strictly parses an incident registry. Unknown keys and
// inverted windows are errors: a silently dropped incident becomes a
// silently wrong "no misses" verdict.
func LoadIncidents(path string) (Registry, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Registry{}, err
	}
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	var r Registry
	if err := dec.Decode(&r); err != nil {
		return Registry{}, fmt.Errorf("parsing incidents %s: %w", path, err)
	}
	if r.Version != 1 {
		return Registry{}, fmt.Errorf("incidents %s: version %d, want 1", path, r.Version)
	}
	seen := map[string]bool{}
	for _, inc := range r.Incidents {
		if inc.ID == "" {
			return Registry{}, fmt.Errorf("incidents %s: incident with empty id", path)
		}
		if seen[inc.ID] {
			return Registry{}, fmt.Errorf("incidents %s: duplicate id %q", path, inc.ID)
		}
		seen[inc.ID] = true
		if !inc.End.After(inc.Start) {
			return Registry{}, fmt.Errorf("incidents %s: %s ends before it starts", path, inc.ID)
		}
	}
	return r, nil
}
