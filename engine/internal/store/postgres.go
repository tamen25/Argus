package store

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5" // migrate pgx driver
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tamen25/Argus/engine/internal/cost"
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

// SaveCostSnapshot persists a priced cost report (Phase 2 showback history).
// The full report is stored as JSONB; total and currency stay queryable.
func (p *Postgres) SaveCostSnapshot(ctx context.Context, report cost.Report, takenAt time.Time) (int64, error) {
	payload, err := json.Marshal(report)
	if err != nil {
		return 0, err
	}
	var id int64
	err = p.pool.QueryRow(ctx,
		`INSERT INTO cost_snapshots (taken_at, currency, total_monthly, payload)
		 VALUES ($1, $2, $3, $4) RETURNING id`,
		takenAt, report.Currency, report.TotalMonthly, payload).Scan(&id)
	if err != nil {
		return 0, err
	}
	return id, nil
}

// LastCostSnapshot returns the most recent cost report strictly before
// `before`. No prior snapshot is not an error — it returns (nil, zero, nil),
// so a first run trends against an empty baseline instead of failing.
func (p *Postgres) LastCostSnapshot(ctx context.Context, before time.Time) (*cost.Report, time.Time, error) {
	var payload []byte
	var takenAt time.Time
	err := p.pool.QueryRow(ctx,
		`SELECT payload, taken_at FROM cost_snapshots WHERE taken_at < $1 ORDER BY taken_at DESC LIMIT 1`,
		before).Scan(&payload, &takenAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, time.Time{}, nil
	}
	if err != nil {
		return nil, time.Time{}, err
	}
	var report cost.Report
	if err := json.Unmarshal(payload, &report); err != nil {
		return nil, time.Time{}, err
	}
	return &report, takenAt, nil
}

// RuleRatios returns every persisted finding's violation ratio grouped by
// rule ID — calibration evidence from score-run history. Ordered scan keeps
// the result deterministic for a given database state.
func (p *Postgres) RuleRatios(ctx context.Context) (map[string][]float64, error) {
	rows, err := p.pool.Query(ctx, `SELECT rule_id, ratio FROM findings ORDER BY rule_id, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]float64{}
	for rows.Next() {
		var id string
		var ratio float64
		if err := rows.Scan(&id, &ratio); err != nil {
			return nil, err
		}
		out[id] = append(out[id], ratio)
	}
	return out, rows.Err()
}

// Close releases the pool.
func (p *Postgres) Close() { p.pool.Close() }
