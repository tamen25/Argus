package rules

import (
	"testing"

	"github.com/tamen25/Argus/engine/internal/model"
)

func spanItem(svc string, res map[string]any) model.Item {
	return model.Item{Kind: model.KindSpan, Service: svc, Resource: res, Span: &model.Span{Name: "op"}}
}

func TestCollectorRollupAndFindings(t *testing.T) {
	eng := mustEngine(t, validRule)
	c := NewCollector(eng)

	good := spanItem("checkout", map[string]any{"service.name": "checkout"})
	bad := spanItem(model.UnknownService, map[string]any{})
	for i := 0; i < 8; i++ {
		c.ObserveItem(good)
	}
	for i := 0; i < 2; i++ {
		c.ObserveItem(bad)
	}

	snap := c.Snapshot()
	chk := snap.Service("checkout")
	if chk == nil {
		t.Fatal("no checkout report")
	}
	if chk.SpecScore != 100 || len(chk.Findings) != 0 {
		t.Errorf("checkout: score=%v findings=%d, want 100/0", chk.SpecScore, len(chk.Findings))
	}

	unk := snap.Service(model.UnknownService)
	if unk == nil {
		t.Fatal("no unknown-service report")
	}
	if unk.SpecScore != 0 {
		t.Errorf("unknown: score=%v, want 0 (single critical rule failed)", unk.SpecScore)
	}
	if len(unk.Findings) != 1 {
		t.Fatalf("unknown findings = %d, want 1", len(unk.Findings))
	}
	f := unk.Findings[0]
	if f.RuleID != "RES-005" || f.Confidence != ConfidenceSampled || f.Stats.Observed != 2 || f.Stats.Violations != 2 {
		t.Errorf("finding = %+v", f)
	}
}

func TestCollectorEvidenceCappedAndTruncated(t *testing.T) {
	eng := mustEngine(t, validRule)
	c := NewCollector(eng)
	bad := spanItem(model.UnknownService, map[string]any{"k": "v"})
	for i := 0; i < 50; i++ {
		c.ObserveItem(bad)
	}
	f := c.Snapshot().Service(model.UnknownService).Findings[0]
	if len(f.Evidence) > MaxEvidence {
		t.Errorf("evidence = %d, want <= %d", len(f.Evidence), MaxEvidence)
	}
	if f.Stats.Violations != 50 {
		t.Errorf("violations = %d, want 50", f.Stats.Violations)
	}
}

func TestCollectorThresholdRatio(t *testing.T) {
	tolerant := `
schema: argus.rules/v1
id: LOG-TEST
source: argus
name: tolerant log rule
description: fails only when >50% of logs violate
target: log
impact: normal
evaluation:
  mode: item
  criteria: "log.severity_text != 'DEBUG'"
service_violation:
  threshold_ratio: 0.5
`
	eng := mustEngine(t, tolerant)
	c := NewCollector(eng)
	dbg := model.Item{Kind: model.KindLogRecord, Service: "s", Resource: map[string]any{},
		Log: &model.LogRecord{SeverityText: "DEBUG"}}
	info := model.Item{Kind: model.KindLogRecord, Service: "s", Resource: map[string]any{},
		Log: &model.LogRecord{SeverityText: "INFO"}}

	// 4/10 DEBUG = ratio 0.4 <= 0.5 -> rule passes, no finding
	for i := 0; i < 4; i++ {
		c.ObserveItem(dbg)
	}
	for i := 0; i < 6; i++ {
		c.ObserveItem(info)
	}
	rep := c.Snapshot().Service("s")
	if len(rep.Findings) != 0 {
		t.Errorf("findings = %v, want none at ratio 0.4", rep.Findings)
	}

	// push over 50%
	for i := 0; i < 5; i++ {
		c.ObserveItem(dbg)
	}
	rep = c.Snapshot().Service("s")
	if len(rep.Findings) != 1 {
		t.Errorf("findings = %d, want 1 at ratio 0.6", len(rep.Findings))
	}
}

