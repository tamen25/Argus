package stats

import "testing"

// Calibrate and soak analysis use robust statistics only: telemetry
// distributions are heavy-tailed, so mean/σ would be dominated by outliers.
func TestRobustStats(t *testing.T) {
	vals := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 100}

	if got := Median(vals); got != 5 {
		t.Errorf("Median = %v, want 5 (lower of the two middles, nearest-rank)", got)
	}
	if got := Percentile(vals, 90); got != 9 {
		t.Errorf("P90 = %v, want 9 (nearest-rank)", got)
	}
	if got := Percentile(vals, 99); got != 100 {
		t.Errorf("P99 = %v, want 100", got)
	}
	// MAD around median 5: deviations {4,3,2,1,0,1,2,3,4,95} -> median 2 (lower middle)
	if got := MAD(vals); got != 2 {
		t.Errorf("MAD = %v, want 2", got)
	}
	if got := Max(vals); got != 100 {
		t.Errorf("Max = %v, want 100", got)
	}
}

func TestStatsEmptyAndSingle(t *testing.T) {
	if got := Median(nil); got != 0 {
		t.Errorf("Median(nil) = %v, want 0", got)
	}
	if got := Percentile([]float64{7}, 99); got != 7 {
		t.Errorf("P99 of single = %v, want 7", got)
	}
	if got := MAD([]float64{7}); got != 0 {
		t.Errorf("MAD of single = %v, want 0", got)
	}
}

// Callers pass unsorted data in map-iteration order; results must not depend
// on input order (deterministic output requirement).
func TestStatsInputOrderIndependent(t *testing.T) {
	a := []float64{9, 1, 5, 3, 7}
	b := []float64{3, 7, 9, 5, 1}
	if Median(a) != Median(b) || Percentile(a, 90) != Percentile(b, 90) {
		t.Error("stats depend on input order")
	}
	if a[0] != 9 || b[0] != 3 {
		t.Error("stats must not mutate the caller's slice")
	}
}

// R34: nearest-rank takes the ceiling of the rank. Rounding it gave P95 of
// 1..11 as 10, a value only 91% of the data is at or below.
func TestPercentileNearestRank(t *testing.T) {
	vals := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11}
	cases := map[float64]float64{
		95:  11, // rank 10.45 → 11
		50:  6,  // rank 5.5 → 6
		10:  2,  // rank 1.1 → 2
		0:   1,  // the smallest value
		100: 11,
	}
	for p, want := range cases {
		if got := Percentile(vals, p); got != want {
			t.Errorf("P%v = %v, want %v", p, got, want)
		}
	}
	if got := Max([]float64{3, -1, 7, 2}); got != 7 {
		t.Errorf("Max = %v, want 7", got)
	}
}
