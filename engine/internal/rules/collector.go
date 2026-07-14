package rules

import (
	"sort"
	"sync"

	"github.com/tamen25/Argus/engine/internal/model"
)

// MaxEvidence caps evidence samples per finding (security/privacy rule:
// findings store bounded evidence, values already truncated by the model).
const MaxEvidence = 5

// Confidence of a finding: sampled (OTLP mirror only) or verified (backed by
// a poller that sees unsampled data).
const (
	ConfidenceSampled  = "sampled"
	ConfidenceVerified = "verified"
)

// Evidence is one bounded violation sample.
type Evidence struct {
	Kind    string         `json:"kind"`
	Summary string         `json:"summary,omitempty"`
	Attrs   map[string]any `json:"attrs,omitempty"`
}

// Stats are the counts behind a finding.
type Stats struct {
	Observed   int64   `json:"observed"`
	Violations int64   `json:"violations"`
	Ratio      float64 `json:"ratio"`
}

// Finding is one failed rule for one service.
type Finding struct {
	RuleID      string         `json:"rule_id"`
	RuleName    string         `json:"rule_name"`
	Source      string         `json:"source"`
	Service     string         `json:"service"`
	Impact      Impact         `json:"impact"`
	Description string         `json:"description"`
	Confidence  string         `json:"confidence"`
	Stats       Stats          `json:"stats"`
	Evidence    []Evidence     `json:"evidence,omitempty"`
	Details     map[string]any `json:"details,omitempty"`
	// EstimatedMonthlyCost is populated by the cost engine in Phase 2.
	EstimatedMonthlyCost *float64 `json:"estimated_monthly_cost,omitempty"`
}

// PollerResult is a backend-verified rule outcome for a service. It overrides
// the sampled outcome (the poller sees everything; the stream is sampled).
type PollerResult struct {
	Service string
	RuleID  string
	Passed  bool
	Details map[string]any
}

// Collector accumulates rule outcomes with bounded memory: counters per
// (service, rule) plus capped evidence. Safe for concurrent use.
type Collector struct {
	eng *Engine

	mu     sync.Mutex
	svc    map[string]*svcState
	poller map[string]map[string]PollerResult // service -> rule -> result
}

type svcState struct {
	// observed counts per rule target kind ("" key = resource rules, which
	// observe every item)
	observedByRule map[string]int64
	violations     map[string]int64
	evalErrs       map[string]int64
	evidence       map[string][]Evidence
	// Aggregate rules re-evaluate every export tick for the life of a serve
	// process, so state is counters + capped evidence, never a growing slice
	// (bounded memory, rule 3 — a soak run caught the earlier append-per-tick
	// version leaking). Evidence keeps the LATEST MaxEvidence rows: current
	// estimates are evidence, day-old ones are noise.
	aggViolations map[string]int64
	aggEvidence   map[string][]Evidence
	aggObserved   map[string]int64
}

// NewCollector builds a collector over the engine's rules.
func NewCollector(eng *Engine) *Collector {
	return &Collector{eng: eng, svc: map[string]*svcState{}, poller: map[string]map[string]PollerResult{}}
}

func (c *Collector) state(svc string) *svcState {
	s, ok := c.svc[svc]
	if !ok {
		s = &svcState{
			observedByRule: map[string]int64{}, violations: map[string]int64{},
			evalErrs: map[string]int64{}, evidence: map[string][]Evidence{},
			aggViolations: map[string]int64{}, aggEvidence: map[string][]Evidence{},
			aggObserved: map[string]int64{},
		}
		c.svc[svc] = s
	}
	return s
}

// ObserveItem evaluates item-mode rules and records outcomes.
func (c *Collector) ObserveItem(it model.Item) {
	viol := c.eng.EvalItem(it)

	c.mu.Lock()
	defer c.mu.Unlock()
	s := c.state(it.Service)
	for _, r := range c.eng.item {
		if targetMatches(r.rule.Target, it.Kind) {
			s.observedByRule[r.rule.ID]++
		}
	}
	for _, v := range viol {
		if v.Err != nil {
			s.evalErrs[v.RuleID]++
			continue
		}
		s.violations[v.RuleID]++
		if len(s.evidence[v.RuleID]) < MaxEvidence {
			s.evidence[v.RuleID] = append(s.evidence[v.RuleID], evidenceFor(it))
		}
	}
}

// ObserveAggregate evaluates aggregate-mode rules against one row.
func (c *Collector) ObserveAggregate(row AggregateRow) {
	viol := c.eng.EvalAggregate(row)

	c.mu.Lock()
	defer c.mu.Unlock()
	s := c.state(row.Service)
	for _, r := range c.eng.aggregate {
		if r.rule.Evaluation.Aggregate == row.Aggregate {
			s.aggObserved[r.rule.ID]++
		}
	}
	for _, v := range viol {
		s.aggViolations[v.RuleID]++
		ev := s.aggEvidence[v.RuleID]
		if len(ev) >= MaxEvidence {
			// latest-wins ring: drop the oldest retained row
			ev = ev[1:]
		}
		s.aggEvidence[v.RuleID] = append(ev, Evidence{Kind: "aggregate", Attrs: v.Fields})
	}
}

