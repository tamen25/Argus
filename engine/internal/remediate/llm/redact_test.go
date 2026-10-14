package llm

import (
	"strings"
	"testing"
)

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
	// the rule's own fields are Argus's text, kept; the service name is not
	if out.RuleID != "MET-001" || out.Description != "high cardinality" {
		t.Errorf("the rule's fields were altered: %+v", out)
	}
	if out.Service != redactedPlaceholder {
		t.Errorf("service = %q, want it redacted: service names come from telemetry", out.Service)
	}
}

// R07: the patch names the service, the metric and the attribute, so the one
// rendered from the real finding is never sent with redaction on; the
// telemetry-free rendering is.
func TestRedactSendsTheRedactedPatch(t *testing.T) {
	in := ExplainInput{
		Service:       "payments-eu",
		Patch:         `delete_key(attributes, "card_number") where resource.attributes["service.name"] == "payments-eu"`,
		RedactedPatch: `delete_key(attributes, "REPLACE_WITH_ATTRIBUTE_KEY") where resource.attributes["service.name"] == "REPLACE_WITH_SERVICE_NAME"`,
	}
	out := Redact(in)
	if out.Patch != in.RedactedPatch || out.RedactedPatch != "" {
		t.Errorf("patch = %q, want the redacted rendering only", out.Patch)
	}
	prompt, err := RenderPrompt(out)
	if err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{"payments-eu", "card_number"} {
		if strings.Contains(prompt, leak) {
			t.Errorf("prompt contains %q:\n%s", leak, prompt)
		}
	}

	// No redacted rendering: no patch is sent at all, and the prompt says so.
	prompt, _ = RenderPrompt(Redact(ExplainInput{Patch: in.Patch}))
	if strings.Contains(prompt, "card_number") || !strings.Contains(prompt, "patch is not shown") {
		t.Errorf("without a redacted patch the prompt is:\n%s", prompt)
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
