package scoring_test

import (
	"path/filepath"
	"testing"

	"github.com/tamen25/Argus/engine/internal/bench"
	"github.com/tamen25/Argus/engine/internal/bench/scoring"
)

// scenario1 loads the SHIPPED scenario rather than restating it. The previous
// fixture was a hand-copy that still described the checkout scenario weeks after
// it was retargeted to frontend (B-15), so these tests certified a rubric that no
// longer shipped. Loading the file means a change to the real scoring block or
// decoy list is exercised here automatically.
func scenario1(t *testing.T) (bench.GroundTruth, bench.ScoringSpec) {
	t.Helper()
	sc, err := bench.LoadScenario(filepath.Join("..", "..", "..", "..", "scenarios", "cardinality-explosion-frontend.yaml"))
	if err != nil {
		t.Fatalf("loading the shipped scenario: %v", err)
	}
	return sc.Spec.GroundTruth, sc.Spec.Scoring
}

func ent(name string) bench.Entity {
	return bench.Entity{Kind: "Deployment", Namespace: "otel-demo", Name: name}
}

func cite(sig, obs string) []bench.Evidence {
	return []bench.Evidence{{Signal: sig, Observation: obs}}
}

const scenario = "cardinality-explosion-frontend"

// TestCalibrationObviousGuessScoresZero is the guard for the finding that
// triggered the rubric work: naming the right workload with a wrong category and
// no evidence once scored a flawless 1.00.
func TestCalibrationObviousGuessScoresZero(t *testing.T) {
	gt, spec := scenario1(t)
	got := scoring.Score(gt, spec, bench.Diagnosis{
		Scenario:          scenario,
		RootCauseEntities: []bench.Entity{ent("frontend")},
		Category:          "performance-degradation", // wrong, and no evidence
	})
	if got.Score != 0 {
		t.Fatalf("Score = %v, want 0 (an uncited guess must not score)", got.Score)
	}
	if !got.EvidenceMissing {
		t.Fatal("EvidenceMissing = false, want true")
	}
	// The entity component is still reported, so a report shows the guess landed
	// on the right workload even though it scored nothing.
	if got.EntityScore != 1 {
		t.Fatalf("EntityScore = %v, want 1", got.EntityScore)
	}
}

// TestCalibrationRightEntityWrongCategoryIsPartial: with evidence, naming the
// right workload for the wrong reason is worth half, not everything.
func TestCalibrationRightEntityWrongCategoryIsPartial(t *testing.T) {
	gt, spec := scenario1(t)
	got := scoring.Score(gt, spec, bench.Diagnosis{
		Scenario:          scenario,
		RootCauseEntities: []bench.Entity{ent("frontend")},
		Category:          "performance-degradation",
		Evidence:          cite("metrics", "frontend latency elevated"),
	})
	if want := 0.5; got.Score != want {
		t.Fatalf("Score = %v, want %v (0.5 x entity 1.0 + 0.5 x category 0)", got.Score, want)
	}
}

// TestCalibrationShotgunHitsTheRealDecoys: the old fixture's invented decoys
// happened to overlap the stub's list, so the penalty was tested against a
// rubric that did not ship. This names the scenario's actual decoys.
func TestCalibrationShotgunHitsTheRealDecoys(t *testing.T) {
	gt, spec := scenario1(t)
	var named []bench.Entity
	named = append(named, ent("frontend"))
	named = append(named, gt.Decoys...)
	named = append(named, ent("cart"))
	got := scoring.Score(gt, spec, bench.Diagnosis{
		Scenario:          scenario,
		RootCauseEntities: named,
		Category:          "performance-degradation",
		Evidence:          cite("metrics", "several services look unhealthy"),
	})
	if len(got.DecoysNamed) != len(gt.Decoys) || len(gt.Decoys) == 0 {
		t.Fatalf("DecoysNamed = %d, want all %d of the shipped decoys", len(got.DecoysNamed), len(gt.Decoys))
	}
	if got.Score != 0 {
		t.Fatalf("Score = %v, want 0: dilution plus a penalty per decoy", got.Score)
	}
}

// TestCalibrationFullCorrectAnswerScoresOne: a rubric nothing can pass is as
// useless as one everything passes.
func TestCalibrationFullCorrectAnswerScoresOne(t *testing.T) {
	gt, spec := scenario1(t)
	got := scoring.Score(gt, spec, bench.Diagnosis{
		Scenario:          scenario,
		RootCauseEntities: gt.RootCauseEntities,
		Category:          gt.Category,
		Evidence:          cite("metrics", "frontend active series growing without bound"),
	})
	if got.Score != 1 {
		t.Fatalf("Score = %v, want 1 for a fully correct, cited diagnosis", got.Score)
	}
}

// TestCalibrationFabricatedEvidenceStillScores is the honest limit of the
// rubric: citations are checked for form, never for truth.
func TestCalibrationFabricatedEvidenceStillScores(t *testing.T) {
	gt, spec := scenario1(t)
	got := scoring.Score(gt, spec, bench.Diagnosis{
		Scenario:          scenario,
		RootCauseEntities: gt.RootCauseEntities,
		Category:          gt.Category,
		Evidence:          []bench.Evidence{{Signal: "metrics", Query: "entirely invented", Observation: "never queried"}},
	})
	if got.Score != 1 {
		t.Fatalf("Score = %v, want 1 — fabricated but well-formed evidence passes by design", got.Score)
	}
}

// TestCalibrationMalformedEvidenceDoesNotCount: a citation with an unknown
// signal is not a citation, so under requireEvidence it scores like no evidence
// — zero — rather than failing the diagnosis (see B-04).
func TestCalibrationMalformedEvidenceDoesNotCount(t *testing.T) {
	gt, spec := scenario1(t)
	got := scoring.Score(gt, spec, bench.Diagnosis{
		Scenario:          scenario,
		RootCauseEntities: gt.RootCauseEntities,
		Category:          gt.Category,
		Evidence:          cite("prometheus", "series tripled"),
	})
	if got.Score != 0 || got.EvidenceCount != 0 || got.MalformedEvidence != 1 {
		t.Fatalf("got Score=%v EvidenceCount=%d Malformed=%d, want 0/0/1",
			got.Score, got.EvidenceCount, got.MalformedEvidence)
	}
}

// TestCalibrationVagueAnswerIsNotScoredZero: a no-entity answer never reaches
// scoring; it fails validation upstream and is recorded as a failure, which is
// why a report must show the answered rate beside the mean.
func TestCalibrationVagueAnswerIsNotScoredZero(t *testing.T) {
	d := bench.Diagnosis{Scenario: scenario, Category: "performance-degradation"}
	if err := d.Validate(); err == nil {
		t.Fatal("want a validation error for an entity-less diagnosis, got nil")
	}
}
