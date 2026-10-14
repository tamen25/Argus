package rules

import (
	"sort"
	"sync"
	"time"

	"github.com/tamen25/Argus/engine/internal/model"
)

// MaxEvidence caps evidence samples per finding (security/privacy rule:
// findings store bounded evidence, values already truncated by the model).
const MaxEvidence = 5

// DefaultMaxServices is how many services a collector tracks at once unless
// told otherwise. Service names come from the telemetry, so a sender that
// keeps inventing them would otherwise grow the collector without limit.
const DefaultMaxServices = 2000

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
	// EstimatedMonthlyCost is set by the cost engine when the finding can be
	// priced, in CostCurrency.
	EstimatedMonthlyCost *float64 `json:"estimated_monthly_cost,omitempty"`
	CostCurrency         string   `json:"cost_currency,omitempty"`
}

// PollerResult is a backend-verified rule outcome for a service. It overrides
// the sampled outcome (the poller sees everything; the stream is sampled).
type PollerResult struct {
	Service string
	RuleID  string
	Passed  bool
	Details map[string]any
}

// CollectorOptions configure a Collector. The zero value keeps every outcome
// for the collector's lifetime, which suits a finite `argus score` run.
type CollectorOptions struct {
	// Window is how long item outcomes count. A report covers the current
	// window and the one before it, so between one and two windows of
	// telemetry, as the aggregate trackers do; a service with nothing in
	// either is dropped. Zero means outcomes never expire.
	Window time.Duration
	// MaxServices caps the services tracked at once. Telemetry for a service
	// past the cap is turned away and counted (Snapshot.RejectedObservations).
	// Zero means DefaultMaxServices.
	MaxServices int
	// Now is the clock; nil means time.Now.
	Now func() time.Time
}

// Collector turns rule evaluations into per-service outcomes, with bounded
// memory: a capped set of services, per-rule counts, and capped evidence.
// Safe for concurrent use.
//
// Item rules (one evaluation per span, metric point or log record) are
// counted over the window. Aggregate rules are different: each export
// evaluates every tracker's current state, so the latest evaluation replaces
// the one before rather than adding to it.
type Collector struct {
	eng  *Engine
	opts CollectorOptions

	mu        sync.Mutex
	svc       map[string]*svcState
	poller    map[string]map[string]polled // service → rule ID → verified outcome
	rotatedAt time.Time
	rejected  int64
}

// svcState is everything one service's report is built from.
type svcState struct {
	cur, prev outcomes // item rules, this window and the one before
	agg       outcomes // aggregate rules, from the latest evaluation
}

// outcomes are per-rule tallies, keyed by rule ID.
type outcomes map[string]*tally

// tally is one rule's counts and evidence for one service.
type tally struct {
	observed, violations int64
	// evalErrs counts evaluations that failed; each is also a violation.
	evalErrs int64
	evidence []Evidence
}

// get returns the rule's tally, creating it on first use.
func (o outcomes) get(ruleID string) *tally {
	t, ok := o[ruleID]
	if !ok {
		t = &tally{}
		o[ruleID] = t
	}
	return t
}

// record counts one evaluation of the rule. A failed evaluation counts as a
// violation: a rule that could not be evaluated has not been passed, and
// counting it as observed only would report it as passing.
func (t *tally) record(v *Violation, ev func() Evidence) {
	t.observed++
	if v == nil {
		return
	}
	t.violations++
	if v.Err != nil {
		t.evalErrs++
	}
	if len(t.evidence) < MaxEvidence {
		t.evidence = append(t.evidence, ev())
	}
}

// polled is a verified outcome and when it was recorded, so it ages out with
// the window like the sampled outcomes it overrides.
type polled struct {
	PollerResult
	at time.Time
}

