// Package store persists score snapshots and findings to Postgres (the only
// storage backend, master plan §3.4). The Store interface is the port; the
// pgx implementation is the adapter; unit tests elsewhere use fakes.
package store

import (
	"context"
	"time"

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
	Close()
}
