// Package stats provides the robust statistics used by soak analysis and
// rule calibration. Telemetry distributions are heavy-tailed, so only
// order statistics (median, MAD, percentiles) are offered — no mean/σ.
// All functions are deterministic (nearest-rank, lower-middle median) and
// leave their input untouched.
package stats

import "sort"

func sorted(vals []float64) []float64 {
	s := make([]float64, len(vals))
	copy(s, vals)
	sort.Float64s(s)
	return s
}

// Median returns the lower of the two middle values for even-length input
// (nearest-rank family; deterministic, never invents a value).
func Median(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	s := sorted(vals)
	return s[(len(s)-1)/2]
}

// Percentile returns the nearest-rank p-th percentile (p in 0..100).
func Percentile(vals []float64, p float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	s := sorted(vals)
	rank := int(p/100*float64(len(s))+0.5) - 1
	if rank < 0 {
		rank = 0
	}
	if rank >= len(s) {
		rank = len(s) - 1
	}
	return s[rank]
}

// MAD is the median absolute deviation around the median — the robust
// spread measure calibration uses instead of standard deviation.
func MAD(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	m := Median(vals)
	devs := make([]float64, len(vals))
	for i, v := range vals {
		d := v - m
		if d < 0 {
			d = -d
		}
		devs[i] = d
	}
	return Median(devs)
}

// Max returns the largest value (0 for empty input).
func Max(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	s := sorted(vals)
	return s[len(s)-1]
}