func TestCollectorAggregateAndVerify(t *testing.T) {
	agg := `
schema: argus.rules/v1
id: MET-001
source: spec
name: bounded metric attribute cardinality
description: test
target: metric
impact: important
evaluation:
  mode: aggregate
  aggregate: metric_attribute_cardinality
  criteria: "agg.cardinality < params.max_cardinality"
params:
  max_cardinality: 10000
`
	eng := mustEngine(t, validRule, agg)
	c := NewCollector(eng)

	c.ObserveItem(spanItem("ad", map[string]any{"service.name": "ad"}))
	c.ObserveAggregate(AggregateRow{Service: "ad", Aggregate: "metric_attribute_cardinality",
		Fields: map[string]any{"metric": "m", "attribute": "user_id", "cardinality": 50000}})

	rep := c.Snapshot().Service("ad")
	// critical passed (weight 40), important failed (weight 30): 40/70
	want := 100 * 40.0 / 70.0
	if rep.SpecScore < want-0.01 || rep.SpecScore > want+0.01 {
		t.Errorf("score = %v, want %v", rep.SpecScore, want)
	}
	if len(rep.Findings) != 1 || rep.Findings[0].RuleID != "MET-001" {
		t.Fatalf("findings = %+v", rep.Findings)
	}

	// Poller verification flips confidence and can override the outcome.
	c.RecordPollerResult(PollerResult{Service: "ad", RuleID: "MET-001", Passed: false,
		Details: map[string]any{"source": "mimir cardinality API"}})
	rep = c.Snapshot().Service("ad")
	if rep.Findings[0].Confidence != ConfidenceVerified {
		t.Errorf("confidence = %s, want verified", rep.Findings[0].Confidence)
	}

	// A verified PASS removes the sampled finding entirely.
	c.RecordPollerResult(PollerResult{Service: "ad", RuleID: "MET-001", Passed: true})
	rep = c.Snapshot().Service("ad")
	if len(rep.Findings) != 0 {
		t.Errorf("findings = %+v, want none after verified pass", rep.Findings)
	}
	if rep.SpecScore != 100 {
		t.Errorf("score = %v, want 100 after verified pass", rep.SpecScore)
	}
}

// Bounded memory (architecture rule 3) holds for aggregate rules under a
// long-running serve loop: re-evaluating the same violating row every export
// tick must not grow retained state — the 2.5h soak caught RSS +38% from
// exactly this. Counts keep counting; evidence stays capped at MaxEvidence
// (latest rows win — a day-old cardinality estimate is not evidence).
func TestCollectorAggregateStateBoundedAcrossTicks(t *testing.T) {
	agg := `
schema: argus.rules/v1
id: MET-001
source: spec
name: bounded metric attribute cardinality
description: test
target: metric
impact: important
evaluation:
  mode: aggregate
  aggregate: metric_attribute_cardinality
  criteria: "agg.cardinality < params.max_cardinality"
params:
  max_cardinality: 10000
`
	eng := mustEngine(t, agg)
	c := NewCollector(eng)

	const ticks = 1000
	for i := 0; i < ticks; i++ {
		c.ObserveAggregate(AggregateRow{Service: "ad", Aggregate: "metric_attribute_cardinality",
			Fields: map[string]any{"metric": "m", "attribute": "user_id", "cardinality": 40000 + i}})
	}

	f := c.Snapshot().Service("ad").Findings[0]
	if f.Stats.Observed != ticks || f.Stats.Violations != ticks || f.Stats.Ratio != 1 {
		t.Errorf("stats = %+v, want %d/%d", f.Stats, ticks, ticks)
	}
	if len(f.Evidence) > MaxEvidence {
		t.Fatalf("evidence = %d entries, want <= %d", len(f.Evidence), MaxEvidence)
	}
	// latest evidence retained: the last tick's cardinality must be present
	found := false
	for _, e := range f.Evidence {
		if e.Attrs["cardinality"] == 40000+ticks-1 {
			found = true
		}
	}
	if !found {
		t.Errorf("evidence holds stale rows only: %+v", f.Evidence)
	}

	// white-box: retained state per rule is the counters + capped evidence
	if got := len(c.svc["ad"].aggEvidence["MET-001"]); got > MaxEvidence {
		t.Errorf("retained aggregate evidence = %d, want <= %d", got, MaxEvidence)
	}
}

func TestCollectorSeparatesSpecAndExtensionScores(t *testing.T) {
	ext := `
schema: argus.rules/v1
id: ARG-RES-001
source: argus
name: service.name is not the SDK default
description: unknown_service indicates a default, unconfigured SDK.
target: resource
impact: critical
evaluation:
  mode: item
  criteria: "!('service.name' in resource) || !string(resource['service.name']).startsWith('unknown_service')"
`
	eng := mustEngine(t, validRule, ext)
	c := NewCollector(eng)
	// service.name present (spec rule passes) but SDK-default (extension fails)
	c.ObserveItem(spanItem("unknown_service:java", map[string]any{"service.name": "unknown_service:java"}))

	rep := c.Snapshot().Service("unknown_service:java")
	if rep.SpecScore != 100 {
		t.Errorf("spec score = %v, want 100 (extension must not pollute spec score)", rep.SpecScore)
	}
	if rep.ExtensionScore == nil || *rep.ExtensionScore != 0 {
		t.Errorf("extension score = %v, want 0", rep.ExtensionScore)
	}
	if len(rep.Findings) != 1 || rep.Findings[0].RuleID != "ARG-RES-001" {
		t.Errorf("findings = %+v", rep.Findings)
	}
}

func TestSnapshotFleetScore(t *testing.T) {
	eng := mustEngine(t, validRule)
	c := NewCollector(eng)
	c.ObserveItem(spanItem("a", map[string]any{"service.name": "a"}))
	c.ObserveItem(spanItem(model.UnknownService, map[string]any{}))
	snap := c.Snapshot()
	if snap.FleetScore != 50 {
		t.Errorf("fleet = %v, want 50 (mean of 100 and 0)", snap.FleetScore)
	}
}
