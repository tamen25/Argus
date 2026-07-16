package backtest_test

import (
	"testing"
	"time"

	"github.com/tamen25/Argus/engine/internal/backtest"
)

// LoadRuleFiles reads Prometheus/Mimir ruler rule files (also Sloth/Pyrra
// output — same format) into the backtest model. Strict: a file that doesn't
// parse is an error, never a silently empty rule set.
func TestLoadRuleFilesValid(t *testing.T) {
	rs, err := backtest.LoadRuleFiles("testdata/rules-valid.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if len(rs.Groups) != 1 {
		t.Fatalf("groups = %d, want 1", len(rs.Groups))
	}
	g := rs.Groups[0]
	if g.Name != "argus-spike" || g.Interval != time.Minute {
		t.Errorf("group = %q interval %v, want argus-spike 1m", g.Name, g.Interval)
	}
	if len(g.Rules) != 3 {
		t.Fatalf("rules = %d, want 3", len(g.Rules))
	}
	rec, alert := g.Rules[0], g.Rules[1]
	if rec.Alert || rec.Name != "service:http_errors:rate5m" {
		t.Errorf("rule 0 = %+v, want recording rule", rec)
	}
	if !alert.Alert || alert.Name != "HighErrorRate" || alert.For != 10*time.Minute {
		t.Errorf("rule 1 = %+v, want alert HighErrorRate for=10m", alert)
	}
	if alert.Labels["severity"] != "page" {
		t.Errorf("labels = %v, want severity=page", alert.Labels)
	}
}

func TestLoadRuleFilesRejectsGarbage(t *testing.T) {
	if _, err := backtest.LoadRuleFiles("testdata/rules-invalid.yaml"); err == nil {
		t.Error("garbage rule file loaded without error")
	}
}

func TestLoadRuleFilesMissing(t *testing.T) {
	if _, err := backtest.LoadRuleFiles("testdata/nope.yaml"); err == nil {
		t.Error("missing file loaded without error")
	}
}
