package main

import (
	"strings"
	"testing"
	"time"

	"github.com/tamen25/Argus/engine/internal/soak"
)

// The analyzer turns a soak output dir into the summary the calibrate
// discussion needs: memory verdict, rotation check, receiver errors, and
// threshold-relevant distributions.
func TestAnalyzeSoakDir(t *testing.T) {
	out, err := analyze("testdata/soakdir")
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{
		// memory: fixture RSS grows 52MB -> ~55MB (~6%), within flat bounds
		"memory: flat",
		// pairs_tracked drops 380 -> 190 mid-run: rotation visible
		"window rotation: observed",
		// no engine-errors.log in fixture
		"receiver errors: none recorded",
		// items/sec: (241000+482000+120500-1000-2000-500)/7200s ≈ 116.7
		"items/sec (avg): 116.7",
		// span-name distribution across 3 services, max is cart's 180
		"span_name_cardinality", "max 180",
		// metric attribute cardinality max 950
		"metric_attribute_cardinality", "max 950",
		// resource attr: service.version max 5
		"service.version", "max 5",
		// exemplar coverage: 1 of 2 services has exemplars
		"exemplar coverage: 1/2 services",
		// trace health ratios surfaced
		"orphan_ratio", "missing_root_ratio",
		// per-rule ratios from reports: ARG-LOG-001 across 2 services
		"ARG-LOG-001", "2 services",
		// last fleet score echoed
		"fleet score (last): 84.7",
		// clean fixture: continuity is affirmed, not just silent
		"run continuity: continuous",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("summary missing %q\n--- got ---\n%s", want, out)
		}
	}
}

// An interrupted run (daemon outage, engine restart) must be labeled
// SEGMENTED in the verdicts — its distributions under-represent steady state.
func TestAnalyzeSegmentedRun(t *testing.T) {
	out, err := analyze("testdata/soakdir-segmented")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "run continuity: SEGMENTED") {
		t.Errorf("segmented run not disclosed:\n%s", out)
	}
	if !strings.Contains(out, "gap") || !strings.Contains(out, "restart") {
		t.Errorf("gap/restart detail missing:\n%s", out)
	}
}

func TestAnalyzeMissingDir(t *testing.T) {
	if _, err := analyze("testdata/nope"); err == nil {
		t.Error("want error for missing dir")
	}
}

// Soak-3 lesson: the first ~2×window of a run is generation fill, not leak.
// The memory verdict must baseline AFTER warmup or a healthy plateau reads
// as +67% growth.
func TestMemoryVerdictExcludesWarmup(t *testing.T) {
	start := time.Date(2026, 7, 14, 17, 0, 0, 0, time.UTC)
	var s []soak.Sample
	for i := 0; i < 24; i++ { // 4h at 10-min samples
		rss := 100e6 // plateau after warmup: 95-105MB sawtooth
		if i%2 == 1 {
			rss = 95e6
		}
		if i < 12 { // first 2h: filling from 40MB
			rss = 40e6 + float64(i)*5e6
		}
		s = append(s, soak.Sample{TS: start.Add(time.Duration(i) * 10 * time.Minute), RSS: rss})
	}

	var with, without strings.Builder
	writeMemoryVerdict(&without, s, 0)
	writeMemoryVerdict(&with, s, 2*time.Hour)

	if !strings.Contains(without.String(), "GROWING") {
		t.Errorf("without warmup exclusion the fill phase should read as growth: %s", without.String())
	}
	if !strings.Contains(with.String(), "memory: flat") {
		t.Errorf("with warmup excluded the plateau must read flat: %s", with.String())
	}
	if !strings.Contains(with.String(), "warmup") {
		t.Errorf("verdict must disclose the excluded warmup: %s", with.String())
	}
}

// Runs shorter than the warmup keep all samples and say so, instead of
// silently judging nothing.
func TestMemoryVerdictShortRunFallsBack(t *testing.T) {
	start := time.Date(2026, 7, 14, 17, 0, 0, 0, time.UTC)
	var s []soak.Sample
	for i := 0; i < 6; i++ {
		s = append(s, soak.Sample{TS: start.Add(time.Duration(i) * time.Minute), RSS: 50e6})
	}
	var b strings.Builder
	writeMemoryVerdict(&b, s, 2*time.Hour)
	if !strings.Contains(b.String(), "shorter than the warmup") {
		t.Errorf("short-run fallback must be disclosed: %s", b.String())
	}
}
