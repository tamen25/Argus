package llm

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite golden files")

func sampleInput() ExplainInput {
	return ExplainInput{
		RuleID:      "MET-001",
		RuleName:    "metric attributes have bound cardinality",
		Service:     "checkout",
		Impact:      "important",
		Description: "Attribute keys on metrics must have fewer than 10,000 unique values.",
		Patch:       "// alloy.river\notelcol.processor.attributes \"drop_high_card\" {\n  action { key = \"request_id\" action = \"delete\" }\n}",
		Evidence:    []Evidence{{Attrs: map[string]any{"metric": "<redacted>", "attribute": "<redacted>", "cardinality": "<redacted>"}}},
	}
}

// The prompt template renders deterministically; a golden file guards the exact
// text sent to the model (model output itself is never tested).
func TestRenderPromptGolden(t *testing.T) {
	got, err := RenderPrompt(sampleInput())
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{"MET-001", "checkout", "attribute values redacted", "otelcol.processor.attributes"} {
		if !strings.Contains(got, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
	// no raw telemetry values leak into the prompt
	if strings.Contains(got, "50000") || strings.Contains(got, "user_id") {
		t.Errorf("prompt appears to contain un-redacted values:\n%s", got)
	}

	golden := filepath.Join("testdata", "explain.golden.txt")
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("read golden (run -update first): %v", err)
	}
	if got != string(want) {
		t.Errorf("prompt != golden; run -update to inspect")
	}
}

// The redaction boundary composes with rendering: a raw finding, redacted then
// rendered, must not leak values.
func TestRedactThenRenderLeaksNothing(t *testing.T) {
	raw := ExplainInput{
		RuleID:   "MET-001",
		Service:  "checkout",
		Patch:    "// patch",
		Evidence: []Evidence{{Summary: "user_id has 50000 values", Attrs: map[string]any{"attribute": "user_id", "cardinality": 50000}}},
	}
	got, err := RenderPrompt(Redact(raw))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "user_id") || strings.Contains(got, "50000") {
		t.Errorf("redact→render leaked a value:\n%s", got)
	}
}
