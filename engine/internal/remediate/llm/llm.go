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

// ExplainInput is everything the LLM sees about a finding. Structural fields
// (rule id/name, service, impact, description) are Argus's own text; only the
// evidence carries user telemetry, and Redact scrubs it.
type ExplainInput struct {
	RuleID      string
	RuleName    string
	Service     string
	Impact      string
	Description string
	// Patch is the deterministic remediation Argus already generated; the LLM
	// explains it, never replaces it.
	Patch    string
	Evidence []Evidence
}

// Client turns a finding into a human-readable explanation. Implementations
// live only in this package (openai.go, and the Anthropic-native client).
type Client interface {
	Explain(ctx context.Context, in ExplainInput) (string, error)
}

// Redact returns a deep copy of in with every evidence attribute value
// replaced by redactedPlaceholder and every free-text summary dropped. The
// caller's input is never mutated. Keys are preserved so the model still sees
// the shape (which metric, which attribute) without the values.
func Redact(in ExplainInput) ExplainInput {
	out := in // structural fields copy by value
	out.Evidence = make([]Evidence, len(in.Evidence))
	for i, ev := range in.Evidence {
		attrs := make(map[string]any, len(ev.Attrs))
		for k := range ev.Attrs {
			attrs[k] = redactedPlaceholder
		}
		out.Evidence[i] = Evidence{Summary: "", Attrs: attrs}
	}
	return out
}
