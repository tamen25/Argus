//go:build integration

package store

import (
	"context"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/tamen25/Argus/engine/internal/rules"
)

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
