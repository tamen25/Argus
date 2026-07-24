package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
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
)

// StubProfiles lists every profile, for CLI validation and tests.
func StubProfiles() []string {
	return []string{string(StubVague), string(StubObvious), string(StubShotgun)}
}

type stubEntity struct {
	Kind      string `json:"kind"`
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name"`
}

// StubConfig configures the calibration stub.
type StubConfig struct {
	Profile StubProfile
	// Namespace is the environment namespace the stub guesses entities in.
	Namespace string
	// Obvious is the workload name the "obvious" profile guesses. Defaults to
	// "checkout" — the busiest service in otel-demo.
	Obvious string
}

// NewStub builds a calibration stub. An unknown profile is an error: silently
// defaulting would make a calibration result mean the wrong thing.
func NewStub(cfg StubConfig) (*StubAgent, error) {
	ns := cfg.Namespace
	if ns == "" {
		ns = "otel-demo"
	}
	obvious := cfg.Obvious
	if obvious == "" {
		obvious = "checkout"
	}

	s := &StubAgent{profile: cfg.Profile}
	switch cfg.Profile {
	case StubVague:
		// No entities at all. The diagnosis will fail validation downstream,
		// which is itself the calibration signal.
		s.category = "performance-degradation"
		s.summary = "Elevated latency in the checkout path."
	case StubObvious:
		s.entities = []stubEntity{{Kind: "Deployment", Namespace: ns, Name: obvious}}
		s.category = "performance-degradation"
		s.summary = "Elevated latency in the checkout path; the checkout deployment looks unhealthy."
	case StubShotgun:
		// A spread of plausible otel-demo workloads including the obvious one.
		for _, n := range []string{obvious, "frontend", "cart", "payment", "product-catalog"} {
			s.entities = append(s.entities, stubEntity{Kind: "Deployment", Namespace: ns, Name: n})
		}
		s.category = "performance-degradation"
		s.summary = "Several services in the checkout path show elevated latency."
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
		RootCauseEntities []stubEntity `json:"root_cause_entities"`
		Category          string       `json:"category"`
		Summary           string       `json:"summary,omitempty"`
	}{
		RootCauseEntities: s.entities,
		Category:          s.category,
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

// SortedProfiles is a small helper for stable CLI help text.
func SortedProfiles() []string {
	p := StubProfiles()
	sort.Strings(p)
	return p
}

var _ Agent = (*StubAgent)(nil)
