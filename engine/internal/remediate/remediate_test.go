package remediate

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/tamen25/Argus/engine/internal/rules"
	"github.com/tamen25/Argus/engine/internal/rules/builtin"
)

var update = flag.Bool("update", false, "rewrite golden files")

func metCtx() Context {
	return Context{
		Service: "checkout",
		Finding: rules.Finding{
			RuleID: "MET-001", Service: "checkout",
			Evidence: []rules.Evidence{{Kind: "aggregate", Attrs: map[string]any{
				"metric": "http_requests_total", "attribute": "user_id", "cardinality": float64(48000),
			}}},
		},
	}
}

// Every template renders both output formats, deterministically, with the
// finding's context substituted.
func TestRenderHighCardinality(t *testing.T) {
	got, err := Render("high-cardinality-attribute", metCtx())
	if err != nil {
		t.Fatal(err)
	}
	for _, format := range []string{"alloy.river", "collector.yaml"} {
		out, ok := got[format]
		if !ok {
			t.Fatalf("missing %s output: %v", format, got)
		}
		for _, want := range []string{"http_requests_total", "user_id", "checkout"} {
			if !strings.Contains(out, want) {
				t.Errorf("%s missing %q\n%s", format, want, out)
			}
		}
	}
	// deterministic
	again, _ := Render("high-cardinality-attribute", metCtx())
	if got["alloy.river"] != again["alloy.river"] {
		t.Error("render not deterministic")
	}
}

func TestRenderUnknownTemplate(t *testing.T) {
	if _, err := Render("nope", Context{}); err == nil || !strings.Contains(err.Error(), "missing-service-name") {
		t.Errorf("want unknown-template error listing available templates, got %v", err)
	}
}

// Every template renders against a golden — the review artifact for patch
// quality. TestEveryTemplateHasAGolden keeps this list complete.
func TestRenderGoldens(t *testing.T) {
	cases := map[string]Context{
		"missing-service-name": {Service: "unknown_service:java", Finding: rules.Finding{
			RuleID: "RES-005", Service: "unknown_service:java",
		}},
		"high-cardinality-attribute": metCtx(),
		"logs-without-trace-context": {Service: "checkout", Finding: rules.Finding{
			RuleID: "ARG-LOG-001", Service: "checkout",
			Stats: rules.Stats{Observed: 100, Violations: 62, Ratio: 0.62},
		}},
		"unbounded-span-name": {Service: "frontend", Finding: rules.Finding{
			RuleID: "SPA-003", Service: "frontend",
			Evidence: []rules.Evidence{{Kind: "aggregate", Attrs: map[string]any{
				"attribute": "span.name", "cardinality": float64(1800),
			}}},
		}},
		"log-level-abuse": {Service: "cart", Finding: rules.Finding{
			RuleID: "LOG-001", Service: "cart",
		}},
		"missing-resource-attributes": {Service: "cart", Finding: rules.Finding{
			RuleID: "ARG-RES-002", Service: "cart",
		}},
		"missing-exemplars": {Service: "checkout", Finding: rules.Finding{
			RuleID: "ARG-MET-001", Service: "checkout",
		}},
		"broken-context-propagation": {Service: "payment", Finding: rules.Finding{
			RuleID: "SPA-004", Service: "payment",
			Stats: rules.Stats{Observed: 200, Violations: 30, Ratio: 0.15},
		}},
		"log-severity-unset": {Service: "product-reviews", Finding: rules.Finding{
			RuleID: "LOG-002", Service: "product-reviews",
			Stats: rules.Stats{Observed: 50, Violations: 50, Ratio: 1},
		}},
		"missing-metric-unit":       metCtx(),
		"unit-in-metric-name":       metCtx(),
		"histogram-bucket-mismatch": metCtx(),
	}
	for name, ctx := range cases {
		got, err := Render(name, ctx)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		for format, ext := range map[string]string{"alloy.river": "river", "collector.yaml": "yaml"} {
			golden := filepath.Join("testdata", "golden", name+"."+ext)
			if *update {
				if err := os.MkdirAll(filepath.Dir(golden), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(golden, []byte(got[format]), 0o644); err != nil {
					t.Fatal(err)
				}
				continue
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("%s: missing golden (run -update): %v", name, err)
			}
			if got[format] != string(want) {
				t.Errorf("%s %s drifted from golden\n--- got ---\n%s", name, format, got[format])
			}
		}
	}
}

// Read-only product: rendered output is a file a human applies; it must
// carry the do-not-auto-apply notice.
func TestRenderCarriesHumanReviewNotice(t *testing.T) {
	got, err := Render("missing-service-name", Context{Service: "svc", Finding: rules.Finding{RuleID: "RES-005"}})
	if err != nil {
		t.Fatal(err)
	}
	for format, out := range got {
		if !strings.Contains(out, "review before applying") {
			t.Errorf("%s missing human-review notice", format)
		}
	}
}

// A rule that names a template that does not exist fails at the moment a user
// asks for its fix: `argus remediate` errors and the plugin's remediation
// panel returns HTTP 500. Seven of the twelve templates the rules named were
// missing until 2026-10-01, covering 13 of the 18 rules, and nothing noticed.
func TestEveryRuleTemplateExists(t *testing.T) {
	rs, err := builtin.Load()
	if err != nil {
		t.Fatal(err)
	}
	have := Available()
	for _, r := range rs {
		if name := r.Remediation.Template; name != "" && !have[name] {
			t.Errorf("rule %s names remediation template %q, which does not exist", r.ID, name)
		}
	}
}

// Every template has both formats and a golden.
func TestEveryTemplateHasAGolden(t *testing.T) {
	for name := range Available() {
		for _, ext := range []string{"river", "yaml"} {
			if _, err := os.Stat(filepath.Join("templates", name+"."+ext+".tmpl")); err != nil {
				t.Errorf("template %s has no %s form", name, ext)
			}
			if _, err := os.Stat(filepath.Join("testdata", "golden", name+"."+ext)); err != nil {
				t.Errorf("template %s has no %s golden (add it to TestRenderGoldens)", name, ext)
			}
		}
	}
}

// The Collector form is YAML a human pastes into a config file: it must parse.
func TestCollectorFormIsValidYAML(t *testing.T) {
	for name := range Available() {
		got, err := Render(name, metCtx())
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var doc any
		if err := yaml.Unmarshal([]byte(got["collector.yaml"]), &doc); err != nil {
			t.Errorf("%s: collector.yaml does not parse: %v\n%s", name, err, got["collector.yaml"])
		}
	}
}