// NewCollector builds a collector over the engine's rules.
func NewCollector(eng *Engine, opts CollectorOptions) *Collector {
	if opts.MaxServices <= 0 {
		opts.MaxServices = DefaultMaxServices
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &Collector{
		eng: eng, opts: opts,
		svc: map[string]*svcState{}, poller: map[string]map[string]polled{},
		rotatedAt: opts.Now(),
	}
}

// state returns the service's state, adding the service if there is room.
// It returns nil, and counts the rejection, when the service is new and the
// collector is full.
func (c *Collector) state(svc string) *svcState {
	if s, ok := c.svc[svc]; ok {
		return s
	}
	if len(c.svc) >= c.opts.MaxServices {
		c.rejected++
		return nil
	}
	s := &svcState{cur: outcomes{}, agg: outcomes{}}
	c.svc[svc] = s
	return s
}

// rotate starts a new window once the current one has passed: this window's
// item outcomes become the previous window's, and older ones are dropped.
// After two windows with no rotation (nothing observed, no snapshot taken),
// both windows are dropped.
func (c *Collector) rotate() {
	if c.opts.Window <= 0 {
		return
	}
	now := c.opts.Now()
	elapsed := now.Sub(c.rotatedAt)
	if elapsed < c.opts.Window {
		return
	}
	for _, s := range c.svc {
		s.prev = s.cur
		if elapsed >= 2*c.opts.Window {
			s.prev = nil
		}
		s.cur = outcomes{}
	}
	for svc, byRule := range c.poller {
		for id, p := range byRule {
			if now.Sub(p.at) >= 2*c.opts.Window {
				delete(byRule, id)
			}
		}
		if len(byRule) == 0 {
			delete(c.poller, svc)
		}
	}
	c.rotatedAt = now
	c.prune()
}

// prune drops services that have no outcome left: nothing in either window,
// nothing in the latest aggregate evaluation, and no verified result.
func (c *Collector) prune() {
	for name, s := range c.svc {
		if len(s.cur) == 0 && len(s.prev) == 0 && len(s.agg) == 0 && len(c.poller[name]) == 0 {
			delete(c.svc, name)
		}
	}
}

// ObserveItem evaluates the item rules that apply to the item and records
// their outcomes.
func (c *Collector) ObserveItem(it model.Item) {
	viol := violationsByRule(c.eng.EvalItem(it))

	c.mu.Lock()
	defer c.mu.Unlock()
	c.rotate()
	s := c.state(it.Service)
	if s == nil {
		return
	}
	for _, r := range c.eng.item {
		if targetMatches(r.rule.Target, it.Kind) {
			s.cur.get(r.rule.ID).record(viol[r.rule.ID], func() Evidence { return evidenceFor(it) })
		}
	}
}

// ObserveAggregates evaluates one complete set of aggregate rows, every
// tracker's current state, and replaces the previous evaluation's outcomes
// with it. Adding instead would count the same tracker state again on every
// export, so ratios would depend on the export interval, and a service whose
// aggregate recovered would keep its old violations.
func (c *Collector) ObserveAggregates(rows []AggregateRow) {
	viols := make([]map[string]*Violation, len(rows))
	for i, row := range rows {
		viols[i] = violationsByRule(c.eng.EvalAggregate(row))
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.rotate()
	for _, s := range c.svc {
		s.agg = outcomes{}
	}
	for i, row := range rows {
		s := c.state(row.Service)
		if s == nil {
			continue
		}
		for _, r := range c.eng.aggregate {
			if r.rule.Evaluation.Aggregate != row.Aggregate {
				continue
			}
			v := viols[i][r.rule.ID]
			s.agg.get(r.rule.ID).record(v, func() Evidence { return Evidence{Kind: "aggregate", Attrs: v.Fields} })
		}
	}
	c.prune()
}

// violationsByRule indexes one evaluation's violations by rule ID.
func violationsByRule(vs []Violation) map[string]*Violation {
	m := make(map[string]*Violation, len(vs))
	for i := range vs {
		m[vs[i].RuleID] = &vs[i]
	}
	return m
}

// RecordPollerResult stores a verified outcome, overriding sampled data at
// snapshot time. The service is added if the stream never saw it.
func (c *Collector) RecordPollerResult(pr PollerResult) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.rotate()
	if c.state(pr.Service) == nil {
		return
	}
	byRule, ok := c.poller[pr.Service]
	if !ok {
		byRule = map[string]polled{}
		c.poller[pr.Service] = byRule
	}
	byRule[pr.RuleID] = polled{PollerResult: pr, at: c.opts.Now()}
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
	// RejectedObservations counts evaluations dropped because their service
	// arrived when the collector already tracked its maximum of services.
	RejectedObservations int64 `json:"rejected_observations,omitempty"`
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

// Snapshot computes per-service rule results, scores, and findings. The
// slices are never nil, so an empty fleet encodes as [] rather than null.
func (c *Collector) Snapshot() *Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.rotate()

	snap := &Snapshot{
		Services:             []ServiceReport{},
		RulesEvaluated:       make([]string, 0, len(c.eng.all)),
		RejectedObservations: c.rejected,
	}
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

// serviceReport judges every rule for one service. A rule the service's
// telemetry never reached is left out, unless a poller verified it.
func (c *Collector) serviceReport(name string) ServiceReport {
	s := c.svc[name]
	rep := ServiceReport{ServiceName: name}
	var specResults, extResults []RuleResult

	for _, r := range c.eng.all {
		t := s.tallyFor(r)
		verified, hasVerified := c.poller[name][r.ID]
		if t.observed == 0 && !hasVerified {
			continue
		}

		ratio := 0.0
		if t.observed > 0 {
			ratio = float64(t.violations) / float64(t.observed)
		}
		// A service passes while its violation ratio stays at or below the
		// rule's threshold (default 0: any violation fails).
		passed := ratio <= r.ServiceViolation.ThresholdRatio
		confidence := ConfidenceSampled
		details := map[string]any{}
		if t.evalErrs > 0 {
			details["eval_errors"] = t.evalErrs
		}
		if hasVerified {
			passed = verified.Passed
			confidence = ConfidenceVerified
			if verified.Details != nil {
				details["poller"] = verified.Details
			}
		}

		res := RuleResult{RuleID: r.ID, Impact: r.Impact, Passed: passed, EvalErrors: t.evalErrs}
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
				Stats:    Stats{Observed: t.observed, Violations: t.violations, Ratio: ratio},
				Evidence: t.evidence, Details: details,
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

// tallyFor is what the report counts for one rule: the latest aggregate
// evaluation for an aggregate rule; both windows together for an item rule,
// with this window's evidence first.
func (s *svcState) tallyFor(r *Rule) tally {
	if r.Evaluation.Mode != ModeItem {
		if t := s.agg[r.ID]; t != nil {
			return *t
		}
		return tally{}
	}
	var sum tally
	for _, window := range []outcomes{s.cur, s.prev} {
		t := window[r.ID]
		if t == nil {
			continue
		}
		sum.observed += t.observed
		sum.violations += t.violations
		sum.evalErrs += t.evalErrs
		for _, e := range t.evidence {
			if len(sum.evidence) < MaxEvidence {
				sum.evidence = append(sum.evidence, e)
			}
		}
	}
	return sum
}

// maxEvidenceAttrs caps the attributes one evidence sample keeps.
const maxEvidenceAttrs = 32

// evidenceFor turns an item into an evidence sample. Evidence outlives the
// item, so everything in it is bounded and copied: the summary, and at most
// maxEvidenceAttrs attributes, keys and string values cut to
// model.MaxAttrValueLen.
func evidenceFor(it model.Item) Evidence {
	e := Evidence{Kind: it.Kind.String()}
	switch {
	case it.Span != nil:
		e.Summary = "span " + it.Span.Name
		e.Attrs = boundedAttrs(it.Resource)
	case it.Metric != nil:
		e.Summary = "metric " + it.Metric.Name
		e.Attrs = boundedAttrs(it.Metric.Attrs)
	case it.Log != nil:
		e.Summary = "log severity=" + it.Log.SeverityText
		e.Attrs = boundedAttrs(it.Resource)
	}
	e.Summary = model.Truncate(e.Summary, model.MaxAttrValueLen)
	return e
}

// boundedAttrs copies at most maxEvidenceAttrs attributes, the first by key
// order so the choice is stable, with keys and string values truncated.
func boundedAttrs(attrs map[string]any) map[string]any {
	if attrs == nil {
		return nil
	}
	keys := make([]string, 0, len(attrs))
	for k := range attrs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if len(keys) > maxEvidenceAttrs {
		keys = keys[:maxEvidenceAttrs]
	}
	out := make(map[string]any, len(keys))
	for _, k := range keys {
		v := attrs[k]
		if s, ok := v.(string); ok {
			v = model.Truncate(s, model.MaxAttrValueLen)
		}
		out[model.Truncate(k, model.MaxAttrValueLen)] = v
	}
	return out
}
