package bench

import (
	"bytes"
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// APIVersion and Kind are the only accepted scenario envelope values. A typo in
// either is a load error, not a silently skipped scenario — a bench run that
// quietly drops a scenario reports a smaller, wrong matrix.
const (
	APIVersion = "argus/v1alpha1"
	Kind       = "BenchScenario"
)

// Valid inject step types (master plan §3.2).
const (
	InjectChaosMesh = "chaosmesh"
	InjectKubectl   = "kubectl"
	InjectScript    = "script"
)

// Valid entity-match strategies for the scoring block.
const (
	MatchJaccard = "jaccard"
	MatchExact   = "exact"
)

// Scenario is one bench scenario (scenarios/*.yaml). It describes a workload, an
// ordered set of fault injections, the ground-truth root cause, and how a
// diagnosis is scored against it.
type Scenario struct {
	APIVersion string       `yaml:"apiVersion"`
	Kind       string       `yaml:"kind"`
	Metadata   Metadata     `yaml:"metadata"`
	Spec       ScenarioSpec `yaml:"spec"`
}

// Metadata carries the scenario name (used as the run key and result label)
// plus optional provenance, so an imported scenario can cite where it came from.
type Metadata struct {
	Name string `yaml:"name"`
	// Description is a human summary, carried through from an import.
	Description string `yaml:"description,omitempty"`
	// Source records provenance for imported scenarios (e.g. "itbench:sre/102"),
	// so a published comparison can be traced back to the upstream definition.
	Source string `yaml:"source,omitempty"`
}

// ScenarioSpec is the body: target environment, fault steps, ground truth, and
// scoring config.
type ScenarioSpec struct {
	Environment Environment  `yaml:"environment"`
	Inject      []InjectStep `yaml:"inject"`
	GroundTruth GroundTruth  `yaml:"groundTruth"`
	Scoring     ScoringSpec  `yaml:"scoring"`
}

// Environment names the target workload the scenario runs against.
type Environment struct {
	App string `yaml:"app"`
}

// InjectStep is one ordered fault injection. Exactly one of Manifest (for
// chaosmesh/kubectl) or Script (for script) is set, per Type.
type InjectStep struct {
	Type     string `yaml:"type"`
	Manifest string `yaml:"manifest"`
	Script   string `yaml:"script"`
	Duration string `yaml:"duration"`
}

// Dur parses the step's duration string (e.g. "10m"). Kept as a string on the
// wire so the YAML stays human-authored; validated at load time.
func (s InjectStep) Dur() (time.Duration, error) {
	return time.ParseDuration(s.Duration)
}

// GroundTruth is the labeled correct answer a diagnosis is scored against —
// ITBench-style root-cause entities plus a fault category.
type GroundTruth struct {
	RootCauseEntities []Entity `yaml:"rootCauseEntities" json:"root_cause_entities"`
	Category          string   `yaml:"category" json:"category"`
}

// Entity is a Kubernetes object referenced as a root cause. It is the shared
// unit of comparison between ground truth and an agent's diagnosis.
type Entity struct {
	Kind      string `yaml:"kind" json:"kind"`
	Namespace string `yaml:"namespace" json:"namespace"`
	Name      string `yaml:"name" json:"name"`
}

// ScoringSpec configures how a diagnosis is scored against the ground truth.
type ScoringSpec struct {
	EntityMatch   string `yaml:"entityMatch"`
	PartialCredit bool   `yaml:"partialCredit"`
}

// LoadScenario strictly parses a scenario file. Unknown keys, a wrong envelope,
// an empty inject list, malformed durations, or a ground truth with no entities
// are all errors: every one of them would silently corrupt a run's matrix or
// its verdicts.
func LoadScenario(path string) (Scenario, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Scenario{}, err
	}
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	var s Scenario
	if err := dec.Decode(&s); err != nil {
		return Scenario{}, fmt.Errorf("parsing scenario %s: %w", path, err)
	}
	if err := s.validate(); err != nil {
		return Scenario{}, fmt.Errorf("scenario %s: %w", path, err)
	}
	return s, nil
}

func (s Scenario) validate() error {
	if s.APIVersion != APIVersion {
		return fmt.Errorf("apiVersion %q, want %q", s.APIVersion, APIVersion)
	}
	if s.Kind != Kind {
		return fmt.Errorf("kind %q, want %q", s.Kind, Kind)
	}
	if s.Metadata.Name == "" {
		return fmt.Errorf("metadata.name is empty")
	}
	if s.Spec.Environment.App == "" {
		return fmt.Errorf("spec.environment.app is empty")
	}
	if len(s.Spec.Inject) == 0 {
		return fmt.Errorf("spec.inject has no steps")
	}
	for i, step := range s.Spec.Inject {
		if err := step.validate(); err != nil {
			return fmt.Errorf("spec.inject[%d]: %w", i, err)
		}
	}
	if len(s.Spec.GroundTruth.RootCauseEntities) == 0 {
		return fmt.Errorf("spec.groundTruth.rootCauseEntities is empty")
	}
	for i, e := range s.Spec.GroundTruth.RootCauseEntities {
		if e.Kind == "" || e.Name == "" {
			return fmt.Errorf("spec.groundTruth.rootCauseEntities[%d]: kind and name are required", i)
		}
	}
	if s.Spec.GroundTruth.Category == "" {
		return fmt.Errorf("spec.groundTruth.category is empty")
	}
	switch s.Spec.Scoring.EntityMatch {
	case "", MatchJaccard, MatchExact: // "" defaults to jaccard downstream
	default:
		return fmt.Errorf("spec.scoring.entityMatch %q, want %q or %q",
			s.Spec.Scoring.EntityMatch, MatchJaccard, MatchExact)
	}
	return nil
}

func (s InjectStep) validate() error {
	switch s.Type {
	case InjectChaosMesh, InjectKubectl:
		if s.Manifest == "" {
			return fmt.Errorf("type %q requires manifest", s.Type)
		}
		if s.Script != "" {
			return fmt.Errorf("type %q must not set script", s.Type)
		}
	case InjectScript:
		if s.Script == "" {
			return fmt.Errorf("type %q requires script", s.Type)
		}
		if s.Manifest != "" {
			return fmt.Errorf("type %q must not set manifest", s.Type)
		}
	default:
		return fmt.Errorf("type %q, want %q, %q or %q",
			s.Type, InjectChaosMesh, InjectKubectl, InjectScript)
	}
	if s.Duration == "" {
		return fmt.Errorf("duration is empty")
	}
	if _, err := s.Dur(); err != nil {
		return fmt.Errorf("duration %q: %w", s.Duration, err)
	}
	return nil
}
