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
	// Calibration optionally names the observed distribution that can
	// propose a better value for ONE params key (argus rules calibrate).
	// Calibration never touches criteria — only params the spec leaves open
	// and argus-extension params.
	Calibration CalibrationSpec `yaml:"calibration"`
	// Cost optionally declares how the cost engine prices this rule's
	// findings (estimated_monthly_cost). Data, not code: the deterministic
	// pricer reads the driver + quantity field, never rule-specific logic.
	Cost CostSpec `yaml:"cost"`
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

// CalibrationSpec ties one params key to the distribution that informs it.
type CalibrationSpec struct {
	Param string `yaml:"param"` // params key the proposal targets
	// Source of observations: "aggregate" (rows from /api/aggregates or a
	// soak dir) or "finding_ratio" (per-service violation ratios from
	// reports/Postgres; failing services only — calibrate discloses that).
	Source    string `yaml:"source"`
	Aggregate string `yaml:"aggregate"` // aggregate name (source=aggregate)
	Field     string `yaml:"field"`     // numeric field in the row (source=aggregate)
	// Kind selects the deterministic proposal formula:
	//   count       — ceil₂sig(P99 × 2)         (large cardinalities)
	//   small_count — ceil(P99) + 1             (single-digit value spreads)
	//   ratio       — min(1, P99 + max(0.05, 2×MAD)) rounded to 2 decimals
	Kind string `yaml:"kind"`
}

// CostSpec declares how the cost engine prices a rule's findings. The pricer
// reads QuantityField from each finding (Details, then the worst Evidence
// sample) and multiplies by the Driver's unit rate.
type CostSpec struct {
	// Driver selects the pricing basis: currently "active_series" (the
	// quantity is a series count priced against active_series.per_million).
	Driver string `yaml:"driver"`
	// QuantityField names the numeric field in the finding holding the
	// cost-bearing quantity (e.g. "series").
	QuantityField string `yaml:"quantity_field"`
}

// ServiceViolationParam is the dotted calibration target for the item-rule
// rollup threshold, which lives outside params.
const ServiceViolationParam = "service_violation.threshold_ratio"

var validCalibrationSources = map[string]bool{"aggregate": true, "finding_ratio": true}
var validCalibrationKinds = map[string]bool{"count": true, "small_count": true, "ratio": true}

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
	// The blocks below say what is wrong; the rule ID is added once, here.
	for _, err := range []error{
		r.Evaluation.validate(),
		r.ServiceViolation.validate(),
		r.Calibration.validate(r.Params),
		r.Cost.validate(),
	} {
		if err != nil {
			return fmt.Errorf("rule %q: %w", r.ID, err)
		}
	}
	return nil
}

func (e Evaluation) validate() error {
	switch e.Mode {
	case ModeItem:
		if e.Aggregate != "" {
			return fmt.Errorf("aggregate name is only valid in aggregate mode")
		}
	case ModeAggregate:
		if e.Aggregate == "" {
			return fmt.Errorf("aggregate mode requires an aggregate name")
		}
	default:
		return fmt.Errorf("invalid evaluation mode %q", e.Mode)
	}
	if e.Criteria == "" {
		return fmt.Errorf("evaluation.criteria is required")
	}
	return nil
}

func (s ServiceViolation) validate() error {
	if s.ThresholdRatio < 0 || s.ThresholdRatio >= 1 {
		return fmt.Errorf("service_violation.threshold_ratio must be in [0,1)")
	}
	return nil
}

// validate checks a calibration block, if the rule has one.
func (c CalibrationSpec) validate(params map[string]any) error {
	if c.Param == "" {
		return nil
	}
	// The param target is a params key, or the one non-params numeric knob a
	// rule has: the item-rollup threshold.
	if _, ok := params[c.Param]; !ok && c.Param != ServiceViolationParam {
		return fmt.Errorf("calibration.param %q not present in params", c.Param)
	}
	if !validCalibrationSources[c.Source] {
		return fmt.Errorf("invalid calibration.source %q", c.Source)
	}
	if !validCalibrationKinds[c.Kind] {
		return fmt.Errorf("invalid calibration.kind %q", c.Kind)
	}
	if c.Source == "aggregate" && (c.Aggregate == "" || c.Field == "") {
		return fmt.Errorf("calibration.source=aggregate requires aggregate and field")
	}
	return nil
}

// validate checks a cost block, if the rule has one.
func (c CostSpec) validate() error {
	if c.Driver == "" {
		return nil
	}
	if !validCostDrivers[c.Driver] {
		return fmt.Errorf("invalid cost.driver %q", c.Driver)
	}
	if c.QuantityField == "" {
		return fmt.Errorf("cost.driver requires cost.quantity_field")
	}
	return nil
}

// validCostDrivers are the pricing bases the cost engine understands.
var validCostDrivers = map[string]bool{"active_series": true}
