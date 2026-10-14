// Package llm is the LLM edge (architecture rule 2: deterministic core, LLM at
// the edge). It is the ONLY package permitted to talk to an LLM — depguard
// forbids rules/, cost/, backtest/, and bench/scoring from importing it, so
// LLM output can never influence a score, price, or backtest verdict.
//
// The LLM does exactly two things here: explain a finding and its (already
// deterministic) remediation patch in prose, and draft remediation text for
// finding types that have no template. Its output is never auto-applied.
//
// Redaction (rule 8) defaults on: attribute values are stripped before any
// call — see Redact.
package llm

import "context"

// redactedPlaceholder replaces attribute values when redaction is on.
const redactedPlaceholder = "<redacted>"

// Evidence is a truncated observation attached to a finding. Attrs values are
// telemetry-derived and are redacted before reaching the LLM.
type Evidence struct {
	Summary string
	Attrs   map[string]any
}

// ExplainInput is everything the LLM sees about a finding. The rule's own
// fields (ID, name, impact, description) are Argus's text. The service name,
// the evidence, and the patch (which names the service, metric and
// attribute) come from user telemetry, and Redact replaces them.
type ExplainInput struct {
	RuleID      string
	RuleName    string
	Service     string
	Impact      string
	Description string
	// Patch is the deterministic remediation Argus already generated; the LLM
	// explains it, never replaces it.
	Patch string
	// RedactedPatch is the same patch rendered from the finding with its
	// service and evidence values replaced, so it holds no telemetry value.
	// Redact sends it in place of Patch; empty, no patch is sent.
	RedactedPatch string
	Evidence      []Evidence
	// Redacted records that Redact ran, so the prompt says so.
	Redacted bool
}

// Client turns a finding into a human-readable explanation. The one
// implementation is the OpenAI-compatible client in openai.go.
type Client interface {
	Explain(ctx context.Context, in ExplainInput) (string, error)
}

// Redact returns a copy of in with nothing from user telemetry left in it:
// the service name becomes redactedPlaceholder, every evidence attribute
// value too, free-text summaries are dropped, and the patch is the one
// rendered without telemetry values (RedactedPatch). Evidence keys are kept,
// so the model still sees the shape (a metric, an attribute) without the
// values. The caller's input is never mutated.
func Redact(in ExplainInput) ExplainInput {
	out := in // the rule's own fields copy by value
	out.Service = redactedPlaceholder
	out.Patch = in.RedactedPatch
	out.RedactedPatch = ""
	out.Redacted = true
	out.Evidence = make([]Evidence, len(in.Evidence))
	for i, ev := range in.Evidence {
		attrs := make(map[string]any, len(ev.Attrs))
		for k := range ev.Attrs {
			attrs[k] = redactedPlaceholder
		}
		out.Evidence[i] = Evidence{Attrs: attrs}
	}
	return out
}
