package backtest

import (
	"context"
	"time"
)

// Segment is a contiguous run of telemetry presence, accurate to the probing
// stride.
type Segment struct {
	Start, End time.Time
}

// Segments sweeps [from, to] at the given stride and returns the maximal runs
// of data presence — fidelity failure modes (c) and (d) generalized: history
// is bounded by retention AND holed by outages, off-hours dev sessions, and
// series lifecycle. The backtest evaluates within segments and reports
// coverage (sum of segment lengths vs the calendar range) so a result over
// gappy history can never masquerade as continuous evidence.
//
// Cost: one instant query per stride step — a 6-day range at 1h stride is 145
// queries. Callers pick the stride to trade boundary precision for query load.
func Segments(ctx context.Context, q InstantQuerier, from, to time.Time, stride time.Duration) ([]Segment, error) {
	var segs []Segment
	var open *Segment
	for t := from; !t.After(to); t = t.Add(stride) {
		ok, err := q.HasData(ctx, t)
		if err != nil {
			return nil, err
		}
		switch {
		case ok && open == nil:
			open = &Segment{Start: t, End: t}
		case ok:
			open.End = t
		case open != nil:
			segs = append(segs, *open)
			open = nil
		}
	}
	if open != nil {
		segs = append(segs, *open)
	}
	return segs, nil
}
