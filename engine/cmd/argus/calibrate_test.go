package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tamen25/Argus/engine/internal/rules"
)

func TestRunCalibrateFromSoakDir(t *testing.T) {
	out := t.TempDir()
	md, err := runCalibrate(context.Background(), &calibrateOptions{
		soakDir: filepath.Join("testdata", "calibrate-soak"),
		outDir:  out,
	})
	if err != nil {
		t.Fatal(err)
	}

	// proposals for every calibratable rule with data in the fixture
	for _, want := range []string{"SPA-003", "MET-001", "SPA-002", "ARG-SPA-002", "ARG-RES-004", "ARG-LOG-001"} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown missing %s\n%s", want, md)
		}
	}
	if !strings.Contains(md, "failing services only") {
		t.Error("finding_ratio disclosure missing")
	}

	// emitted overrides load, and merging replaces the builtin param
	custom, err := rules.LoadDir(out)
	if err != nil {
		t.Fatalf("emitted overrides do not load: %v", err)
	}
	if len(custom) != 6 {
		t.Errorf("override files = %d rules, want 6", len(custom))
	}
	for _, r := range custom {
		if r.ID == "SPA-003" {
			// fixture P99 = 180 -> ×2 = 360
			if v, _ := r.Params["max_span_names"].(int); v != 360 {
				t.Errorf("SPA-003 override max_span_names = %v, want 360", r.Params["max_span_names"])
			}
		}
	}

	// determinism: byte-identical second run
	out2 := t.TempDir()
	md2, err := runCalibrate(context.Background(), &calibrateOptions{
		soakDir: filepath.Join("testdata", "calibrate-soak"),
		outDir:  out2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if md != md2 {
		t.Error("markdown not deterministic")
	}
	a, _ := os.ReadFile(filepath.Join(out, "spa-003.yaml"))
	b, _ := os.ReadFile(filepath.Join(out2, "spa-003.yaml"))
	if len(a) == 0 || string(a) != string(b) {
		t.Error("override files not deterministic")
	}
}

// A soak dir whose metrics.csv shows gaps/restarts must stamp the proposal
// with the segmented-run disclosure (evidence quality travels with the
// evidence).
func TestRunCalibrateDisclosesSegmentedRun(t *testing.T) {
	md, err := runCalibrate(context.Background(), &calibrateOptions{
		soakDir: filepath.Join("testdata", "calibrate-soak-segmented"),
		outDir:  t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(md, "SEGMENTED") {
		t.Errorf("segmented soak not disclosed in proposal:\n%s", md)
	}
}

func TestRunCalibrateRequiresData(t *testing.T) {
	if _, err := runCalibrate(context.Background(), &calibrateOptions{soakDir: "testdata/nope", outDir: t.TempDir()}); err == nil {
		t.Error("want error for missing soak dir")
	}
}
