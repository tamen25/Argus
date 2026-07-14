package remediate

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tamen25/Argus/engine/internal/rules"
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

// The five committed templates (master plan rules 1, 2, 4, 5, 7) render
// against goldens — the review artifact for patch quality.
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
