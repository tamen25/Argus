package bench

import (
	"fmt"
	"slices"
	"sort"
	"strings"
)

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
	// Evidence is the telemetry the agent cites for its conclusion. A scenario
	// may require it (ScoringSpec.RequireEvidence), which is what separates a
	// diagnosis from a guess that happened to name the right workload.
	Evidence []Evidence `json:"evidence,omitempty"`
	// Summary is the agent's human-readable rationale. Recorded, never scored.
	Summary string `json:"summary,omitempty"`
	// Confidence is the agent's self-reported confidence in [0,1]. Recorded,
	// never scored — an agent cannot grade its own answer.
	Confidence float64 `json:"confidence,omitempty"`
}

// Evidence is one piece of telemetry an agent cites in support of its
// diagnosis. The scorer checks that evidence is present and well-formed, not
// that its claims are true: verifying an observation would mean re-running the
// agent's queries, which the deterministic core deliberately does not do. What
// it buys is that an agent must at least have looked, and that a human reading
// the report can check the citation by hand.
type Evidence struct {
	// Signal is the telemetry type consulted: metrics, logs, traces, alerts or
	// topology — the read-only MCP surface's vocabulary.
	Signal string `json:"signal"`
	// Query is the query or tool call that produced the observation.
	Query string `json:"query,omitempty"`
	// Observation is what the agent saw, in its own words.
	Observation string `json:"observation"`
}

// ValidSignals are the telemetry kinds evidence may cite, matching the MCP tool
// surface. A citation naming something else is malformed.
var ValidSignals = []string{"metrics", "logs", "traces", "alerts", "topology"}

// validate checks one evidence item is well-formed.
func (e Evidence) validate(i int) error {
	if strings.TrimSpace(e.Observation) == "" {
		return fmt.Errorf("diagnosis: evidence[%d]: observation is empty", i)
	}
	sig := strings.ToLower(strings.TrimSpace(e.Signal))
	if !slices.Contains(ValidSignals, sig) {
		return fmt.Errorf("diagnosis: evidence[%d]: signal %q, want one of %s",
			i, e.Signal, strings.Join(ValidSignals, ", "))
	}
	return nil
}

// Validate enforces the same constraints as schema/diagnosis.json in Go, so a
// malformed diagnosis is rejected before it reaches scoring rather than scoring
// as a silent zero. It mirrors the schema's required fields and bounds.
func (d Diagnosis) Validate() error {
	if strings.TrimSpace(d.Scenario) == "" {
		return fmt.Errorf("diagnosis: scenario is empty")
	}
	if strings.TrimSpace(d.Category) == "" {
		return fmt.Errorf("diagnosis: category is empty")
	}
	if len(d.RootCauseEntities) == 0 {
		return fmt.Errorf("diagnosis: root_cause_entities is empty")
	}
	for i, e := range d.RootCauseEntities {
		if strings.TrimSpace(e.Kind) == "" || strings.TrimSpace(e.Name) == "" {
			return fmt.Errorf("diagnosis: root_cause_entities[%d]: kind and name are required", i)
		}
	}
	if d.Confidence < 0 || d.Confidence > 1 {
		return fmt.Errorf("diagnosis: confidence %v out of [0,1]", d.Confidence)
	}
	// Evidence is optional at the schema level — a scenario decides whether it is
	// mandatory (ScoringSpec.RequireEvidence) — but anything supplied must be
	// well-formed, so a malformed citation cannot pass as a real one.
	for i, e := range d.Evidence {
		if err := e.validate(i); err != nil {
			return err
		}
	}
	return nil
}

// CitedSignals returns the distinct, normalized signals the diagnosis cites,
// sorted. Used by scoring to reward breadth of investigation.
func (d Diagnosis) CitedSignals() []string {
	seen := map[string]bool{}
	for _, e := range d.Evidence {
		sig := strings.ToLower(strings.TrimSpace(e.Signal))
		if sig != "" {
			seen[sig] = true
		}
	}
	out := make([]string, 0, len(seen))
	for s := range seen {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
