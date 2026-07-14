package report

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tamen25/Argus/engine/internal/rules"
)

var update = flag.Bool("update", false, "rewrite golden files")

func sampleReport() *Report {
	ext := 0.0
	return &Report{
		GeneratedAt:     time.Date(2026, 7, 12, 3, 0, 0, 0, time.UTC),
		ArgusVersion:    "v0.1.0-test",
		SpecVersion:     "e6ee22274284",
		Window:          "60s",
		RuleSetComplete: false,
		Notes:           []string{"cardinality tracker overflowed 3 pairs"},
		Snapshot: &rules.Snapshot{
			FleetScore: 50,
			Services: []rules.ServiceReport{
				{ServiceName: "checkout", SpecScore: 100, Category: "Excellent",
					Results: []rules.RuleResult{{RuleID: "RES-005", Impact: rules.ImpactCritical, Passed: true}}},
				{ServiceName: "unknown_service:java", SpecScore: 0, Category: "Poor", ExtensionScore: &ext,
					Results: []rules.RuleResult{{RuleID: "RES-005", Impact: rules.ImpactCritical, Passed: false}},
					Findings: []rules.Finding{{
						RuleID: "RES-005", RuleName: "service.name is present", Source: "spec",
						Service: "unknown_service:java", Impact: rules.ImpactCritical,
						Description: "Resource attributes MUST contain a non-empty service.name.",
						Confidence:  rules.ConfidenceSampled,
						Stats:       rules.Stats{Observed: 4, Violations: 4, Ratio: 1},
						Evidence:    []rules.Evidence{{Kind: "span", Summary: "span unnamed"}},
					}},
				},
			},
			RulesEvaluated: []string{"MET-001", "RES-005"},
		},
	}
}

func TestJSONRoundTripsAndDisclosure(t *testing.T) {
	b, err := JSON(sampleReport())
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if got["spec_version"] != "e6ee22274284" {
		t.Errorf("spec_version = %v", got["spec_version"])
	}
	if got["rule_set_complete"] != false {
		t.Error("rule_set_complete must be present and false (spec disclosure)")
	}
}

func twoServiceSameRuleReport() *Report {
	f := func(svc string) rules.Finding {
		return rules.Finding{
			RuleID: "MET-002", RuleName: "metrics have a unit", Source: "spec",
			Service: svc, Impact: rules.ImpactImportant,
			Description: "Metrics SHOULD declare a unit.",
			Confidence:  rules.ConfidenceSampled,
			Stats:       rules.Stats{Observed: 10, Violations: 10, Ratio: 1},
		}
	}
	return &Report{
		GeneratedAt:  time.Date(2026, 7, 14, 3, 0, 0, 0, time.UTC),
		ArgusVersion: "v0.1.0-test", SpecVersion: "e6ee22274284", Window: "60s",
		Snapshot: &rules.Snapshot{
			FleetScore: 70,
			Services: []rules.ServiceReport{
				{ServiceName: "ad", SpecScore: 70, Category: "Needs Improvement", Findings: []rules.Finding{f("ad")}},
				{ServiceName: "cart", SpecScore: 70, Category: "Needs Improvement", Findings: []rules.Finding{f("cart")}},
			},
			RulesEvaluated: []string{"MET-002"},
		},
	}
}

// One noisy rule must read as one section with a count, not N repeated
// sections (report-UX: MET-002 fired on 13/18 live services).
func TestMarkdownGroupsFindingsByRule(t *testing.T) {
	md := Markdown(twoServiceSameRuleReport())
	if got := strings.Count(md, "metrics have a unit (`MET-002`)"); got != 1 {
		t.Errorf("rule heading appears %d times, want 1 (grouped)\n%s", got, md)
	}
	if !strings.Contains(md, "— 2 services") {
		t.Errorf("grouped heading missing service count\n%s", md)
	}
	for _, svc := range []string{"ad", "cart"} {
		if !strings.Contains(md, "**"+svc+"**") {
			t.Errorf("per-service line for %s missing\n%s", svc, md)
		}
	}
}

func TestJSONFindingCounts(t *testing.T) {
	b, err := JSON(twoServiceSameRuleReport())
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		FindingCounts map[string]int `json:"finding_counts"`
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got.FindingCounts["MET-002"] != 2 {
		t.Errorf("finding_counts = %v, want MET-002:2", got.FindingCounts)
	}
}

func TestMarkdownGolden(t *testing.T) {
	md := Markdown(sampleReport())

	// invariants regardless of golden state
	for _, want := range []string{
		"RES-005", "unknown_service:java", "sampled",
		"does not yet implement the full rule set", // spec-mandated disclosure
		"Instrumentation Score spec e6ee22274284",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown missing %q", want)
		}
	}

	golden := filepath.Join("testdata", "report.golden.md")
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, []byte(md), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("missing golden (run -update): %v", err)
	}
	if md != string(want) {
		t.Errorf("markdown drifted from golden\n--- got ---\n%s", md)
	}
}
