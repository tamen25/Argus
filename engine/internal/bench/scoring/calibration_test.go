package scoring_test

import (
	"testing"

	"github.com/tamen25/Argus/engine/internal/bench"
	"github.com/tamen25/Argus/engine/internal/bench/scoring"
)

// scenario1 mirrors scenarios/cardinality-explosion-checkout.yaml: a single
// ground-truth entity, Jaccard matching, partial credit on.
func scenario1() (bench.GroundTruth, bench.ScoringSpec) {
	return bench.GroundTruth{
			RootCauseEntities: []bench.Entity{
				{Kind: "Deployment", Namespace: "otel-demo", Name: "checkout"},
			},
			Category: "cardinality-explosion",
		}, bench.ScoringSpec{
			EntityMatch:   bench.MatchJaccard,
			PartialCredit: true,
		}
}

// TestCalibrationObviousGuessScoresPerfect is the finding that matters: an agent
// that names the most conspicuous workload with a WRONG category scores a
// flawless 1.0, because CategoryMatch is recorded beside the score and never
// folded into it. The rubric as written cannot distinguish a real diagnosis from
// a lucky guess on a single-entity scenario.
//
// This test asserts the current (too loose) behavior deliberately. When the
// rubric is tightened, this test should fail and be updated — that failure is
// the point.
func TestCalibrationObviousGuessScoresPerfect(t *testing.T) {
	gt, spec := scenario1()
	d := bench.Diagnosis{
		Scenario: "cardinality-explosion-checkout",
		RootCauseEntities: []bench.Entity{
			{Kind: "Deployment", Namespace: "otel-demo", Name: "checkout"},
		},
		Category: "performance-degradation", // wrong
	}
	got := scoring.Score(gt, spec, d)
	if got.EntityScore != 1 {
		t.Fatalf("EntityScore = %v, want 1 (documents the loose rubric)", got.EntityScore)
	}
	if got.CategoryMatch {
		t.Fatal("CategoryMatch = true, want false — the stub's category is wrong")
	}
}

// TestCalibrationShotgunIsPenalized confirms Jaccard does punish over-broad
// answers, so the rubric is not uniformly loose.
func TestCalibrationShotgunIsPenalized(t *testing.T) {
	gt, spec := scenario1()
	d := bench.Diagnosis{
		Scenario: "cardinality-explosion-checkout",
		RootCauseEntities: []bench.Entity{
			{Kind: "Deployment", Namespace: "otel-demo", Name: "checkout"},
			{Kind: "Deployment", Namespace: "otel-demo", Name: "frontend"},
			{Kind: "Deployment", Namespace: "otel-demo", Name: "cart"},
			{Kind: "Deployment", Namespace: "otel-demo", Name: "payment"},
			{Kind: "Deployment", Namespace: "otel-demo", Name: "product-catalog"},
		},
		Category: "performance-degradation",
	}
	got := scoring.Score(gt, spec, d)
	if want := 0.2; got.EntityScore != want {
		t.Fatalf("EntityScore = %v, want %v (1 matched / 5 union)", got.EntityScore, want)
	}
}

// TestCalibrationVagueAnswerIsNotScoredZero documents that a no-entity answer
// never reaches scoring at all: it fails Diagnosis.Validate upstream and is
// recorded as a failure. That is correct, but it means MeanEntityScore is an
// average over answered runs only — a leaderboard MUST show diagnoses/attempts
// beside it, or an agent that mostly refuses to answer looks excellent.
func TestCalibrationVagueAnswerIsNotScoredZero(t *testing.T) {
	d := bench.Diagnosis{
		Scenario:          "cardinality-explosion-checkout",
		RootCauseEntities: nil,
		Category:          "performance-degradation",
	}
	if err := d.Validate(); err == nil {
		t.Fatal("want validation error for an entity-less diagnosis, got nil")
	}
}
