// Package store persists score snapshots and findings to Postgres (the only
// storage backend, master plan §3.4). The Store interface is the port; the
// pgx implementation is the adapter; unit tests elsewhere use fakes.
package store

import (
	"context"
	"time"

	"github.com/tamen25/Argus/engine/internal/cost"
	"github.com/tamen25/Argus/engine/internal/rules"
)

// Meta describes the run that produced a snapshot.
type Meta struct {
	TakenAt      time.Time
	SpecVersion  string
	ArgusVersion string
}

// Store is the persistence port.
type Store interface {
	SaveSnapshot(ctx context.Context, snap *rules.Snapshot, meta Meta) (int64, error)
	// SaveCostSnapshot persists a priced cost report for week-over-week trends.
	SaveCostSnapshot(ctx context.Context, report cost.Report, takenAt time.Time) (int64, error)
	// LastCostSnapshot returns the most recent cost report strictly before
	// `before`, or nil (with a zero time) when there is no prior baseline.
	LastCostSnapshot(ctx context.Context, before time.Time) (*cost.Report, time.Time, error)
	Close()
}
