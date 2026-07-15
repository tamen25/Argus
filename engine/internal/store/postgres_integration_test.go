//go:build integration

package store

import (
	"context"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/tamen25/Argus/engine/internal/cost"
	"github.com/tamen25/Argus/engine/internal/rules"
)

func newTestStore(t *testing.T) (*Postgres, context.Context) {
	t.Helper()
	ctx := context.Background()
	pg, err := postgres.Run(ctx, "postgres:17-alpine",
		postgres.WithDatabase("argus"), postgres.WithUsername("argus"), postgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(wait.ForLog("database system is ready to accept connections").WithOccurrence(2)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pg.Terminate(ctx) })
	dsn, err := pg.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	s, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s, ctx
}

// Cost snapshots persist and read back for week-over-week trends; the first
// run has no prior baseline (nil, not an error).
func TestPostgresCostSnapshots(t *testing.T) {
	s, ctx := newTestStore(t)

	// no prior snapshot yet
	prev, _, err := s.LastCostSnapshot(ctx, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if prev != nil {
		t.Fatalf("want nil prior snapshot, got %+v", prev)
	}

	t0 := time.Now().UTC().Add(-7 * 24 * time.Hour)
	older := cost.Report{Currency: "USD", Lines: []cost.Line{{Service: "checkout", Signal: "metrics", TotalMonthly: 100}}, TotalMonthly: 100}
	if _, err := s.SaveCostSnapshot(ctx, older, t0); err != nil {
		t.Fatal(err)
	}
	newer := cost.Report{Currency: "USD", Lines: []cost.Line{{Service: "checkout", Signal: "metrics", TotalMonthly: 130}}, TotalMonthly: 130}
	if _, err := s.SaveCostSnapshot(ctx, newer, t0.Add(7*24*time.Hour)); err != nil {
		t.Fatal(err)
	}

	// the report just before "now" is the newer one; before t0+1s is the older
	got, at, err := s.LastCostSnapshot(ctx, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.TotalMonthly != 130 {
		t.Fatalf("last snapshot = %+v, want total 130", got)
	}
	if at.Before(t0) {
		t.Errorf("taken_at = %v, want ~%v", at, t0.Add(7*24*time.Hour))
	}

	got, _, err = s.LastCostSnapshot(ctx, t0.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.TotalMonthly != 100 {
		t.Errorf("snapshot before t0+1s = %+v, want the older total 100", got)
	}
}

func TestPostgresSaveSnapshot(t *testing.T) {
	ctx := context.Background()
	pg, err := postgres.Run(ctx, "postgres:17-alpine",
		postgres.WithDatabase("argus"), postgres.WithUsername("argus"), postgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(wait.ForLog("database system is ready to accept connections").WithOccurrence(2)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pg.Terminate(ctx) })

	dsn, err := pg.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}

	s, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	ext := 50.0
	snap := &rules.Snapshot{
		FleetScore: 75,
		Services: []rules.ServiceReport{{
			ServiceName: "checkout", SpecScore: 75, Category: "Good", ExtensionScore: &ext,
			Findings: []rules.Finding{{
				RuleID: "RES-005", RuleName: "service.name is present", Source: "spec",
				Service: "checkout", Impact: rules.ImpactCritical, Confidence: rules.ConfidenceSampled,
				Stats: rules.Stats{Observed: 10, Violations: 2, Ratio: 0.2},
			}},
		}},
	}
	id, err := s.SaveSnapshot(ctx, snap, Meta{TakenAt: time.Now().UTC(), SpecVersion: "e6ee2227", ArgusVersion: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if id == 0 {
		t.Error("id = 0, want > 0")
	}

	var count int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM findings WHERE snapshot_id = $1`, id).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("findings = %d, want 1", count)
	}
}
