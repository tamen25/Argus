package backtest_test

import (
	"testing"
	"time"

	"github.com/tamen25/Argus/engine/internal/backtest"
)

// LoadIncidents reads the incident registry (incidents.yaml, schema v1) —
// the backtest's ground truth. Strict: unknown keys and malformed timestamps
// are errors, because a silently dropped incident becomes a silently wrong
// "no misses" verdict.
func TestLoadIncidentsValid(t *testing.T) {
	reg, err := backtest.LoadIncidents("testdata/incidents-valid.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if len(reg.Incidents) != 2 {
		t.Fatalf("incidents = %d, want 2", len(reg.Incidents))
	}
	inc := reg.Incidents[0]
	if inc.ID != "2026-07-18-adfailure-baseline-2" {
		t.Errorf("id = %q", inc.ID)
	}
	if !inc.Start.Equal(time.Date(2026, 7, 18, 5, 16, 23, 0, time.UTC)) {
		t.Errorf("start = %v", inc.Start)
	}
	if len(inc.Services) != 1 || inc.Services[0] != "ad" {
		t.Errorf("services = %v", inc.Services)
	}
	if len(inc.ExpectedAlerts) != 2 || !inc.Induced {
		t.Errorf("expected_alerts = %v induced = %v", inc.ExpectedAlerts, inc.Induced)
	}
}

func TestLoadIncidentsRejectsUnknownKeys(t *testing.T) {
	if _, err := backtest.LoadIncidents("testdata/incidents-unknown-key.yaml"); err == nil {
		t.Error("unknown key loaded without error")
	}
}

func TestLoadIncidentsRejectsBadWindow(t *testing.T) {
	if _, err := backtest.LoadIncidents("testdata/incidents-bad-window.yaml"); err == nil {
		t.Error("end-before-start incident loaded without error")
	}
}

// The repo's own incidents.yaml must always load — it is the dogfood registry.
func TestLoadIncidentsRepoRegistry(t *testing.T) {
	reg, err := backtest.LoadIncidents("../../../incidents.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if len(reg.Incidents) < 3 {
		t.Errorf("repo registry has %d incidents, want >=3", len(reg.Incidents))
	}
}
