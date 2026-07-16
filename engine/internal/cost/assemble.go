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

// modeledNote and calibrationNote are the standing honesty caveats on every
// cost report (architecture rule 7). Self-hosted LGTM has no invoice, so a
// dollar figure here is a model — and the shipped rate templates are
// illustrative until the user calibrates them. Both facts ride on every
// rendering (Markdown, JSON, and the plugin Spend page) so a screenshot can
// never present a modeled number as a billed one.
const (
	modeledNote     = "Costs are modeled from your pricing.yaml, not billed — they are as accurate as your rates."
	calibrationNote = "Shipped pricing templates are illustrative: S3 storage classes use AWS list prices, but ingest and active-series rates are estimates — edit pricing.yaml to match your environment."
)

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
		Notes:       []string{modeledNote, calibrationNote},
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
