package rules

import (
	"strings"
	"testing"
)

const validRule = `
schema: argus.rules/v1
id: RES-005
source: spec
name: service.name is present
description: Resource attributes MUST contain a non-empty service.name.
target: resource
impact: critical
evaluation:
  mode: item
  criteria: "'service.name' in resource && string(resource['service.name']) != ''"
`

func TestLoadValidRule(t *testing.T) {
	rs, err := LoadBytes([]byte(validRule))
	if err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}
	if len(rs) != 1 {
		t.Fatalf("rules = %d, want 1", len(rs))
	}
	r := rs[0]
	if r.ID != "RES-005" || r.Impact != ImpactCritical || r.Source != "spec" || r.Target != "resource" {
		t.Errorf("rule = %+v", r)
	}
}

func TestLoaderRejectsUnknownFields(t *testing.T) {
	_, err := LoadBytes([]byte(validRule + "\nbogus_field: 1\n"))
	if err == nil || !strings.Contains(err.Error(), "bogus_field") {
		t.Errorf("want unknown-field error, got %v", err)
	}
}

func TestLoaderRejectsUnknownSchemaVersion(t *testing.T) {
	bad := strings.Replace(validRule, "argus.rules/v1", "argus.rules/v9", 1)
	if _, err := LoadBytes([]byte(bad)); err == nil {
		t.Error("want schema version error, got nil")
	}
}

func TestLoaderRejectsInvalidCEL(t *testing.T) {
	bad := strings.Replace(validRule, "'service.name' in resource && string(resource['service.name']) != ''", "this is not CEL ((", 1)
	if _, err := LoadBytes([]byte(bad)); err == nil {
		t.Error("want CEL compile error, got nil")
	}
}

func TestLoaderRejectsMissingRequired(t *testing.T) {
	for _, drop := range []string{"id: RES-005", "impact: critical", "target: resource"} {
		bad := strings.Replace(validRule, drop, "", 1)
		if _, err := LoadBytes([]byte(bad)); err == nil {
			t.Errorf("want error when %q removed, got nil", drop)
		}
	}
}

func TestLoaderRejectsBadEnumValues(t *testing.T) {
	for old, bad := range map[string]string{
		"impact: critical": "impact: catastrophic",
		"target: resource": "target: everything",
		"mode: item":       "mode: vibes",
		"source: spec":     "source: somewhere",
	} {
		b := strings.Replace(validRule, old, bad, 1)
		if _, err := LoadBytes([]byte(b)); err == nil {
			t.Errorf("want enum error for %q, got nil", bad)
		}
	}
}

func TestLoadDirSeparatesSpecAndExtensionAndParams(t *testing.T) {
	agg := `
schema: argus.rules/v1
id: MET-001
source: spec
name: bounded metric attribute cardinality
description: Attribute keys on metrics MUST have < max_cardinality unique values per window.
target: metric
impact: important
evaluation:
  mode: aggregate
  aggregate: metric_attribute_cardinality
  criteria: "agg.cardinality < params.max_cardinality"
params:
  max_cardinality: 10000
`
	rs, err := LoadBytes([]byte(validRule), []byte(agg))
	if err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}
	if len(rs) != 2 {
		t.Fatalf("rules = %d, want 2", len(rs))
	}
	var met *Rule
	for _, r := range rs {
		if r.ID == "MET-001" {
			met = r
		}
	}
	if met == nil || met.Evaluation.Mode != ModeAggregate || met.Evaluation.Aggregate != "metric_attribute_cardinality" {
		t.Fatalf("MET-001 = %+v", met)
	}
	if met.Params["max_cardinality"] != 10000 {
		t.Errorf("params = %v", met.Params)
	}
}

func TestLoaderRejectsDuplicateIDs(t *testing.T) {
	if _, err := LoadBytes([]byte(validRule), []byte(validRule)); err == nil {
		t.Error("want duplicate-id error, got nil")
	}
}

func TestMergeOverridesByID(t *testing.T) {
	base, err := LoadBytes([]byte(validRule))
	if err != nil {
		t.Fatal(err)
	}
	override := strings.Replace(validRule, "impact: critical", "impact: low", 1)
	extra := `
schema: argus.rules/v1
id: CUSTOM-001
source: argus
name: custom rule
description: user-supplied
target: span
impact: low
evaluation:
  mode: item
  criteria: "span.name != ''"
`
	over, err := LoadBytes([]byte(override), []byte(extra))
	if err != nil {
		t.Fatal(err)
	}
	merged := Merge(base, over)
	if len(merged) != 2 {
		t.Fatalf("merged = %d rules, want 2", len(merged))
	}
	byID := map[string]*Rule{}
	for _, r := range merged {
		byID[r.ID] = r
	}
	if byID["RES-005"].Impact != ImpactLow {
		t.Errorf("override did not replace builtin: impact = %s", byID["RES-005"].Impact)
	}
	if byID["CUSTOM-001"] == nil {
		t.Error("extension rule missing after merge")
	}
}
