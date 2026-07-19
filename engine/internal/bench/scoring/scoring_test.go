package scoring

import (
	"testing"

	"github.com/tamen25/Argus/engine/internal/bench"
)

func ent(kind, ns, name string) bench.Entity {
	return bench.Entity{Kind: kind, Namespace: ns, Name: name}
}

func gt(cat string, es ...bench.Entity) bench.GroundTruth {
	return bench.GroundTruth{RootCauseEntities: es, Category: cat}
}

func diag(cat string, es ...bench.Entity) bench.Diagnosis {
	return bench.Diagnosis{Scenario: "s", RootCauseEntities: es, Category: cat}
}

func TestScore_JaccardPartialCredit(t *testing.T) {
	spec := bench.ScoringSpec{EntityMatch: bench.MatchJaccard, PartialCredit: true}
	g := gt("cardinality-explosion", ent("Deployment", "otel-demo", "checkout"), ent("Deployment", "otel-demo", "cart"))

	tests := []struct {
		name string
		d    bench.Diagnosis
		want float64
	}{
		{"perfect", diag("cardinality-explosion", ent("Deployment", "otel-demo", "checkout"), ent("Deployment", "otel-demo", "cart")), 1.0},
		{"half (1 of 2, no extras)", diag("x", ent("Deployment", "otel-demo", "checkout")), 0.5},
		{"one right one wrong", diag("x", ent("Deployment", "otel-demo", "checkout"), ent("Deployment", "otel-demo", "frontend")), 1.0 / 3.0},
		{"all wrong", diag("x", ent("Pod", "otel-demo", "nope")), 0.0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Score(g, spec, tc.d).EntityScore
			if !approx(got, tc.want) {
				t.Errorf("EntityScore = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestScore_NoPartialCredit(t *testing.T) {
	spec := bench.ScoringSpec{EntityMatch: bench.MatchJaccard, PartialCredit: false}
	g := gt("c", ent("Deployment", "ns", "a"), ent("Deployment", "ns", "b"))

	if s := Score(g, spec, diag("c", ent("Deployment", "ns", "a"))).EntityScore; s != 0 {
		t.Errorf("partial answer scored %v, want 0 without partial credit", s)
	}
	full := Score(g, spec, diag("c", ent("Deployment", "ns", "a"), ent("Deployment", "ns", "b"))).EntityScore
	if full != 1 {
		t.Errorf("exact answer scored %v, want 1", full)
	}
}

func TestScore_ExactStrategy(t *testing.T) {
	spec := bench.ScoringSpec{EntityMatch: bench.MatchExact}
	g := gt("c", ent("Deployment", "ns", "a"))
	// Extra entity breaks an exact match.
	s := Score(g, spec, diag("c", ent("Deployment", "ns", "a"), ent("Pod", "ns", "x"))).EntityScore
	if s != 0 {
		t.Errorf("extra entity scored %v under exact match, want 0", s)
	}
}

func TestScore_CategoryCaseInsensitive(t *testing.T) {
	spec := bench.ScoringSpec{}
	g := gt("Cardinality-Explosion", ent("Deployment", "ns", "a"))
	r := Score(g, spec, diag("cardinality-explosion", ent("Deployment", "ns", "a")))
	if !r.CategoryMatch {
		t.Error("category match should be case-insensitive")
	}
	// Default (empty) EntityMatch is jaccard, no partial credit -> exact set = 1.
	if r.EntityScore != 1 {
		t.Errorf("EntityScore = %v, want 1", r.EntityScore)
	}
}

func TestScore_Breakdown(t *testing.T) {
	spec := bench.ScoringSpec{EntityMatch: bench.MatchJaccard, PartialCredit: true}
	g := gt("c", ent("Deployment", "ns", "a"), ent("Deployment", "ns", "b"))
	r := Score(g, spec, diag("c", ent("Deployment", "ns", "a"), ent("Pod", "ns", "z")))
	if len(r.Matched) != 1 || r.Matched[0].Name != "a" {
		t.Errorf("matched = %+v", r.Matched)
	}
	if len(r.Missed) != 1 || r.Missed[0].Name != "b" {
		t.Errorf("missed = %+v", r.Missed)
	}
	if len(r.Extra) != 1 || r.Extra[0].Name != "z" {
		t.Errorf("extra = %+v", r.Extra)
	}
}

func approx(a, b float64) bool {
	const eps = 1e-9
	d := a - b
	return d < eps && d > -eps
}
