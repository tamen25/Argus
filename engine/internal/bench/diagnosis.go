package bench

// Diagnosis is the structured answer every bench agent must return, regardless
// of adapter (OpenAI-compatible, Anthropic, or a shell agent normalized into
// this shape). It is the unit the deterministic scorer consumes — no agent's
// free-form prose reaches scoring. The JSON schema mirror lives at
// internal/bench/schema/diagnosis.json.
type Diagnosis struct {
	// Scenario is the scenario name this diagnosis answers.
	Scenario string `json:"scenario"`
	// RootCauseEntities is the agent's identified root-cause set, scored against
	// GroundTruth.RootCauseEntities.
	RootCauseEntities []Entity `json:"root_cause_entities"`
	// Category is the agent's fault classification (e.g. "cardinality-explosion").
	Category string `json:"category"`
	// Summary is the agent's human-readable rationale. Recorded, never scored.
	Summary string `json:"summary,omitempty"`
	// Confidence is the agent's self-reported confidence in [0,1]. Recorded,
	// never scored — an agent cannot grade its own answer.
	Confidence float64 `json:"confidence,omitempty"`
}
