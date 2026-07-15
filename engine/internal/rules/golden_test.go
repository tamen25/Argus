package rules_test

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/ptrace"

	"github.com/tamen25/Argus/engine/internal/ingest"
	"github.com/tamen25/Argus/engine/internal/rules"
)

var update = flag.Bool("update", false, "rewrite golden expected.json files")

// Golden-file contract (quality bar: golden test per rule): each directory
// under testdata/golden is one case. Inputs are OTLP/JSON payloads
// (traces.json, metrics.json, logs.json) and aggregates.json rows; the
// expected output is the full Snapshot in expected.json. The rules evaluated
// are the repo's real built-in rules from /rules.
//
// Inputs flow through the REAL ingest pipeline (trackers included) on a
// fixed clock that then advances past one window, so completed-window
// aggregates like trace_health are exercised end-to-end — the soak-3
// orphan false positive (per-service trace judgement) lived exactly in the
// gap between rule goldens and tracker unit tests.
func TestGolden(t *testing.T) {
	rs, err := rules.LoadDir(repoRules(t, "spec"), repoRules(t, "argus"))
	if err != nil {
		t.Fatalf("loading built-in rules: %v", err)
	}
	eng, err := rules.NewEngine(rs)
	if err != nil {
		t.Fatalf("engine: %v", err)
	}

	cases, err := os.ReadDir(filepath.Join("testdata", "golden"))
	if err != nil {
		t.Fatalf("no golden cases: %v", err)
	}
	for _, dir := range cases {
		if !dir.IsDir() {
			continue
		}
		t.Run(dir.Name(), func(t *testing.T) {
			base := filepath.Join("testdata", "golden", dir.Name())
			c := rules.NewCollector(eng)

			now := time.Unix(1_700_000_000, 0) // fixed epoch: goldens stay deterministic
			pipe := ingest.NewPipeline(c, ingest.TrackerOpts{
				Window: time.Hour,
				Now:    func() time.Time { return now },
			})

			if b, err := os.ReadFile(filepath.Join(base, "traces.json")); err == nil {
				var um ptrace.JSONUnmarshaler
				td, err := um.UnmarshalTraces(b)
				if err != nil {
					t.Fatalf("traces.json: %v", err)
				}
				pipe.ConsumeTraces(td)
			}
			if b, err := os.ReadFile(filepath.Join(base, "metrics.json")); err == nil {
				var um pmetric.JSONUnmarshaler
				md, err := um.UnmarshalMetrics(b)
				if err != nil {
					t.Fatalf("metrics.json: %v", err)
				}
				pipe.ConsumeMetrics(md)
			}
			if b, err := os.ReadFile(filepath.Join(base, "logs.json")); err == nil {
				var um plog.JSONUnmarshaler
				ld, err := um.UnmarshalLogs(b)
				if err != nil {
					t.Fatalf("logs.json: %v", err)
				}
				pipe.ConsumeLogs(ld)
			}
			if b, err := os.ReadFile(filepath.Join(base, "aggregates.json")); err == nil {
				var rows []rules.AggregateRow
				if err := json.Unmarshal(b, &rows); err != nil {
					t.Fatalf("aggregates.json: %v", err)
				}
				for _, row := range rows {
					c.ObserveAggregate(row)
				}
			}

			// cross one window boundary: completed-window aggregates
			// (trace_health) judge the finished generation
			now = now.Add(61 * time.Minute)
			pipe.AggregateRows()

			got, err := json.MarshalIndent(c.Snapshot(), "", "  ")
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			got = append(got, '\n')

			expPath := filepath.Join(base, "expected.json")
			if *update {
				if err := os.WriteFile(expPath, got, 0o644); err != nil {
					t.Fatalf("update: %v", err)
				}
				return
			}
			want, err := os.ReadFile(expPath)
			if err != nil {
				t.Fatalf("missing expected.json (run with -update): %v", err)
			}
			if string(got) != string(want) {
				t.Errorf("snapshot mismatch for %s\n--- got ---\n%s\n--- want ---\n%s", dir.Name(), got, want)
			}
		})
	}
}

func repoRules(t *testing.T, sub string) string {
	t.Helper()
	p, err := filepath.Abs(filepath.Join("..", "..", "..", "rules", sub))
	if err != nil {
		t.Fatal(err)
	}
	return p
}
