package bench

import (
	"reflect"
	"testing"
	"time"
)

func TestEvidenceWellFormed(t *testing.T) {
	cases := []struct {
		name string
		e    Evidence
		want bool
	}{
		{"metrics with observation", Evidence{Signal: "metrics", Observation: "p95 up"}, true},
		{"case and space tolerant", Evidence{Signal: " Traces ", Observation: "orphans"}, true},
		{"unknown signal", Evidence{Signal: "prometheus", Observation: "x"}, false},
		{"empty observation", Evidence{Signal: "logs", Observation: "  "}, false},
		{"empty signal", Evidence{Observation: "x"}, false},
	}
	for _, tc := range cases {
		if got := tc.e.WellFormed(); got != tc.want {
			t.Errorf("%s: WellFormed = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestCitedSignalsCountsOnlyWellFormed: a malformed citation must not appear as
// a signal the agent consulted, or a report would credit it with investigating.
func TestCitedSignalsCountsOnlyWellFormed(t *testing.T) {
	d := Diagnosis{Evidence: []Evidence{
		{Signal: "metrics", Observation: "a"},
		{Signal: "METRICS", Observation: "b"}, // duplicate after normalizing
		{Signal: "logs", Observation: "c"},
		{Signal: "prometheus", Observation: "d"}, // malformed: not counted
		{Signal: "traces"},                       // malformed: no observation
	}}
	if got, want := len(d.WellFormedEvidence()), 3; got != want {
		t.Errorf("WellFormedEvidence = %d, want %d", got, want)
	}
	if got, want := d.CitedSignals(), []string{"logs", "metrics"}; !reflect.DeepEqual(got, want) {
		t.Errorf("CitedSignals = %v, want %v (sorted, deduplicated, well-formed only)", got, want)
	}
	if got := (Diagnosis{}).CitedSignals(); len(got) != 0 {
		t.Errorf("no evidence: CitedSignals = %v, want empty", got)
	}
}

func TestSteadyStateValidate(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	cases := []struct {
		name    string
		ss      SteadyState
		wantErr bool
	}{
		{"min only", SteadyState{Query: "q", Min: f(1)}, false},
		{"max only", SteadyState{Query: "q", Max: f(1)}, false},
		{"band", SteadyState{Query: "q", Min: f(1), Max: f(2), Settle: "30s"}, false},
		{"empty query", SteadyState{Query: " ", Min: f(1)}, true},
		{"no bound", SteadyState{Query: "q"}, true},
		{"inverted band", SteadyState{Query: "q", Min: f(5), Max: f(1)}, true},
		{"bad settle", SteadyState{Query: "q", Min: f(1), Settle: "soon"}, true},
	}
	for _, tc := range cases {
		if err := tc.ss.validate(); (err != nil) != tc.wantErr {
			t.Errorf("%s: err = %v, wantErr %v", tc.name, err, tc.wantErr)
		}
	}
}

func TestSteadyStateSettleDur(t *testing.T) {
	for in, want := range map[string]time.Duration{"": 0, "  ": 0, "45s": 45 * time.Second, "2m": 2 * time.Minute} {
		got, err := SteadyState{Settle: in}.SettleDur()
		if err != nil || got != want {
			t.Errorf("SettleDur(%q) = (%v, %v), want (%v, nil)", in, got, err, want)
		}
	}
}

// TestScoringDefaults pins the rubric's defaults: changing either silently
// re-scores every scenario that relies on it, so it should take a test change.
func TestScoringDefaults(t *testing.T) {
	var unset ScoringSpec
	if got := unset.EffectiveCategoryWeight(); got != 0.5 {
		t.Errorf("default category weight = %v, want 0.5", got)
	}
	if got := unset.EffectiveDecoyPenalty(); got != 0.25 {
		t.Errorf("default decoy penalty = %v, want 0.25", got)
	}
	// An explicit zero is a real setting, not "unset".
	zero := 0.0
	set := ScoringSpec{CategoryWeight: &zero, DecoyPenalty: &zero}
	if set.EffectiveCategoryWeight() != 0 || set.EffectiveDecoyPenalty() != 0 {
		t.Error("an explicit 0 must override the default")
	}
}
