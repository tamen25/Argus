// Package scoring deterministically grades a bench diagnosis against a
// scenario's ground truth. It is part of the deterministic core: it never
// imports the LLM client (architecture rule 2, depguard-enforced). An LLM may
// normalize a shell agent's output into a Diagnosis upstream, but the grade
// itself is pure arithmetic over entity sets.
package scoring

import (
	"sort"
	"strings"

	"github.com/tamen25/Argus/engine/internal/bench"
)

// Result is the graded outcome of one diagnosis against one scenario.
type Result struct {
	Scenario string `json:"scenario"`
	// Score is the overall grade in [0,1] and the only number a leaderboard
	// should rank on. It combines entity agreement with the fault
	// classification, deducts for decoys named, and is zero when a scenario
	// requires evidence the agent did not cite.
	Score float64 `json:"score"`
	// EntityScore is the entity-set agreement in [0,1]: Jaccard overlap when the
	// scenario uses partial credit, else 1.0 only on an exact set match. Kept
	// broken out so a report can show where a score came from.
	EntityScore float64 `json:"entity_score"`
	// CategoryMatch reports whether the agent's category equals ground truth
	// (case-insensitive). It is folded into Score with weight
	// ScoringSpec.CategoryWeight: naming the right workload for the wrong reason
	// is a partial answer, not a complete one.
	CategoryMatch bool `json:"category_match"`
	// DecoysNamed are plausible-but-wrong entities the agent asserted. Each costs
	// ScoringSpec.DecoyPenalty.
	DecoysNamed []bench.Entity `json:"decoys_named,omitempty"`
	// EvidenceCount is how many well-formed citations the diagnosis carried, and
	// CitedSignals which telemetry kinds it consulted — both recorded so a report
	// can show whether an agent actually investigated.
	EvidenceCount int      `json:"evidence_count"`
	CitedSignals  []string `json:"cited_signals,omitempty"`
	// EvidenceMissing is set when the scenario required evidence and none was
	// cited. The score is zero and the reason is explicit rather than inferred.
	EvidenceMissing bool `json:"evidence_missing,omitempty"`
	// Matched/Missed/Extra break down the entity comparison for the report.
	Matched []bench.Entity `json:"matched"`
	Missed  []bench.Entity `json:"missed"`
	Extra   []bench.Entity `json:"extra"`
}

// Score grades a diagnosis against a scenario's ground truth and scoring config.
// An empty EntityMatch defaults to Jaccard. PartialCredit only affects the
// Jaccard path — an exact-match scenario is all-or-nothing by definition.
func Score(gt bench.GroundTruth, spec bench.ScoringSpec, d bench.Diagnosis) Result {
	want := entitySet(gt.RootCauseEntities)
	got := entitySet(d.RootCauseEntities)

	var matched, missed, extra []bench.Entity
	for k, e := range want {
		if _, ok := got[k]; ok {
			matched = append(matched, e)
		} else {
			missed = append(missed, e)
		}
	}
	for k, e := range got {
		if _, ok := want[k]; !ok {
			extra = append(extra, e)
		}
	}
	sortEntities(matched)
	sortEntities(missed)
	sortEntities(extra)

	res := Result{
		Scenario:      d.Scenario,
		CategoryMatch: strings.EqualFold(strings.TrimSpace(gt.Category), strings.TrimSpace(d.Category)),
		DecoysNamed:   decoysNamed(gt.Decoys, got),
		EvidenceCount: len(d.Evidence),
		CitedSignals:  d.CitedSignals(),
		Matched:       matched,
		Missed:        missed,
		Extra:         extra,
	}

	match := spec.EntityMatch
	if match == "" {
		match = bench.MatchJaccard
	}
	switch match {
	case bench.MatchExact:
		if len(missed) == 0 && len(extra) == 0 && len(want) > 0 {
			res.EntityScore = 1
		}
	default: // jaccard
		union := len(want) + len(got) - len(matched)
		if !spec.PartialCredit {
			// No partial credit: full marks only on a clean Jaccard of 1.
			if union > 0 && len(matched) == union {
				res.EntityScore = 1
			}
			break
		}
		if union > 0 {
			res.EntityScore = float64(len(matched)) / float64(union)
		}
	}

	res.EvidenceMissing = spec.RequireEvidence && res.EvidenceCount == 0
	res.Score = overall(res, spec)
	return res
}

// overall folds the graded components into the single number a leaderboard
// ranks on:
//
//	score = (1-w)·entity + w·category  −  penalty·decoys        clamped to [0,1]
//
// and collapses to zero when the scenario demanded evidence and got none. The
// arithmetic is deliberately simple and inspectable: a benchmark whose headline
// number cannot be recomputed by hand from the report is not defensible.
func overall(res Result, spec bench.ScoringSpec) float64 {
	if spec.RequireEvidence && res.EvidenceCount == 0 {
		return 0
	}
	w := spec.EffectiveCategoryWeight()
	var cat float64
	if res.CategoryMatch {
		cat = 1
	}
	s := (1-w)*res.EntityScore + w*cat
	s -= spec.EffectiveDecoyPenalty() * float64(len(res.DecoysNamed))
	return clamp01(s)
}

// decoysNamed returns the ground-truth decoys the agent asserted, sorted.
func decoysNamed(decoys []bench.Entity, got map[string]bench.Entity) []bench.Entity {
	var named []bench.Entity
	for _, d := range decoys {
		if _, ok := got[key(d)]; ok {
			named = append(named, d)
		}
	}
	sortEntities(named)
	return named
}

func clamp01(f float64) float64 {
	switch {
	case f < 0:
		return 0
	case f > 1:
		return 1
	default:
		return f
	}
}

// entitySet keys entities by a normalized kind/namespace/name triple so
// comparison is case- and whitespace-insensitive.
func entitySet(es []bench.Entity) map[string]bench.Entity {
	m := make(map[string]bench.Entity, len(es))
	for _, e := range es {
		m[key(e)] = e
	}
	return m
}

func key(e bench.Entity) string {
	norm := func(s string) string { return strings.ToLower(strings.TrimSpace(s)) }
	return norm(e.Kind) + "/" + norm(e.Namespace) + "/" + norm(e.Name)
}

func sortEntities(es []bench.Entity) {
	sort.Slice(es, func(i, j int) bool { return key(es[i]) < key(es[j]) })
}
