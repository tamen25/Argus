// Package rules implements the deterministic rule engine: YAML+CEL rule
// definitions (architecture rule 4: rules are data), per-item and
// per-aggregate evaluation, and the Instrumentation Score calculation
// following the vendored specification exactly (see rules/spec/upstream and
// /.instrumentation-score-version).
//
// This package must never import the LLM client (architecture rule 2,
// depguard-enforced).
package rules

import "fmt"

// SchemaV1 is the only accepted rule schema version.
const SchemaV1 = "argus.rules/v1"

// Impact levels and weights are fixed by the Instrumentation Score spec.
type Impact string

const (
	ImpactCritical  Impact = "critical"
	ImpactImportant Impact = "important"
	ImpactNormal    Impact = "normal"
	ImpactLow       Impact = "low"
)

// Weight returns the spec-defined weight for the impact level.
func (i Impact) Weight() int {
	switch i {
	case ImpactCritical:
		return 40
	case ImpactImportant:
		return 30
	case ImpactNormal:
		return 20
	case ImpactLow:
		return 10
	default:
		return 0
	}
}

func (i Impact) valid() bool { return i.Weight() != 0 }

// Evaluation modes.
const (
	ModeItem      = "item"      // CEL criteria per normalized telemetry item
	ModeAggregate = "aggregate" // CEL criteria per named aggregate row
)

// Rule targets mirror the spec's Target attribute.
var validTargets = map[string]bool{"resource": true, "span": true, "metric": true, "log": true}

// Rule sources: spec rules feed the Instrumentation Score; argus extension
// rules produce findings and a separately-reported extension score (the spec
// forbids mixing non-spec rules into the spec score).
var validSources = map[string]bool{"spec": true, "argus": true}

// Rule is one YAML rule definition (schema argus.rules/v1).
type Rule struct {
	Schema      string         `yaml:"schema"`
	ID          string         `yaml:"id"`
	Source      string         `yaml:"source"`
	Name        string         `yaml:"name"`
	Description string         `yaml:"description"`
	Target      string         `yaml:"target"`
	Impact      Impact         `yaml:"impact"`
	Evaluation  Evaluation     `yaml:"evaluation"`
	Params      map[string]any `yaml:"params"`
	// ServiceViolation controls how per-item violations roll up to a
	// per-service pass/fail. A service fails the rule when
	// violations/observed > ThresholdRatio (default 0: any violation fails).
	ServiceViolation ServiceViolation `yaml:"service_violation"`
	// Confidence optionally names a poller check that can verify this rule
	// from a backend that sees unsampled data.
	Confidence ConfidenceSpec `yaml:"confidence"`
	// Remediation names the patch template for this rule (Phase 1: informative).
	Remediation RemediationSpec `yaml:"remediation"`
}

// Evaluation holds the CEL criteria. Criteria evaluates to true on SUCCESS
// (mirroring the spec's rule Criteria); a false result is a violation.
type Evaluation struct {
	Mode      string `yaml:"mode"`
	Aggregate string `yaml:"aggregate"`
	Criteria  string `yaml:"criteria"`
}

// ServiceViolation controls item-rule rollup per service.
type ServiceViolation struct {
	ThresholdRatio float64 `yaml:"threshold_ratio"`
}

// ConfidenceSpec names an optional poller-side verification.
type ConfidenceSpec struct {
	Poller string `yaml:"poller"`
}

// RemediationSpec names the remediation template for the rule.
type RemediationSpec struct {
	Template string `yaml:"template"`
}

func (r *Rule) validate() error {
	if r.Schema != SchemaV1 {
		return fmt.Errorf("rule %q: unsupported schema %q (want %s)", r.ID, r.Schema, SchemaV1)
	}
	if r.ID == "" {
		return fmt.Errorf("rule missing id")
	}
	if !validSources[r.Source] {
		return fmt.Errorf("rule %q: invalid source %q", r.ID, r.Source)
	}
	if r.Name == "" || r.Description == "" {
		return fmt.Errorf("rule %q: name and description are required", r.ID)
	}
	if !validTargets[r.Target] {
		return fmt.Errorf("rule %q: invalid target %q", r.ID, r.Target)
	}
	if !r.Impact.valid() {
		return fmt.Errorf("rule %q: invalid impact %q", r.ID, r.Impact)
	}
	switch r.Evaluation.Mode {
	case ModeItem:
		if r.Evaluation.Aggregate != "" {
			return fmt.Errorf("rule %q: aggregate name is only valid in aggregate mode", r.ID)
		}
	case ModeAggregate:
		if r.Evaluation.Aggregate == "" {
			return fmt.Errorf("rule %q: aggregate mode requires an aggregate name", r.ID)
		}
	default:
		return fmt.Errorf("rule %q: invalid evaluation mode %q", r.ID, r.Evaluation.Mode)
	}
	if r.Evaluation.Criteria == "" {
		return fmt.Errorf("rule %q: evaluation.criteria is required", r.ID)
	}
	if r.ServiceViolation.ThresholdRatio < 0 || r.ServiceViolation.ThresholdRatio >= 1 {
		return fmt.Errorf("rule %q: service_violation.threshold_ratio must be in [0,1)", r.ID)
	}
	return nil
}