// RecordPollerResult stores a verified outcome, overriding sampled data at
// snapshot time.
func (c *Collector) RecordPollerResult(pr PollerResult) {
	c.mu.Lock()
	defer c.mu.Unlock()
	m, ok := c.poller[pr.Service]
	if !ok {
		m = map[string]PollerResult{}
		c.poller[pr.Service] = m
	}
	m[pr.RuleID] = pr
	// ensure the service exists even if the stream never saw it
	c.state(pr.Service)
}

// ServiceReport is the per-service outcome.
type ServiceReport struct {
	ServiceName string       `json:"service"`
	SpecScore   float64      `json:"spec_score"`
	Category    string       `json:"category"`
	Results     []RuleResult `json:"results"`
	// ExtensionScore covers source=argus rules only; nil when none were
	// evaluated. Kept apart from SpecScore — the spec forbids blending.
	ExtensionScore *float64  `json:"extension_score,omitempty"`
	Findings       []Finding `json:"findings,omitempty"`
}

// Snapshot is a point-in-time evaluation of everything observed.
type Snapshot struct {
	FleetScore float64         `json:"fleet_score"`
	Services   []ServiceReport `json:"services"`
	// RulesEvaluated lists rule IDs contributing to scores, for the
	// spec-mandated incomplete-rule-set disclosure.
	RulesEvaluated []string `json:"rules_evaluated"`
}

// Service returns the report for one service, or nil.
func (s *Snapshot) Service(name string) *ServiceReport {
	for i := range s.Services {
		if s.Services[i].ServiceName == name {
			return &s.Services[i]
		}
	}
	return nil
}

// Snapshot computes per-service rule results, scores, and findings.
func (c *Collector) Snapshot() *Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()

	snap := &Snapshot{}
	for _, r := range c.eng.all {
		snap.RulesEvaluated = append(snap.RulesEvaluated, r.ID)
	}

	names := make([]string, 0, len(c.svc))
	for name := range c.svc {
		names = append(names, name)
	}
	sort.Strings(names)

	var fleetSum float64
	for _, name := range names {
		rep := c.serviceReport(name)
		fleetSum += rep.SpecScore
		snap.Services = append(snap.Services, rep)
	}
	if len(snap.Services) > 0 {
		snap.FleetScore = fleetSum / float64(len(snap.Services))
	} else {
		snap.FleetScore = 100
	}
	return snap
}

func (c *Collector) serviceReport(name string) ServiceReport {
	s := c.svc[name]
	rep := ServiceReport{ServiceName: name}
	var specResults, extResults []RuleResult

	for _, r := range c.eng.all {
		var observed, violations int64
		var evid []Evidence
		details := map[string]any{}

		if r.Evaluation.Mode == ModeItem {
			observed = s.observedByRule[r.ID]
			violations = s.violations[r.ID]
			evid = s.evidence[r.ID]
		} else {
			observed = s.aggObserved[r.ID]
			violations = s.aggViolations[r.ID]
			evid = s.aggEvidence[r.ID]
		}
		if errs := s.evalErrs[r.ID]; errs > 0 {
			details["eval_errors"] = errs
		}
		if observed == 0 && violations == 0 {
			// rule never applied to this service's telemetry; skip unless a
			// poller has an opinion
			if _, ok := c.poller[name][r.ID]; !ok {
				continue
			}
		}

		ratio := 0.0
		if observed > 0 {
			ratio = float64(violations) / float64(observed)
		}
		// A service passes an item rule while its violation ratio stays at or
		// below the rule's threshold (default 0: any violation fails).
		passed := ratio <= r.ServiceViolation.ThresholdRatio
		confidence := ConfidenceSampled

		if pr, ok := c.poller[name][r.ID]; ok {
			passed = pr.Passed
			confidence = ConfidenceVerified
			if pr.Details != nil {
				details["poller"] = pr.Details
			}
		}

		res := RuleResult{RuleID: r.ID, Impact: r.Impact, Passed: passed}
		if r.Source == "spec" {
			specResults = append(specResults, res)
		} else {
			extResults = append(extResults, res)
		}
		rep.Results = append(rep.Results, res)

		if !passed {
			if len(details) == 0 {
				details = nil
			}
			rep.Findings = append(rep.Findings, Finding{
				RuleID: r.ID, RuleName: r.Name, Source: r.Source, Service: name,
				Impact: r.Impact, Description: r.Description, Confidence: confidence,
				Stats:    Stats{Observed: observed, Violations: violations, Ratio: ratio},
				Evidence: evid, Details: details,
			})
		}
	}

	rep.SpecScore = Score(specResults)
	rep.Category = Category(rep.SpecScore)
	if len(extResults) > 0 {
		ext := Score(extResults)
		rep.ExtensionScore = &ext
	}
	return rep
}

func evidenceFor(it model.Item) Evidence {
	e := Evidence{Kind: it.Kind.String()}
	switch {
	case it.Span != nil:
		e.Summary = "span " + it.Span.Name
		e.Attrs = it.Resource
	case it.Metric != nil:
		e.Summary = "metric " + it.Metric.Name
		e.Attrs = it.Metric.Attrs
	case it.Log != nil:
		e.Summary = "log severity=" + it.Log.SeverityText
		e.Attrs = it.Resource
	}
	return e
}
