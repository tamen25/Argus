package agent

import (
	"context"
	"encoding/json"
	"testing"
)

func TestNewStubRejectsUnknownProfile(t *testing.T) {
	if _, err := NewStub(StubConfig{Profile: "sensible"}); err == nil {
		t.Fatal("want error for unknown profile, got nil")
	}
}

func TestStubNameIsPrefixed(t *testing.T) {
	// A calibration result must never be mistakable for a real agent's.
	s, err := NewStub(StubConfig{Profile: StubObvious})
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
		wantFirst    string
	}{
		{StubVague, 0, ""},
		{StubObvious, 1, "checkout"},
		{StubShotgun, 5, "checkout"},
	}
	for _, tc := range tests {
		t.Run(string(tc.profile), func(t *testing.T) {
			s, err := NewStub(StubConfig{Profile: tc.profile, Namespace: "otel-demo"})
			if err != nil {
				t.Fatal(err)
			}
			res, err := s.Diagnose(context.Background(), Task{})
			if err != nil {
				t.Fatal(err)
			}
			var got struct {
				RootCauseEntities []stubEntity `json:"root_cause_entities"`
				Category          string       `json:"category"`
			}
			if err := json.Unmarshal(res.Raw, &got); err != nil {
				t.Fatalf("stub emitted unparseable JSON: %v", err)
			}
			if len(got.RootCauseEntities) != tc.wantEntities {
				t.Fatalf("entities = %d, want %d", len(got.RootCauseEntities), tc.wantEntities)
			}
			if tc.wantEntities > 0 && got.RootCauseEntities[0].Name != tc.wantFirst {
				t.Fatalf("first entity = %q, want %q", got.RootCauseEntities[0].Name, tc.wantFirst)
			}
			// No profile may accidentally guess the real category — that would make
			// the stub look like it understood the fault.
			if got.Category == "cardinality-explosion" {
				t.Fatalf("stub must not emit the ground-truth category, got %q", got.Category)
			}
		})
	}
}

// TestStubConsumesNoBudget documents that a calibration run costs nothing, so it
// can be re-run freely as scenarios change.
func TestStubConsumesNoBudget(t *testing.T) {
	s, err := NewStub(StubConfig{Profile: StubShotgun})
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
