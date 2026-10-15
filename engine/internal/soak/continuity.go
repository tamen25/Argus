// Package soak reads scripts/soak.sh metrics output and judges run
// continuity. A soak interrupted by daemon outages or engine restarts still
// yields usable distributions, but they under-represent steady state —
// consumers (soakanalyze, rules calibrate) must disclose that, never hide it.
package soak

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Sample is one metrics.csv row.
type Sample struct {
	TS                                time.Time
	RSS, Pairs, Evictions, ItemsTotal float64
}

// metricsColumns are the metrics.csv columns ReadMetrics needs. soak.sh
// writes more; the rest are ignored.
var metricsColumns = []string{"ts", "rss_bytes", "pairs_tracked", "evictions_total",
	"items_traces", "items_metrics", "items_logs"}

// ReadMetrics parses a soak metrics.csv by header name (column order free).
// An empty field is 0: soak.sh leaves a counter blank when the engine did not
// report it that minute. A missing column, a value that is not a number, or a
// bad timestamp is an error, since reading it as 0 would invent a restart or
// a gap.
func ReadMetrics(path string) ([]Sample, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	r := csv.NewReader(f)
	header, err := r.Read()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	col := map[string]int{}
	for i, h := range header {
		col[h] = i
	}
	for _, name := range metricsColumns {
		if _, ok := col[name]; !ok {
			return nil, fmt.Errorf("%s: no %q column", path, name)
		}
	}

	var out []Sample
	for line := 2; ; line++ {
		rec, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		var bad error
		num := func(name string) float64 {
			field := rec[col[name]]
			if field == "" {
				return 0
			}
			v, err := strconv.ParseFloat(field, 64)
			if err != nil && bad == nil {
				bad = fmt.Errorf("%s line %d: %s %q is not a number", path, line, name, field)
			}
			return v
		}
		ts, err := time.Parse(time.RFC3339, rec[col["ts"]])
		if err != nil {
			return nil, fmt.Errorf("%s line %d: ts %q is not an RFC 3339 time", path, line, rec[col["ts"]])
		}
		sample := Sample{
			TS:         ts,
			RSS:        num("rss_bytes"),
			Pairs:      num("pairs_tracked"),
			Evictions:  num("evictions_total"),
			ItemsTotal: num("items_traces") + num("items_metrics") + num("items_logs"),
		}
		if bad != nil {
			return nil, bad
		}
		out = append(out, sample)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s: no samples", path)
	}
	return out, nil
}

// Gap is a hole in the sampling timeline.
type Gap struct {
	After   time.Time     // last sample before the hole
	Missing time.Duration // how long sampling was silent
}

// Continuity is the segmentation verdict for one run.
type Continuity struct {
	Samples  int
	Gaps     []Gap
	Restarts int // monotonic item counters reset -> engine restarted
}

// Segmented reports whether the run was interrupted.
func (c Continuity) Segmented() bool { return len(c.Gaps) > 0 || c.Restarts > 0 }

// String renders the one-line disclosure consumers embed in their output.
func (c Continuity) String() string {
	if !c.Segmented() {
		return fmt.Sprintf("continuous (%d samples)", c.Samples)
	}
	parts := []string{}
	if n := len(c.Gaps); n > 0 {
		longest := time.Duration(0)
		for _, g := range c.Gaps {
			if g.Missing > longest {
				longest = g.Missing
			}
		}
		parts = append(parts, fmt.Sprintf("%d gap(s), longest %s", n, longest.Round(time.Minute)))
	}
	if c.Restarts > 0 {
		parts = append(parts, fmt.Sprintf("%d engine restart(s)", c.Restarts))
	}
	return "SEGMENTED — " + strings.Join(parts, ", ")
}

// CheckContinuity flags sampling gaps (interval > 3× the median cadence)
// and engine restarts (item counters going backwards).
func CheckContinuity(s []Sample) Continuity {
	c := Continuity{Samples: len(s)}
	if len(s) < 2 {
		return c
	}

	deltas := make([]float64, 0, len(s)-1)
	for i := 1; i < len(s); i++ {
		deltas = append(deltas, s[i].TS.Sub(s[i-1].TS).Seconds())
	}
	sorted := append([]float64(nil), deltas...)
	sort.Float64s(sorted)
	median := sorted[(len(sorted)-1)/2]
	// floor keeps sub-minute cadences from flagging scheduler jitter
	threshold := 3 * median
	if threshold < 120 {
		threshold = 120
	}

	for i := 1; i < len(s); i++ {
		if d := deltas[i-1]; d > threshold {
			c.Gaps = append(c.Gaps, Gap{After: s[i-1].TS, Missing: time.Duration(d) * time.Second})
		}
		if s[i].ItemsTotal < s[i-1].ItemsTotal {
			c.Restarts++
		}
	}
	return c
}
