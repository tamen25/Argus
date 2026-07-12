package store

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5" // migrate pgx driver
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tamen25/Argus/engine/internal/rules"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Postgres implements Store over a pgx pool.
type Postgres struct {
	pool *pgxpool.Pool
}

// Open connects, runs migrations, and returns the store.
func Open(ctx context.Context, dsn string) (*Postgres, error) {
	if err := Migrate(dsn); err != nil {
		return nil, fmt.Errorf("migrating: %w", err)
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return &Postgres{pool: pool}, nil
}

// Migrate applies embedded migrations (golang-migrate, master plan §3.4).
func Migrate(dsn string) error {
	src, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		return err
	}
	m, err := migrate.NewWithSourceInstance("iofs", src, "pgx5://"+trimScheme(dsn))
	if err != nil {
		return err
	}
	defer func() { _, _ = m.Close() }()
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return err
	}
	return nil
}

// trimScheme strips a postgres:// or postgresql:// prefix so the migrate
// pgx5 scheme can be applied.
func trimScheme(dsn string) string {
	for _, p := range []string{"postgres://", "postgresql://"} {
		if len(dsn) > len(p) && dsn[:len(p)] == p {
			return dsn[len(p):]
		}
	}
	return dsn
}

// SaveSnapshot writes the snapshot and its findings in one transaction.
func (p *Postgres) SaveSnapshot(ctx context.Context, snap *rules.Snapshot, meta Meta) (int64, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var id int64
	err = tx.QueryRow(ctx,
		`INSERT INTO score_snapshots (taken_at, fleet_score, spec_version, argus_version)
		 VALUES ($1, $2, $3, $4) RETURNING id`,
		meta.TakenAt, snap.FleetScore, meta.SpecVersion, meta.ArgusVersion).Scan(&id)
	if err != nil {
		return 0, err
	}

	for _, svc := range snap.Services {
		_, err = tx.Exec(ctx,
			`INSERT INTO service_scores (snapshot_id, service, spec_score, extension_score, category)
			 VALUES ($1, $2, $3, $4, $5)`,
			id, svc.ServiceName, svc.SpecScore, svc.ExtensionScore, svc.Category)
		if err != nil {
			return 0, err
		}
		for _, f := range svc.Findings {
			payload, err := json.Marshal(f)
			if err != nil {
				return 0, err
			}
			_, err = tx.Exec(ctx,
				`INSERT INTO findings (snapshot_id, service, rule_id, source, impact, confidence,
				                       observed, violations, ratio, payload)
				 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
				id, f.Service, f.RuleID, f.Source, f.Impact, f.Confidence,
				f.Stats.Observed, f.Stats.Violations, f.Stats.Ratio, payload)
			if err != nil {
				return 0, err
			}
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return id, nil
}

// Close releases the pool.
func (p *Postgres) Close() { p.pool.Close() }
