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

// A blank counter is one the engine did not report that minute: 0. Anything
// else that does not parse is an error naming the line, since reading it as 0
// would invent an engine restart, and a bad time would invent a gap.
func TestReadMetricsRejectsWhatItCannotRead(t *testing.T) {
	blank := writeCSV(t, "2026-07-15T08:00:00Z,1,1,1,0,,,,1\n")
	s, err := ReadMetrics(blank)
	if err != nil || s[0].ItemsTotal != 0 {
		t.Errorf("blank counters: %+v, %v; want 0 and no error", s, err)
	}

	for name, rows := range map[string]string{
		"a value that is not a number": "2026-07-15T08:00:00Z,1,1,1,0,lots,0,0,1\n",
		"a bad timestamp":              "15/07/2026 08:00,1,1,1,0,1,1,1,1\n",
	} {
		if _, err := ReadMetrics(writeCSV(t, rows)); err == nil || !strings.Contains(err.Error(), "line 2") {
			t.Errorf("%s: err = %v, want an error naming line 2", name, err)
		}
	}

	p := filepath.Join(t.TempDir(), "metrics.csv")
	if err := os.WriteFile(p, []byte("ts,rss_bytes\n2026-07-15T08:00:00Z,1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadMetrics(p); err == nil || !strings.Contains(err.Error(), "pairs_tracked") {
		t.Errorf("missing columns: err = %v, want one naming the column", err)
	}
}
