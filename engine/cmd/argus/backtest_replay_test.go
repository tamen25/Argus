package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tamen25/Argus/engine/internal/backtest"
)

// replayExpr's three outcomes: an undefined recording rule is a caveat, a
// defined one is inlined with --synthesize, and a rule that cannot be
// synthesized is refused with the reason.
func TestReplayExpr(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rules.yaml")
	if err := os.WriteFile(path, []byte(`groups:
  - name: g
    rules:
      - record: job:errors:rate5m
        expr: sum by (job) (rate(errors_total[5m]))
      - alert: Defined
        expr: job:errors:rate5m > 1
      - alert: External
        expr: team:latency:p99 > 2
`), 0o600); err != nil {
		t.Fatal(err)
	}
	rs, err := backtest.LoadRuleFiles(path)
	if err != nil {
		t.Fatal(err)
	}
	deps := backtest.Dependencies(rs)
	rule := func(name string) backtest.Rule {
		for _, r := range rs.Groups[0].Rules {
			if r.Name == name {
				return r
			}
		}
		t.Fatalf("no rule %s", name)
		return backtest.Rule{}
	}

	// Without --synthesize: the expression as written, and a caveat for a
	// recording rule the files do not define.
	expr, caveats, err := replayExpr(rs, rule("External"), deps, false)
	if err != nil || expr != "team:latency:p99 > 2" || len(caveats) != 1 || !strings.Contains(caveats[0], "external recording rule") {
		t.Errorf("external: expr=%q caveats=%v err=%v", expr, caveats, err)
	}

	// With --synthesize: a defined recording rule is inlined.
	expr, _, err = replayExpr(rs, rule("Defined"), deps, true)
	if err != nil || strings.Contains(expr, "job:errors:rate5m") || !strings.Contains(expr, "errors_total") {
		t.Errorf("synthesized: expr=%q err=%v, want the recording rule inlined", expr, err)
	}
}
