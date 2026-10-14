package rules

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/tamen25/Argus/engine/internal/model"
)

func spanItem(svc string, res map[string]any) model.Item {
	return model.Item{Kind: model.KindSpan, Service: svc, Resource: res, Span: &model.Span{Name: "op"}}
}

func TestCollectorRollupAndFindings(t *testing.T) {
	eng := mustEngine(t, validRule)
	c := NewCollector(eng, CollectorOptions{})

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
	c := NewCollector(eng, CollectorOptions{})
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
	c := NewCollector(eng, CollectorOptions{})
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
	c := NewCollector(eng, CollectorOptions{})

	c.ObserveItem(spanItem("ad", map[string]any{"service.name": "ad"}))
	c.ObserveAggregates([]AggregateRow{{Service: "ad", Aggregate: "metric_attribute_cardinality",
		Fields: map[string]any{"metric": "m", "attribute": "user_id", "cardinality": 50000}}})

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

const aggRule = `
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

func cardinalityRow(service string, cardinality int) AggregateRow {
	return AggregateRow{Service: service, Aggregate: "metric_attribute_cardinality",
		Fields: map[string]any{"metric": "m", "attribute": "user_id", "cardinality": cardinality}}
}

// Each export evaluates the trackers' current state, and replaces the last
// evaluation. Re-reporting the same violating row every tick used to count it
// again each time, so a serve loop ran up 1000/1000 (and, before evidence was
// capped, grew without bound: the soak caught RSS +38%).
func TestCollectorAggregatesReplacePerExport(t *testing.T) {
	c := NewCollector(mustEngine(t, aggRule), CollectorOptions{})
	for i := 0; i < 1000; i++ {
		c.ObserveAggregates([]AggregateRow{cardinalityRow("ad", 40000+i)})
	}
	f := c.Snapshot().Service("ad").Findings[0]
	if f.Stats.Observed != 1 || f.Stats.Violations != 1 {
		t.Errorf("stats = %+v, want the latest evaluation only (1/1)", f.Stats)
	}
	if len(f.Evidence) != 1 || f.Evidence[0].Attrs["cardinality"] != 40999 {
		t.Errorf("evidence = %+v, want the latest row", f.Evidence)
	}
}

// R04: a service whose aggregate recovers passes at the next export, and one
// whose aggregate disappears is no longer reported on it.
func TestCollectorAggregateRecovery(t *testing.T) {
	c := NewCollector(mustEngine(t, aggRule), CollectorOptions{})
	c.ObserveAggregates([]AggregateRow{cardinalityRow("ad", 50000), cardinalityRow("cart", 50000)})
	c.ObserveAggregates([]AggregateRow{cardinalityRow("ad", 20)})

	snap := c.Snapshot()
	if ad := snap.Service("ad"); ad == nil || len(ad.Findings) != 0 || ad.SpecScore != 100 {
		t.Errorf("ad = %+v, want passing once its cardinality recovered", ad)
	}
	if snap.Service("cart") != nil {
		t.Errorf("cart is still reported with no aggregate left: %+v", snap.Service("cart"))
	}
}

// R04: item outcomes count over the window, the current and the previous
// one, so a violation stops failing a service two windows later, and a
// service that went quiet is dropped.
func TestCollectorItemOutcomesExpireWithTheWindow(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	c := NewCollector(mustEngine(t, validRule), CollectorOptions{
		Window: time.Hour, Now: func() time.Time { return now },
	})
	c.ObserveItem(spanItem("bad", map[string]any{})) // no service.name: a violation
	c.ObserveItem(spanItem("quiet", map[string]any{"service.name": "quiet"}))

	now = now.Add(61 * time.Minute) // the violation is in the previous window
	c.ObserveItem(spanItem("bad", map[string]any{"service.name": "bad"}))
	if f := c.Snapshot().Service("bad").Findings; len(f) != 1 {
		t.Errorf("one window on: findings = %+v, want the violation still counted", f)
	}

	now = now.Add(61 * time.Minute) // two windows on: it has aged out
	c.ObserveItem(spanItem("bad", map[string]any{"service.name": "bad"}))
	snap := c.Snapshot()
	if f := snap.Service("bad").Findings; len(f) != 0 {
		t.Errorf("two windows on: findings = %+v, want none", f)
	}
	if snap.Service("quiet") != nil {
		t.Error("a service with nothing observed for two windows is still reported")
	}
}

// R03: a rule that fails to evaluate has not passed. The error is counted as
// a violation, and shows on the result.
func TestCollectorEvalErrorFailsTheRule(t *testing.T) {
	reads := `
schema: argus.rules/v1
id: ARG-TEST-001
source: argus
name: reads an attribute without checking for it
description: test
target: span
impact: important
evaluation:
  mode: item
  criteria: "span.attrs['http.route'] != ''"
`
	c := NewCollector(mustEngine(t, reads), CollectorOptions{})
	c.ObserveItem(spanItem("ad", map[string]any{"service.name": "ad"}))

	rep := c.Snapshot().Service("ad")
	if rep.ExtensionScore == nil || *rep.ExtensionScore != 0 {
		t.Errorf("extension score = %v, want 0: the only rule could not be evaluated", rep.ExtensionScore)
	}
	if len(rep.Results) != 1 || rep.Results[0].EvalErrors != 1 {
		t.Errorf("results = %+v, want one eval error reported", rep.Results)
	}
	if len(rep.Findings) != 1 || rep.Findings[0].Details["eval_errors"] != int64(1) {
		t.Errorf("findings = %+v, want the eval error in the finding", rep.Findings)
	}
}

// R02: past MaxServices, a new service is turned away and counted; known
// services keep being observed.
func TestCollectorCapsServices(t *testing.T) {
	c := NewCollector(mustEngine(t, validRule), CollectorOptions{MaxServices: 2})
	for _, svc := range []string{"a", "b", "c", "d", "a"} {
		c.ObserveItem(spanItem(svc, map[string]any{"service.name": svc}))
	}
	snap := c.Snapshot()
	if len(snap.Services) != 2 || snap.Service("a") == nil || snap.Service("b") == nil {
		t.Errorf("services = %d, want a and b only", len(snap.Services))
	}
	if snap.RejectedObservations != 2 {
		t.Errorf("rejected = %d, want 2 (c and d)", snap.RejectedObservations)
	}
}

// R31: an empty fleet encodes its lists as [], never null; the plugin reads
// them as arrays.
func TestSnapshotEmptyFleetEncodesArrays(t *testing.T) {
	b, err := json.Marshal(NewCollector(mustEngine(t, validRule), CollectorOptions{}).Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"services":[]`) {
		t.Errorf("snapshot = %s, want services as []", b)
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
	c := NewCollector(eng, CollectorOptions{})
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
	c := NewCollector(eng, CollectorOptions{})
	c.ObserveItem(spanItem("a", map[string]any{"service.name": "a"}))
	c.ObserveItem(spanItem(model.UnknownService, map[string]any{}))
	snap := c.Snapshot()
	if snap.FleetScore != 50 {
		t.Errorf("fleet = %v, want 50 (mean of 100 and 0)", snap.FleetScore)
	}
}
