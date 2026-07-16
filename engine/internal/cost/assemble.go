package cost

import (
	"context"
	"time"
)

// SnapshotStore is the persistence port the showback pipeline needs — a subset
// of the store.Store interface, defined here so cost stays free of a store
// import (store imports cost, not the reverse). store.Postgres satisfies it
// structurally.
type SnapshotStore interface {
	SaveCostSnapshot(ctx context.Context, report Report, takenAt time.Time) (int64, error)
	LastCostSnapshot(ctx context.Context, before time.Time) (*Report, time.Time, error)
}

// modeledNote is the standing honesty caveat on every cost report.
const modeledNote = "Costs are modeled from your pricing.yaml, not billed — they are as accurate as your rates."

// Assemble runs the showback pipeline end to end: gather usage from the wired
// sources, price it, model lifecycle savings, and — when a store is provided —
// compute the week-over-week trend against the last snapshot and persist this
// one. This is what `argus cost` runs; injecting Sources and SnapshotStore
// keeps it unit-testable without live backends.
func Assemble(ctx context.Context, p *Pricing, srcs Sources, window time.Duration, store SnapshotStore, now time.Time) (Showback, error) {
	usage, err := Gather(ctx, srcs, window)
	if err != nil {
		return Showback{}, err
	}
	report := Price(p, usage)

	sb := Showback{
		GeneratedAt: now.UTC(),
		Window:      window.String(),
		Report:      report,
		Lifecycle:   LifecycleSavings(usage.Storage, DefaultLifecycleRules(), p),
		Notes:       []string{modeledNote},
	}

	if store != nil {
		prev, _, err := store.LastCostSnapshot(ctx, now)
		if err != nil {
			return Showback{}, err
		}
		if prev != nil {
			tr := Trend(report, *prev)
			sb.Trend = &tr
		}
		if _, err := store.SaveCostSnapshot(ctx, report, now); err != nil {
			return Showback{}, err
		}
	}
	return sb, nil
}
