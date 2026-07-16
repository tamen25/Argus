package llm

import "testing"

// Redaction is the privacy backstop (architecture rule 8, llm.redact defaults
// on): attribute VALUES are stripped before anything reaches the LLM, keys are
// kept so the model still knows the shape of the telemetry.
func TestRedactStripsEvidenceValues(t *testing.T) {
	in := ExplainInput{
		RuleID:      "MET-001",
		Service:     "checkout",
		Description: "high cardinality",
		Evidence: []Evidence{
			{Summary: "user_id has 50000 values", Attrs: map[string]any{
				"metric":      "http.server.duration",
				"attribute":   "user_id",
				"cardinality": 50000,
			}},
		},
	}

	out := Redact(in)

	ev := out.Evidence[0]
	// keys survive, values are gone
	for k := range ev.Attrs {
		if _, ok := map[string]bool{"metric": true, "attribute": true, "cardinality": true}[k]; !ok {
			t.Errorf("unexpected key %q after redaction", k)
		}
	}
	for k, v := range ev.Attrs {
		if v != redactedPlaceholder {
			t.Errorf("attr %q value = %v, want %q (redacted)", k, v, redactedPlaceholder)
		}
	}
	// free-text summary can leak values → dropped entirely, not partially masked
	if ev.Summary != "" {
		t.Errorf("evidence summary = %q, want empty (free text may carry values)", ev.Summary)
	}
	// structural fields (rule id, service, description) are not user data — kept
	if out.RuleID != "MET-001" || out.Service != "checkout" || out.Description != "high cardinality" {
		t.Errorf("structural fields were altered: %+v", out)
	}
}

// Redact never mutates the caller's input (the deterministic finding must be
// unchanged for the non-LLM output paths).
func TestRedactDoesNotMutateInput(t *testing.T) {
	in := ExplainInput{Evidence: []Evidence{{Summary: "keep me", Attrs: map[string]any{"a": "secret"}}}}
	_ = Redact(in)
	if in.Evidence[0].Summary != "keep me" || in.Evidence[0].Attrs["a"] != "secret" {
		t.Errorf("Redact mutated the caller's input: %+v", in.Evidence[0])
	}
}
