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

// The flagship's "remediated" condition is Argus's own fix, not a hand-written
// stand-in: scenarios/conditions/remediated.alloy must be exactly what this
// template renders, with only its placeholder filled in. If the template
// changes, the condition has to be regenerated, and the experiment measures
// the new output.
func TestRemediatedConditionIsTheRenderedTemplate(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "scenarios", "conditions", "remediated.alloy"))
	if err != nil {
		t.Fatal(err)
	}
	const marker = "// --- rendered by argus remediate ---\n"
	_, body, ok := strings.Cut(string(raw), marker)
	if !ok {
		t.Fatalf("remediated.alloy has no %q line separating its header from the rendered patch", strings.TrimSpace(marker))
	}
	out, err := Render("missing-resource-attributes", Context{
		Service: "condition", Finding: rules.Finding{RuleID: "ARG-RES-002", Service: "condition"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := strings.ReplaceAll(out["alloy.river"], "REPLACE_WITH_ENVIRONMENT", "kind-dev")
	if body != want {
		t.Errorf("remediated.alloy differs from the rendered missing-resource-attributes template.\n"+
			"Regenerate it from the template.\n--- file ---\n%s\n--- template ---\n%s", body, want)
	}
}

// Telemetry chooses metric names, attribute keys and service names. None of
// them may change the shape of a patch: a quote, a backtick or a newline in a
// name would otherwise end an OTTL string, an Alloy raw string or a comment,
// and write configuration the reviewer did not ask for.
func TestRenderHostileNames(t *testing.T) {
	hostile := Context{
		Service: "svc\nexporters: {evil: {}}",
		Finding: rules.Finding{
			RuleID: "MET-001",
			Evidence: []rules.Evidence{{Kind: "aggregate", Attrs: map[string]any{
				"metric":      "m\") or true or (\"",
				"attribute":   "user_id`\n  zz_marker = true",
				"cardinality": int64(48000), // as the engine hands it over, not float64
			}}},
		},
	}
	for name := range Available() {
		safe, err := Render(name, metCtx())
		if err != nil {
			t.Fatal(err)
		}
		got, err := Render(name, hostile)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for format, out := range got {
			if strings.Contains(out, "zz_marker") && !strings.Contains(out, `\n  zz_marker`) {
				t.Errorf("%s %s: a value's newline started a line:\n%s", name, format, out)
			}
			if strings.Contains(out, "\nexporters: {evil") {
				t.Errorf("%s %s: the service name wrote configuration:\n%s", name, format, out)
			}
			if strings.Contains(out, "or true") && !strings.Contains(out, `\") or true`) {
				t.Errorf("%s %s: the metric name was written into a statement:\n%s", name, format, out)
			}
			// Same number of lines as a clean render: no value added one.
			if strings.Count(out, "\n") != strings.Count(safe[format], "\n") {
				t.Errorf("%s %s: %d lines, want %d as for clean names", name, format,
					strings.Count(out, "\n"), strings.Count(safe[format], "\n"))
			}
		}
		var doc any
		if err := yaml.Unmarshal([]byte(got["collector.yaml"]), &doc); err != nil {
			t.Errorf("%s: collector.yaml does not parse: %v", name, err)
		}
	}

	got, _ := Render("high-cardinality-attribute", hostile)
	for _, want := range []string{placeholderMetric, placeholderAttribute, placeholderService, "~48000 distinct"} {
		if !strings.Contains(got["collector.yaml"], want) {
			t.Errorf("high-cardinality patch missing %q:\n%s", want, got["collector.yaml"])
		}
	}
}

// A service with no real name cannot be selected by name: the scope is left
// for the reviewer to fill in rather than guessed.
func TestRenderUnknownServiceIsNotScopedByName(t *testing.T) {
	ctx := metCtx()
	ctx.Service = "unknown_service:java"
	got, err := Render("high-cardinality-attribute", ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got["collector.yaml"], `resource.attributes["service.name"] == "`+placeholderService+`"`) {
		t.Errorf("want the scope left as a placeholder:\n%s", got["collector.yaml"])
	}
}
