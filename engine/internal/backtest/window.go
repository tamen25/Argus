package backtest

import (
	"context"
	"fmt"
	"time"
)

// InstantQuerier is the port the usable-window probe needs: does any sample
// exist at time t? The Mimir adapter implements it with an instant query;
// tests use fakes (architecture rule 1).
type InstantQuerier interface {
	HasData(ctx context.Context, t time.Time) (bool, error)
}

// UsableWindow finds the earliest timestamp in [earliest, latest] that has
// data, to within one resolution step — fidelity failure mode (c): Mimir
// retention (or first write) bounds the backtest window, and the engine must
// report the window it can actually see instead of failing silently.
//
// Binary search: O(log(range/resolution)) queries, assuming data presence is
// monotone (absent before some point, present after). Out-of-order backfill
// can violate that assumption; the caller gets the boundary the search
// converged on, which is still the honest "queries older than this saw
// nothing" line.
func UsableWindow(ctx context.Context, q InstantQuerier, earliest, latest time.Time, resolution time.Duration) (time.Time, error) {
	ok, err := q.HasData(ctx, latest)
	if err != nil {
		return time.Time{}, err
	}
	if !ok {
		return time.Time{}, fmt.Errorf("no data at %s — nothing to backtest in the searched range", latest.Format(time.RFC3339))
	}

	lo, hi := earliest, latest // invariant: no data seen at lo*, data exists at hi
	if ok, err := q.HasData(ctx, lo); err != nil {
		return time.Time{}, err
	} else if ok {
		return lo, nil // data reaches back past the search start
	}

	for hi.Sub(lo) > resolution {
		mid := lo.Add(hi.Sub(lo) / 2)
		ok, err := q.HasData(ctx, mid)
		if err != nil {
			return time.Time{}, err
		}
		if ok {
			hi = mid
		} else {
			lo = mid
		}
	}
	return hi, nil
}
