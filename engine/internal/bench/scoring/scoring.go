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
	// EntityScore is the entity-set agreement in [0,1]: Jaccard overlap when the
	// scenario uses partial credit, else 1.0 only on an exact set match.
	EntityScore float64 `json:"entity_score"`
	// CategoryMatch reports whether the agent's category equals ground truth
	// (case-insensitive). Recorded alongside the score, not folded into it —
	// the scoring block grades on entities.
	CategoryMatch bool `json:"category_match"`
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
	return res
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
