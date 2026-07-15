// Package soak reads scripts/soak.sh metrics output and judges run
// continuity. A soak interrupted by daemon outages or engine restarts still
// yields usable distributions, but they under-represent steady state —
// consumers (soakanalyze, rules calibrate) must disclose that, never hide it.
package soak

import (
	"encoding/csv"
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

// ReadMetrics parses a soak metrics.csv by header name (column order free).
func ReadMetrics(path string) ([]Sample, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	r := csv.NewReader(f)
	header, err := r.Read()
	if err != nil {
		return nil, err
	}
	col := map[string]int{}
	for i, h := range header {
		col[h] = i
	}
	var out []Sample
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		num := func(name string) float64 {
			v, _ := strconv.ParseFloat(rec[col[name]], 64)
			return v
		}
		ts, _ := time.Parse(time.RFC3339, rec[col["ts"]])
		out = append(out, Sample{
			TS:         ts,
			RSS:        num("rss_bytes"),
			Pairs:      num("pairs_tracked"),
			Evictions:  num("evictions_total"),
			ItemsTotal: num("items_traces") + num("items_metrics") + num("items_logs"),
		})
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
