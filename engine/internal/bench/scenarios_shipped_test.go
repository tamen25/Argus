package bench_test

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tamen25/Argus/engine/internal/bench"
	"gopkg.in/yaml.v3"
)

// scenarioDir is the repo's shipped scenario library, relative to this package.
const scenarioDir = "../../../scenarios"

// TestShippedScenariosLoad parses every scenario in the library. The loader is
// strict, so this catches a typo'd key or a bad envelope at test time rather
// than partway through a run matrix that has already spent an hour.
func TestShippedScenariosLoad(t *testing.T) {
	paths := shippedScenarios(t)
	if len(paths) == 0 {
		t.Fatal("no scenarios found — the library should not be empty")
	}
	for _, p := range paths {
		t.Run(filepath.Base(p), func(t *testing.T) {
			if _, err := bench.LoadScenario(p); err != nil {
				t.Fatalf("scenario does not load: %v", err)
			}
		})
	}
}

// TestShippedScenariosReferenceExistingManifests: a scenario naming a manifest
// that is not in the repo fails only at inject time, after the environment has
// been reset and the clock has started. Scenario 1 shipped in exactly that state
// for weeks.
func TestShippedScenariosReferenceExistingManifests(t *testing.T) {
	for _, p := range shippedScenarios(t) {
		sc, err := bench.LoadScenario(p)
		if err != nil {
			continue // covered by TestShippedScenariosLoad
		}
		t.Run(sc.Metadata.Name, func(t *testing.T) {
			exists := func(where, ref string) {
				if ref == "" {
					return
				}
				if _, err := os.Stat(filepath.Join(filepath.Dir(p), ref)); err != nil {
					t.Errorf("%s references %q which does not exist: %v", where, ref, err)
				}
			}
			for i, step := range sc.Spec.Inject {
				exists(fmt.Sprintf("inject[%d]", i), step.Manifest+step.Script)
			}
			// Hooks too: a mistyped restore path would otherwise surface only
			// mid-run, after the fault is already injected and cannot be undone.
			for i, h := range sc.Spec.Reset {
				exists(fmt.Sprintf("reset[%d]", i), h.Script)
			}
			for i, h := range sc.Spec.Cleanup {
				exists(fmt.Sprintf("cleanup[%d]", i), h.Script)
			}
		})
	}
}

// TestShippedScenariosAreScorable enforces the rubric floor the calibration work
// established. A scenario without cited evidence or a meaningful category weight
// cannot distinguish a diagnosis from a lucky guess, and one without decoys
// cannot penalize a confident wrong attribution.
func TestShippedScenariosAreScorable(t *testing.T) {
	for _, p := range shippedScenarios(t) {
		sc, err := bench.LoadScenario(p)
		if err != nil {
			continue
		}
		t.Run(sc.Metadata.Name, func(t *testing.T) {
			if !sc.Spec.Scoring.RequireEvidence {
				t.Error("requireEvidence is false: an uncited guess would score full marks")
			}
			if w := sc.Spec.Scoring.EffectiveCategoryWeight(); w <= 0 {
				t.Errorf("categoryWeight %v: naming the right entity for the wrong reason would score full marks", w)
			}
			if len(sc.Spec.GroundTruth.Decoys) == 0 {
				t.Error("no decoys: nothing penalizes a confident wrong attribution")
			}
			if sc.Spec.SteadyState == nil {
				t.Error("no steadyState: the agent would be asked before the fault is observable, " +
					"and an ineffective fault would score against a healthy environment")
			}
		})
	}
}

// TestShippedScenarioNamesMatchFilenames keeps the library navigable: a report
// cites metadata.name, and hunting for the file that produced it should not
// require grep.
func TestShippedScenarioNamesMatchFilenames(t *testing.T) {
	for _, p := range shippedScenarios(t) {
		sc, err := bench.LoadScenario(p)
		if err != nil {
			continue
		}
		want := strings.TrimSuffix(filepath.Base(p), ".yaml")
		if sc.Metadata.Name != want {
			t.Errorf("%s declares metadata.name %q", filepath.Base(p), sc.Metadata.Name)
		}
	}
}

func shippedScenarios(t *testing.T) []string {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(scenarioDir, "*.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	return paths
}

// TestFaultManifestsCarryTheSweepLabel: reset sweeps every object labeled
// app.kubernetes.io/managed-by=argus-bench, which is what catches a fault leaked
// by a crashed run or a manual test. An object without the label is one the
// sweep can never clean up, so every object in every fault manifest must carry it.
func TestFaultManifestsCarryTheSweepLabel(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join(scenarioDir, "faults", "*.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatal("no fault manifests found")
	}
	for _, p := range paths {
		f, err := os.Open(p)
		if err != nil {
			t.Fatal(err)
		}
		dec := yaml.NewDecoder(f)
		for doc := 0; ; doc++ {
			var obj struct {
				Kind     string `yaml:"kind"`
				Metadata struct {
					Name   string            `yaml:"name"`
					Labels map[string]string `yaml:"labels"`
				} `yaml:"metadata"`
			}
			if err := dec.Decode(&obj); err != nil {
				if errors.Is(err, io.EOF) {
					break
				}
				t.Fatalf("%s doc %d: %v", filepath.Base(p), doc, err)
			}
			if obj.Kind == "" {
				continue // empty document between separators
			}
			if got := obj.Metadata.Labels["app.kubernetes.io/managed-by"]; got != "argus-bench" {
				t.Errorf("%s: %s/%s lacks app.kubernetes.io/managed-by=argus-bench (got %q); "+
					"reset's sweep could never remove it", filepath.Base(p), obj.Kind, obj.Metadata.Name, got)
			}
		}
		_ = f.Close()
	}
}
