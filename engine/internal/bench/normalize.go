package bench

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
)

// Normalizer maps an agent's raw output into a validated Diagnosis. Adapters
// that already emit the diagnosis JSON use JSONNormalizer (deterministic).
// Shell agents whose native format we don't control get a per-adapter
// normalizer; an LLM-judge normalizer lands with the agent-adapter slice and
// MUST report Method() = "llm-judge" so results disclose it (honest reporting,
// architecture rule 7). Scoring never sees raw agent text — only what a
// Normalizer produced.
type Normalizer interface {
	// Normalize converts raw agent output into a validated Diagnosis for the
	// given scenario. The scenario name is injected so the Diagnosis.Scenario
	// field is authoritative even if the agent omits or fabricates it.
	Normalize(ctx context.Context, raw []byte, scenario string) (Diagnosis, error)
	// Method names the normalization method for the run record (e.g. "json",
	// "llm-judge"). Deterministic methods are preferred; a non-deterministic
	// one must be visible in the report.
	Method() string
}

// JSONNormalizer is the deterministic normalizer: the raw output is the
// diagnosis JSON. It strictly decodes (unknown fields rejected), forces the
// scenario name, and validates. No model, no ambiguity.
type JSONNormalizer struct{}

// Method identifies this normalizer in run records.
func (JSONNormalizer) Method() string { return "json" }

// Normalize strictly decodes raw as a Diagnosis, overrides its Scenario with the
// authoritative name, and validates it.
func (JSONNormalizer) Normalize(_ context.Context, raw []byte, scenario string) (Diagnosis, error) {
	d, err := ParseDiagnosis(raw)
	if err != nil {
		return Diagnosis{}, err
	}
	d.Scenario = scenario
	if err := d.Validate(); err != nil {
		return Diagnosis{}, err
	}
	return d, nil
}

// ParseDiagnosis strictly decodes a Diagnosis from JSON: unknown fields and
// trailing data are errors. It does not force the scenario name or validate —
// callers (normalizers) do. Kept separate so adapters can parse without
// committing to a scenario binding.
func ParseDiagnosis(raw []byte) (Diagnosis, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var d Diagnosis
	if err := dec.Decode(&d); err != nil {
		return Diagnosis{}, fmt.Errorf("parsing diagnosis: %w", err)
	}
	if dec.More() {
		return Diagnosis{}, fmt.Errorf("parsing diagnosis: trailing data after JSON object")
	}
	return d, nil
}

var _ Normalizer = JSONNormalizer{}
