package cost

import (
	"sort"
	"time"
)

// TrendReport is week-over-week cost movement between two reports.
type TrendReport struct {
	Currency string `json:"currency"`
	// BaselineAt is when the report compared against was taken: the latest
	// snapshot at least a week old. Zero when Trend was called directly.
	BaselineAt time.Time   `json:"baseline_at,omitzero"`
	Lines      []TrendLine `json:"lines"`
	TotalDelta float64     `json:"total_delta"` // current total − previous total
}

// TrendLine is one (service, signal) line's movement. A line new this period
// has Previous 0; one that vanished has Current 0 (and a negative Delta).
type TrendLine struct {
	Service      string  `json:"service"`
	Team         string  `json:"team,omitempty"`
	Signal       string  `json:"signal"`
	Current      float64 `json:"current"`
	Previous     float64 `json:"previous"`
	Delta        float64 `json:"delta"`         // Current − Previous
	PercentDelta float64 `json:"percent_delta"` // Delta / Previous × 100; 0 when Previous is 0
}

// Trend computes per-line and total deltas between cur and prev. Deterministic
// and sorted; lines present in either report appear. PercentDelta is 0 against
// a zero baseline (a new line is not "infinite%" growth — it has no baseline).
func Trend(cur, prev Report) TrendReport {
	type acc struct {
		service, team, signal string
		cur, prev             float64
	}
	byKey := map[lineKey]*acc{}
	add := func(l Line, isCur bool) {
		k := lineKey{l.Service, l.Team, l.Signal}
		a, ok := byKey[k]
		if !ok {
			a = &acc{service: l.Service, team: l.Team, signal: l.Signal}
			byKey[k] = a
		}
		if isCur {
			a.cur += l.TotalMonthly
		} else {
			a.prev += l.TotalMonthly
		}
	}
	for _, l := range cur.Lines {
		add(l, true)
	}
	for _, l := range prev.Lines {
		add(l, false)
	}

	tr := TrendReport{
		Currency:   cur.Currency,
		Lines:      make([]TrendLine, 0, len(byKey)), // [] not null when there are none: the plugin reads .length
		TotalDelta: cur.TotalMonthly - prev.TotalMonthly,
	}
	if tr.Currency == "" {
		tr.Currency = prev.Currency
	}
	for _, a := range byKey {
		pct := 0.0
		if a.prev > 0 {
			pct = (a.cur - a.prev) / a.prev * 100
		}
		tr.Lines = append(tr.Lines, TrendLine{
			Service: a.service, Team: a.team, Signal: a.signal,
			Current: a.cur, Previous: a.prev, Delta: a.cur - a.prev, PercentDelta: pct,
		})
	}
	// Sorted on the whole line key: map order is random, so a partial key
	// would order two teams' lines for one service and signal differently on
	// every run.
	sort.Slice(tr.Lines, func(i, j int) bool {
		a, b := tr.Lines[i], tr.Lines[j]
		if a.Service != b.Service {
			return a.Service < b.Service
		}
		if a.Signal != b.Signal {
			return a.Signal < b.Signal
		}
		return a.Team < b.Team
	})
	return tr
}
