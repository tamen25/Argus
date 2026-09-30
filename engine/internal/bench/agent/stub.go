package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// StubAgent is a calibration instrument, not a bench subject. It returns a fixed
// answer without calling a model, so a scenario's scoring rubric can be probed
// for discrimination before real agents are pointed at it: if a canned
// non-diagnosis scores well, the rubric is too loose and every result it later
// produces is worthless.
//
// It reads no telemetry and makes no network calls, which is the point — its
// score is a pure property of the rubric.
type StubAgent struct {
	profile StubProfile
	// entities is the answer for profiles that name entities.
	entities []stubEntity
	category string
	summary  string
	evidence []stubEvidence
}

// StubProfile selects which failure mode of a weak agent to imitate.
type StubProfile string

const (
	// StubVague answers in prose and names no entity at all — the "elevated
	// latency in the checkout path" non-diagnosis. Probes whether a non-answer
	// can score.
	StubVague StubProfile = "vague"
	// StubObvious names the single most conspicuous workload in the environment
	// with a generic category, as a lazy agent would from the scenario's own
	// name or from whatever is busiest. Probes whether an evidence-free guess is
	// distinguishable from a real diagnosis.
	StubObvious StubProfile = "obvious"
	// StubShotgun names many plausible entities hoping one lands. Probes whether
	// the rubric penalizes over-broad answers.
	StubShotgun StubProfile = "shotgun"
	// StubCited is the strongest cheat the rubric admits: the right entity, the
	// right category, and fabricated evidence, all without reading any
	// telemetry. It exists to measure the ceiling on what tightening can achieve
	// — evidence is checked for form, not truth, so this profile SHOULD score
	// well. If it ever scores poorly, the check has become something other than
	// what it claims to be.
	StubCited StubProfile = "cited"
)

// StubProfiles lists every profile, for CLI validation and tests.
func StubProfiles() []string {
	return []string{string(StubVague), string(StubObvious), string(StubShotgun), string(StubCited)}
}

type stubEntity struct {
	Kind      string `json:"kind"`
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name"`
}

type stubEvidence struct {
	Signal      string `json:"signal"`
	Query       string `json:"query,omitempty"`
	Observation string `json:"observation"`
}

// StubConfig configures the calibration stub.
type StubConfig struct {
	Profile StubProfile
	// Namespace is the environment namespace the stub guesses entities in.
	Namespace string
	// Obvious is the workload the entity-naming profiles (obvious, shotgun,
	// cited) put forward. It is REQUIRED for them and has no default: set it to
	// the scenario's ground-truth entity. A calibration that guesses the entity
	// measures the guess, not the rubric — with a wrong default, "cited" scored
	// 0.5 instead of its true ceiling of 1.0, understating what fabricated
	// evidence can get away with.
	Obvious string
	// Category is the fault classification the "cited" profile claims, and is
	// required for it: set it to the scenario's ground-truth category so the
	// profile represents a fully-correct answer. The other profiles always claim
	// a deliberately wrong category.
	Category string
	// Extra names the workloads "shotgun" lists alongside Obvious. Set it to the
	// scenario's decoys to check the decoy penalty actually bites; left empty it
	// falls back to a generic spread of otel-demo services.
	Extra []string
}

// NewStub builds a calibration stub. An unknown profile is an error: silently
// defaulting would make a calibration result mean the wrong thing.
func NewStub(cfg StubConfig) (*StubAgent, error) {
	ns := cfg.Namespace
	if ns == "" {
		ns = "otel-demo"
	}
	obvious := strings.TrimSpace(cfg.Obvious)
	if obvious == "" && cfg.Profile != StubVague {
		return nil, fmt.Errorf("stub profile %q needs the entity to name (--stub-obvious): "+
			"set it to the scenario's ground-truth entity", cfg.Profile)
	}

	s := &StubAgent{profile: cfg.Profile}
	switch cfg.Profile {
	case StubVague:
		// No entities at all. The diagnosis will fail validation downstream,
		// which is itself the calibration signal.
		s.category = "performance-degradation"
		s.summary = "Elevated latency somewhere in the request path."
	case StubObvious:
		s.entities = []stubEntity{{Kind: "Deployment", Namespace: ns, Name: obvious}}
		s.category = "performance-degradation"
		s.summary = "Elevated latency; the " + obvious + " deployment looks unhealthy."
	case StubShotgun:
		extra := cfg.Extra
		if len(extra) == 0 {
			extra = []string{"frontend", "cart", "payment", "product-catalog"}
		}
		seen := map[string]bool{}
		for _, n := range append([]string{obvious}, extra...) {
			n = strings.TrimSpace(n)
			if n == "" || seen[n] {
				continue
			}
			seen[n] = true
			s.entities = append(s.entities, stubEntity{Kind: "Deployment", Namespace: ns, Name: n})
		}
		s.category = "performance-degradation"
		s.summary = "Several services show elevated latency."
	case StubCited:
		if strings.TrimSpace(cfg.Category) == "" {
			return nil, fmt.Errorf("stub profile %q needs the category to claim (--stub-category): "+
				"set it to the scenario's ground-truth category", cfg.Profile)
		}
		s.entities = []stubEntity{{Kind: "Deployment", Namespace: ns, Name: obvious}}
		// Deliberately the scenario's own category, to isolate what evidence adds.
		s.category = cfg.Category
		s.summary = "Telemetry for " + obvious + " shows the reported fault signature."
		// Plausible-looking and entirely invented — the stub queried nothing. It
		// uses the stack's real service label (job) so it reads like a genuine
		// citation; the scorer checks citations for form, never for truth.
		s.evidence = []stubEvidence{{
			Signal:      "metrics",
			Query:       `count({job="` + obvious + `"})`,
			Observation: "active series for " + obvious + " climbing steadily over the window",
		}}
	default:
		return nil, fmt.Errorf("unknown stub profile %q (want %s)",
			cfg.Profile, strings.Join(StubProfiles(), ", "))
	}
	return s, nil
}

// Name identifies the stub in the run record. It is deliberately prefixed so a
// calibration result can never be mistaken for a real agent's on a leaderboard.
func (s *StubAgent) Name() string { return "stub:" + string(s.profile) }

// Diagnose returns the canned answer. It ignores the tool surface entirely and
// reports zero usage: the stub consumes no budget because it does no work.
func (s *StubAgent) Diagnose(_ context.Context, _ Task) (Result, error) {
	payload := struct {
		RootCauseEntities []stubEntity   `json:"root_cause_entities"`
		Category          string         `json:"category"`
		Evidence          []stubEvidence `json:"evidence,omitempty"`
		Summary           string         `json:"summary,omitempty"`
	}{
		RootCauseEntities: s.entities,
		Category:          s.category,
		Evidence:          s.evidence,
		Summary:           s.summary,
	}
	// The vague profile must emit an explicitly empty array rather than null, so
	// it exercises the same validation path a real agent's empty answer would.
	if payload.RootCauseEntities == nil {
		payload.RootCauseEntities = []stubEntity{}
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return Result{}, err
	}
	return Result{Raw: raw, Usage: Usage{Steps: 1}}, nil
}

var _ Agent = (*StubAgent)(nil)
