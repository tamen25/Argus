package soak

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeCSV(t *testing.T, rows string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "metrics.csv")
	header := "ts,rss_bytes,go_heap_bytes,pairs_tracked,evictions_total,items_traces,items_metrics,items_logs,goroutines\n"
	if err := os.WriteFile(p, []byte(header+rows), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestContinuityContinuous(t *testing.T) {
	p := writeCSV(t, `2026-07-15T08:00:00Z,1,1,1,0,100,100,100,1
2026-07-15T08:01:00Z,1,1,1,0,200,200,200,1
2026-07-15T08:02:00Z,1,1,1,0,300,300,300,1
2026-07-15T08:03:00Z,1,1,1,0,400,400,400,1
`)
	s, err := ReadMetrics(p)
	if err != nil {
		t.Fatal(err)
	}
	c := CheckContinuity(s)
	if c.Segmented() {
		t.Errorf("continuous run flagged segmented: %+v", c)
	}
}

// A sampling hole (harness or docker outage) segments the run: the
// distributions may under-represent steady state and calibrate must say so.
func TestContinuityDetectsGap(t *testing.T) {
	p := writeCSV(t, `2026-07-15T08:00:00Z,1,1,1,0,100,100,100,1
2026-07-15T08:01:00Z,1,1,1,0,200,200,200,1
2026-07-15T08:45:00Z,1,1,1,0,300,300,300,1
2026-07-15T08:46:00Z,1,1,1,0,400,400,400,1
`)
	s, _ := ReadMetrics(p)
	c := CheckContinuity(s)
	if !c.Segmented() || len(c.Gaps) != 1 {
		t.Fatalf("gap not detected: %+v", c)
	}
	if c.Gaps[0].Missing < 40*time.Minute {
		t.Errorf("gap length = %v, want ~44m", c.Gaps[0].Missing)
	}
	if !strings.Contains(c.String(), "gap") {
		t.Errorf("String() = %q", c.String())
	}
}

// Monotonic item counters resetting means the engine restarted mid-run:
// aggregate windows started over.
func TestContinuityDetectsRestart(t *testing.T) {
	p := writeCSV(t, `2026-07-15T08:00:00Z,1,1,1,0,100,100,100,1
2026-07-15T08:01:00Z,1,1,1,0,200,200,200,1
2026-07-15T08:02:00Z,1,1,1,0,5,5,5,1
2026-07-15T08:03:00Z,1,1,1,0,50,50,50,1
`)
	s, _ := ReadMetrics(p)
	c := CheckContinuity(s)
	if !c.Segmented() || c.Restarts != 1 {
		t.Fatalf("restart not detected: %+v", c)
	}
	if !strings.Contains(c.String(), "restart") {
		t.Errorf("String() = %q", c.String())
	}
}

func TestContinuityTooFewSamples(t *testing.T) {
	p := writeCSV(t, "2026-07-15T08:00:00Z,1,1,1,0,100,100,100,1\n")
	s, _ := ReadMetrics(p)
	if c := CheckContinuity(s); c.Segmented() {
		t.Errorf("single sample must not be segmented: %+v", c)
	}
}
