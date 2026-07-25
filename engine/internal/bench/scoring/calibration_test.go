package scoring_test

import (
	"testing"

	"github.com/tamen25/Argus/engine/internal/bench"
	"github.com/tamen25/Argus/engine/internal/bench/scoring"
)

func ptr(f float64) *float64 { return &f }

// scenario1 mirrors scenarios/cardinality-explosion-checkout.yaml after the
// rubric was tightened: one ground-truth entity, three decoys, category carrying
// half the score, evidence mandatory.
func scenario1() (bench.GroundTruth, bench.ScoringSpec) {
	return bench.GroundTruth{
			RootCauseEntities: []bench.Entity{
				{Kind: "Deployment", Namespace: "otel-demo", Name: "checkout"},
			},
			Category: "cardinality-explosion",
			Decoys: []bench.Entity{
				{Kind: "Deployment", Namespace: "otel-demo", Name: "frontend"},
				{Kind: "Deployment", Namespace: "otel-demo", Name: "cart"},
				{Kind: "Deployment", Namespace: "otel-demo", Name: "payment"},
			},
		}, bench.ScoringSpec{
			EntityMatch:     bench.MatchJaccard,
			PartialCredit:   true,
			CategoryWeight:  ptr(0.5),
			RequireEvidence: true,
		}
}

func ent(name string) bench.Entity {
	return bench.Entity{Kind: "Deployment", Namespace: "otel-demo", Name: name}
}

// TestCalibrationObviousGuessNowScoresZero is the regression guard for the
// finding that triggered this rubric change: before, naming the most conspicuous
// workload with a wrong category and no evidence scored a flawless 1.00.
func TestCalibrationObviousGuessNowScoresZero(t *testing.T) {
	gt, spec := scenario1()
	got := scoring.Score(gt, spec, bench.Diagnosis{
		Scenario:          "cardinality-explosion-checkout",
		RootCauseEntities: []bench.Entity{ent("checkout")},
		Category:          "performance-degradation", // wrong
		// no evidence
	})
	if got.Score != 0 {
		t.Fatalf("Score = %v, want 0 (uncited guess must not score)", got.Score)
	}
	if !got.EvidenceMissing {
		t.Fatal("EvidenceMissing = false, want true")
	}
	// The entity component is still recorded, so a report can show the guess did
	// land on the right workload even though the answer scored nothing.
	if got.EntityScore != 1 {
		t.Fatalf("EntityScore = %v, want 1 (component still reported)", got.EntityScore)
	}
}

// TestCalibrationRightEntityWrongCategoryIsPartial confirms category is folded
// in: with evidence supplied, naming the right workload for the wrong reason is
// worth half, not everything.
func TestCalibrationRightEntityWrongCategoryIsPartial(t *testing.T) {
	gt, spec := scenario1()
	got := scoring.Score(gt, spec, bench.Diagnosis{
		Scenario:          "cardinality-explosion-checkout",
		RootCauseEntities: []bench.Entity{ent("checkout")},
		Category:          "performance-degradation",
		Evidence: []bench.Evidence{
			{Signal: "metrics", Observation: "checkout latency elevated"},
		},
	})
	if want := 0.5; got.Score != want {
		t.Fatalf("Score = %v, want %v (0.5·entity 1.0 + 0.5·category 0)", got.Score, want)
	}
}

// TestCalibrationShotgunIsHeavilyPenalized: the shotgun answer now collides with
// three decoys on top of Jaccard dilution, driving it to zero.
func TestCalibrationShotgunIsHeavilyPenalized(t *testing.T) {
	gt, spec := scenario1()
	got := scoring.Score(gt, spec, bench.Diagnosis{
		Scenario: "cardinality-explosion-checkout",
		RootCauseEntities: []bench.Entity{
			ent("checkout"), ent("frontend"), ent("cart"), ent("payment"), ent("product-catalog"),
		},
		Category: "performance-degradation",
		Evidence: []bench.Evidence{
			{Signal: "metrics", Observation: "several services show elevated latency"},
		},
	})
	if len(got.DecoysNamed) != 3 {
		t.Fatalf("DecoysNamed = %d, want 3", len(got.DecoysNamed))
	}
	// 0.5·0.2 + 0.5·0 = 0.1, less 3×0.25 decoy penalty, clamped at 0.
	if got.Score != 0 {
		t.Fatalf("Score = %v, want 0", got.Score)
	}
}

// TestCalibrationFullCorrectAnswerScoresOne: the rubric must still let a genuine
// diagnosis reach the top. A rubric nothing can pass is as useless as one
// everything passes.
func TestCalibrationFullCorrectAnswerScoresOne(t *testing.T) {
	gt, spec := scenario1()
	got := scoring.Score(gt, spec, bench.Diagnosis{
		Scenario:          "cardinality-explosion-checkout",
		RootCauseEntities: []bench.Entity{ent("checkout")},
		Category:          "cardinality-explosion",
		Evidence: []bench.Evidence{
			{Signal: "metrics", Query: `count(...)`, Observation: "checkout active series growing without bound"},
		},
	})
	if got.Score != 1 {
		t.Fatalf("Score = %v, want 1 for a fully correct, cited diagnosis", got.Score)
	}
}

// TestCalibrationFabricatedEvidenceStillScores is the honest limit of this
// rubric, asserted so nobody mistakes the evidence check for proof of work: the
// scorer verifies that citations are present and well-formed, never that they
// are true. A confident fabricator scores full marks.
func TestCalibrationFabricatedEvidenceStillScores(t *testing.T) {
	gt, spec := scenario1()
	got := scoring.Score(gt, spec, bench.Diagnosis{
		Scenario:          "cardinality-explosion-checkout",
		RootCauseEntities: []bench.Entity{ent("checkout")},
		Category:          "cardinality-explosion",
		Evidence: []bench.Evidence{
			{Signal: "metrics", Query: "entirely invented", Observation: "never actually queried"},
		},
	})
	if got.Score != 1 {
		t.Fatalf("Score = %v, want 1 — fabricated-but-well-formed evidence passes by design", got.Score)
	}
}

// TestCalibrationVagueAnswerIsNotScoredZero documents that a no-entity answer
// never reaches scoring at all: it fails Diagnosis.Validate upstream and is
// recorded as a failure, which is why the report must show the answered rate.
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
