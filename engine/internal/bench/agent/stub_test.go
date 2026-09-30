package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

type stubAnswer struct {
	RootCauseEntities []stubEntity   `json:"root_cause_entities"`
	Category          string         `json:"category"`
	Evidence          []stubEvidence `json:"evidence"`
}

func diagnoseStub(t *testing.T, cfg StubConfig) stubAnswer {
	t.Helper()
	s, err := NewStub(cfg)
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.Diagnose(context.Background(), Task{})
	if err != nil {
		t.Fatal(err)
	}
	var got stubAnswer
	if err := json.Unmarshal(res.Raw, &got); err != nil {
		t.Fatalf("stub emitted unparseable JSON: %v", err)
	}
	return got
}

func TestNewStubRejectsUnknownProfile(t *testing.T) {
	if _, err := NewStub(StubConfig{Profile: "sensible", Obvious: "frontend"}); err == nil {
		t.Fatal("want error for unknown profile, got nil")
	}
}

// TestStubRequiresTheEntityToName is the regression guard for B-15. The stub
// used to default to "checkout", which stopped being any scenario's ground
// truth when scenario 1 was retargeted. Calibrating with a wrong default made
// "cited" score 0.5 instead of its true ceiling of 1.0 — a quietly misleading
// calibration. The entity must now be named explicitly.
func TestStubRequiresTheEntityToName(t *testing.T) {
	for _, p := range []StubProfile{StubObvious, StubShotgun, StubCited} {
		if _, err := NewStub(StubConfig{Profile: p, Category: "c"}); err == nil ||
			!strings.Contains(err.Error(), "--stub-obvious") {
			t.Errorf("profile %s without Obvious: err = %v, want a --stub-obvious requirement", p, err)
		}
	}
	// vague names no entity, so it needs none.
	if _, err := NewStub(StubConfig{Profile: StubVague}); err != nil {
		t.Errorf("vague must not require an entity, got %v", err)
	}
}

func TestStubCitedRequiresTheCategoryToClaim(t *testing.T) {
	_, err := NewStub(StubConfig{Profile: StubCited, Obvious: "frontend"})
	if err == nil || !strings.Contains(err.Error(), "--stub-category") {
		t.Fatalf("cited without Category: err = %v, want a --stub-category requirement", err)
	}
}

func TestStubNameIsPrefixed(t *testing.T) {
	// A calibration result must never be mistakable for a real agent's.
	s, err := NewStub(StubConfig{Profile: StubObvious, Obvious: "frontend"})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := s.Name(), "stub:obvious"; got != want {
		t.Fatalf("Name() = %q, want %q", got, want)
	}
}

func TestStubProfilesProduceExpectedAnswers(t *testing.T) {
	tests := []struct {
		profile      StubProfile
		wantEntities int
	}{
		{StubVague, 0},
		{StubObvious, 1},
		// frontend plus the generic fallbacks (frontend, cart, payment,
		// product-catalog), with the duplicate frontend collapsed: 4.
		{StubShotgun, 4},
	}
	for _, tc := range tests {
		t.Run(string(tc.profile), func(t *testing.T) {
			got := diagnoseStub(t, StubConfig{Profile: tc.profile, Namespace: "otel-demo", Obvious: "frontend"})
			if len(got.RootCauseEntities) != tc.wantEntities {
				t.Fatalf("entities = %d, want %d", len(got.RootCauseEntities), tc.wantEntities)
			}
			if tc.wantEntities > 0 && got.RootCauseEntities[0].Name != "frontend" {
				t.Fatalf("first entity = %q, want the named entity", got.RootCauseEntities[0].Name)
			}
			// These profiles claim a deliberately wrong category — only cited may
			// claim the real one.
			if got.Category == "cardinality-explosion" {
				t.Fatalf("stub must not emit the ground-truth category, got %q", got.Category)
			}
		})
	}
}

// TestStubShotgunNamesTheGivenDecoys is the regression guard for B-26: via the
// CLI the shotgun profile used to name a fixed generic list, so it never hit a
// scenario's actual decoys and the decoy penalty went unexercised end to end.
func TestStubShotgunNamesTheGivenDecoys(t *testing.T) {
	got := diagnoseStub(t, StubConfig{Profile: StubShotgun, Obvious: "frontend",
		Extra: []string{"product-reviews", "frontend-proxy", "frontend"}}) // dup of Obvious ignored
	var names []string
	for _, e := range got.RootCauseEntities {
		names = append(names, e.Name)
	}
	if want := "frontend,product-reviews,frontend-proxy"; strings.Join(names, ",") != want {
		t.Fatalf("shotgun named %v, want %s (Obvious first, Extra deduplicated)", names, want)
	}
}

// TestStubCitedIsAFullyCorrectCitedAnswer: cited is the honest ceiling — the
// named entity, the claimed category, and a well-formed (fabricated) citation
// using the stack's real service label.
func TestStubCitedIsAFullyCorrectCitedAnswer(t *testing.T) {
	got := diagnoseStub(t, StubConfig{Profile: StubCited, Obvious: "frontend", Category: "cardinality-explosion"})
	if got.Category != "cardinality-explosion" || got.RootCauseEntities[0].Name != "frontend" {
		t.Fatalf("cited = %+v, want the named entity and category", got)
	}
	if len(got.Evidence) != 1 || !strings.Contains(got.Evidence[0].Query, `job="frontend"`) {
		t.Fatalf("evidence = %+v, want one citation querying job=\"frontend\"", got.Evidence)
	}
}

// TestStubConsumesNoBudget documents that a calibration run costs nothing, so it
// can be re-run freely as scenarios change.
func TestStubConsumesNoBudget(t *testing.T) {
	s, err := NewStub(StubConfig{Profile: StubShotgun, Obvious: "frontend"})
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.Diagnose(context.Background(), Task{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Usage.ToolCalls != 0 || res.Usage.Tokens != 0 {
		t.Fatalf("stub reported usage %+v, want zero tool calls and tokens", res.Usage)
	}
}
